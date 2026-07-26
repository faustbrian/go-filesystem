package sftp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/fstest"
	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestConformance(t *testing.T) {
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		state := newFakeState()
		adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
			return &fakeSession{state: state, atomicRename: true}, nil
		}, "/storage", 100)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = adapter.Close() })
		return adapter
	})
}

func TestNewRequiresHostKeyVerificationAndAuthentication(t *testing.T) {
	t.Parallel()

	tests := []Config{
		{Address: "example.test:22", User: "user", Auth: []ssh.AuthMethod{ssh.Password("secret")}},
		{Address: "example.test:22", User: "user", HostKeyCallback: ssh.InsecureIgnoreHostKey()},
		{Address: "example.test:22", Auth: []ssh.AuthMethod{ssh.Password("secret")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()},
		{User: "user", Auth: []ssh.AuthMethod{ssh.Password("secret")}, HostKeyCallback: ssh.InsecureIgnoreHostKey()},
	}
	for _, configuration := range tests {
		configuration := configuration
		t.Run(configuration.Address+configuration.User, func(t *testing.T) {
			t.Parallel()
			if _, err := New(context.Background(), configuration); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func TestStatReconnectsOnceAfterConnectionLoss(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	state.objects["/storage/file.txt"] = []byte("content")
	first := &fakeSession{state: state, atomicRename: true, statError: pkgsftp.ErrSSHFxConnectionLost}
	second := &fakeSession{state: state, atomicRename: true}
	connector := &scriptedConnector{sessions: []remoteSession{first, second}}
	adapter, err := newAdapter(context.Background(), connector.Connect, "/storage", 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	metadata, err := adapter.Stat(context.Background(), filesystem.MustParsePath("file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Size != int64(len("content")) {
		t.Fatalf("Stat().Size = %d", metadata.Size)
	}
	if connector.Calls() != 2 {
		t.Fatalf("connector calls = %d, want 2", connector.Calls())
	}
}

func TestWriteDoesNotReplayConsumedStreamAfterConnectionLoss(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	first := &fakeSession{state: state, atomicRename: true, writeError: pkgsftp.ErrSSHFxConnectionLost}
	second := &fakeSession{state: state, atomicRename: true}
	connector := &scriptedConnector{sessions: []remoteSession{first, second}}
	adapter, err := newAdapter(context.Background(), connector.Connect, "/storage", 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	_, err = adapter.Write(
		context.Background(),
		filesystem.MustParsePath("file.txt"),
		strings.NewReader("content"),
		filesystem.WriteOptions{},
	)
	if !errors.Is(err, pkgsftp.ErrSSHFxConnectionLost) {
		t.Fatalf("Write() error = %v", err)
	}
	if connector.Calls() != 1 {
		t.Fatalf("connector calls = %d, want 1", connector.Calls())
	}
	if len(state.objects) != 0 {
		t.Fatalf("partial objects = %v", state.objects)
	}
}

func TestCanceledOpenStreamStopsReading(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	state.objects["/storage/file.txt"] = []byte("content")
	adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
		return &fakeSession{state: state, atomicRename: true}, nil
	}, "/storage", 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := adapter.Open(ctx, filesystem.MustParsePath("file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	buffer := make([]byte, 1)
	if _, err := stream.Read(buffer); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v, want context.Canceled", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMoveCapabilityRequiresPOSIXRename(t *testing.T) {
	t.Parallel()

	for _, atomicRename := range []bool{false, true} {
		atomicRename := atomicRename
		t.Run(map[bool]string{false: "standard", true: "posix"}[atomicRename], func(t *testing.T) {
			t.Parallel()
			adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
				return &fakeSession{state: newFakeState(), atomicRename: atomicRename}, nil
			}, "/storage", 100)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = adapter.Close() })
			if got := adapter.Capabilities().Supports(filesystem.CapabilityMove); got != atomicRename {
				t.Fatalf("Supports(move) = %v, want %v", got, atomicRename)
			}
		})
	}
}

type scriptedConnector struct {
	mu       sync.Mutex
	sessions []remoteSession
	calls    int
}

func (c *scriptedConnector) Connect(context.Context) (remoteSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if len(c.sessions) == 0 {
		return nil, errors.New("no scripted session")
	}
	session := c.sessions[0]
	c.sessions = c.sessions[1:]
	return session, nil
}

func (c *scriptedConnector) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type fakeState struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeState() *fakeState {
	return &fakeState{objects: make(map[string][]byte)}
}

type fakeSession struct {
	state            *fakeState
	atomicRename     bool
	statError        error
	writeError       error
	openError        error
	openedFile       remoteFile
	openFileError    error
	createdFile      *fakeFile
	lstatError       error
	lstatInfo        fs.FileInfo
	readDirError     error
	removeError      error
	mkdirError       error
	renameError      error
	posixRenameError error
	closeError       error
	closed           bool
}

func (s *fakeSession) Open(name string) (remoteFile, error) {
	if s.openError != nil {
		return nil, s.openError
	}
	if s.openedFile != nil {
		return s.openedFile, nil
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	content, found := s.state.objects[name]
	if !found {
		return nil, fs.ErrNotExist
	}
	return &fakeFile{reader: bytes.NewReader(append([]byte(nil), content...)), info: fakeInfo{name: path.Base(name), size: int64(len(content))}}, nil
}

func (s *fakeSession) OpenFile(name string, _ int) (remoteFile, error) {
	if s.openFileError != nil {
		return nil, s.openFileError
	}
	if s.createdFile != nil {
		s.createdFile.state = s.state
		s.createdFile.name = name
		return s.createdFile, nil
	}
	return &fakeFile{
		state:      s.state,
		name:       name,
		writeError: s.writeError,
		info:       fakeInfo{name: path.Base(name)},
	}, nil
}

func (s *fakeSession) Stat(name string) (fs.FileInfo, error) {
	if s.statError != nil {
		err := s.statError
		s.statError = nil
		return nil, err
	}
	return s.stat(name)
}

func (s *fakeSession) Lstat(name string) (fs.FileInfo, error) {
	if s.lstatError != nil {
		return nil, s.lstatError
	}
	if s.lstatInfo != nil {
		return s.lstatInfo, nil
	}
	return s.stat(name)
}

func (s *fakeSession) stat(name string) (fs.FileInfo, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if content, found := s.state.objects[name]; found {
		return fakeInfo{name: path.Base(name), size: int64(len(content))}, nil
	}
	prefix := strings.TrimSuffix(name, "/") + "/"
	for candidate := range s.state.objects {
		if strings.HasPrefix(candidate, prefix) {
			return fakeInfo{name: path.Base(name), directory: true}, nil
		}
	}
	return nil, fs.ErrNotExist
}

func (s *fakeSession) ReadDirContext(ctx context.Context, directory string) ([]fs.FileInfo, error) {
	if s.readDirError != nil {
		return nil, s.readDirError
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	prefix := strings.TrimSuffix(directory, "/") + "/"
	entries := make(map[string]fakeInfo)
	for candidate, content := range s.state.objects {
		if !strings.HasPrefix(candidate, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(candidate, prefix)
		parts := strings.SplitN(remainder, "/", 2)
		if len(parts) == 1 {
			entries[parts[0]] = fakeInfo{name: parts[0], size: int64(len(content))}
		} else {
			entries[parts[0]] = fakeInfo{name: parts[0], directory: true}
		}
	}
	result := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name() < result[right].Name() })
	return result, nil
}

func (s *fakeSession) Remove(name string) error {
	if s.removeError != nil {
		return s.removeError
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if _, found := s.state.objects[name]; !found {
		return fs.ErrNotExist
	}
	delete(s.state.objects, name)
	return nil
}

func (s *fakeSession) MkdirAll(string) error { return s.mkdirError }

func (s *fakeSession) Rename(source, destination string) error {
	if s.renameError != nil {
		return s.renameError
	}
	return s.rename(source, destination)
}

func (s *fakeSession) PosixRename(source, destination string) error {
	if s.posixRenameError != nil {
		return s.posixRenameError
	}
	if !s.atomicRename {
		return errors.New("posix rename unsupported")
	}
	return s.rename(source, destination)
}

func (s *fakeSession) rename(source, destination string) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	content, found := s.state.objects[source]
	if !found {
		return fs.ErrNotExist
	}
	s.state.objects[destination] = content
	delete(s.state.objects, source)
	return nil
}

func (s *fakeSession) HasExtension(name string) (string, bool) {
	return "1", name == "posix-rename@openssh.com" && s.atomicRename
}

func (s *fakeSession) Close() error {
	s.closed = true
	return s.closeError
}

type fakeFile struct {
	state       *fakeState
	name        string
	buffer      bytes.Buffer
	reader      *bytes.Reader
	info        fakeInfo
	writeError  error
	readError   error
	seekError   error
	statError   error
	syncError   error
	closeError  error
	closeNotify chan struct{}
	closed      bool
}

func (f *fakeFile) Read(buffer []byte) (int, error) {
	if f.readError != nil {
		return 0, f.readError
	}
	if f.reader == nil {
		return 0, io.EOF
	}
	return f.reader.Read(buffer)
}

func (f *fakeFile) Write(buffer []byte) (int, error) {
	if f.writeError != nil {
		return 0, f.writeError
	}
	return f.buffer.Write(buffer)
}

func (f *fakeFile) Seek(offset int64, whence int) (int64, error) {
	if f.seekError != nil {
		return 0, f.seekError
	}
	if f.reader == nil {
		return 0, errors.New("not readable")
	}
	return f.reader.Seek(offset, whence)
}

func (f *fakeFile) Stat() (fs.FileInfo, error) { return f.info, f.statError }

func (f *fakeFile) Sync() error { return f.syncError }

func (f *fakeFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	if f.closeNotify != nil {
		close(f.closeNotify)
	}
	if f.state != nil && f.writeError == nil {
		f.state.mu.Lock()
		f.state.objects[f.name] = append([]byte(nil), f.buffer.Bytes()...)
		f.state.mu.Unlock()
	}
	return f.closeError
}

type fakeInfo struct {
	name      string
	size      int64
	directory bool
	mode      fs.FileMode
}

func (i fakeInfo) Name() string { return i.name }
func (i fakeInfo) Size() int64  { return i.size }
func (i fakeInfo) Mode() fs.FileMode {
	if i.mode != 0 {
		return i.mode
	}
	if i.directory {
		return fs.ModeDir | 0o755
	}
	return 0o600
}
func (i fakeInfo) ModTime() time.Time { return time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC) }
func (i fakeInfo) IsDir() bool        { return i.directory }
func (i fakeInfo) Sys() any           { return nil }
