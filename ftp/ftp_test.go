package ftp

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/fstest"
	protocolserver "github.com/gonzalop/ftp/server"
)

func TestConcreteClientIntegration(t *testing.T) {
	root := t.TempDir()
	driver, err := protocolserver.NewFSDriver(root,
		protocolserver.WithAuthenticator(func(user, password, _ string, _ net.IP) (string, bool, error) {
			if user != "user" || password != "password" {
				return "", false, os.ErrPermission
			}
			return root, false, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := protocolserver.NewServer(listener.Addr().String(), protocolserver.WithDriver(driver))
	if err != nil {
		t.Fatal(err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("server Shutdown() error = %v", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, protocolserver.ErrServerClosed) {
			t.Errorf("server Serve() error = %v", err)
		}
	})

	adapter, err := New(context.Background(), Config{
		Address:        listener.Addr().String(),
		Username:       "user",
		Password:       "password",
		TLSMode:        TLSPlaintext,
		AllowPlaintext: true,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := adapter.Close(); err != nil {
			t.Errorf("adapter Close() error = %v", err)
		}
	})

	logicalPath := filesystem.MustParsePath("nested/file.txt")
	if _, err := adapter.Write(context.Background(), logicalPath, strings.NewReader("content"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Open(context.Background(), logicalPath)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read error = %v, close error = %v", readErr, closeErr)
	}
	if string(content) != "content" {
		t.Fatalf("Open() content = %q", content)
	}
	metadata, err := adapter.Stat(context.Background(), logicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Size != int64(len("content")) {
		t.Fatalf("Stat().Size = %d", metadata.Size)
	}
	if _, err := os.Stat(filepath.Join(root, "nested", "file.txt")); err != nil {
		t.Fatal(err)
	}
	if !adapter.Profile().MachineListings {
		t.Fatal("Profile().MachineListings = false")
	}
	iterator, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	for iterator.Next() {
	}
	if err := iterator.Err(); err != nil {
		t.Fatal(err)
	}
	if err := iterator.Close(); err != nil {
		t.Fatal(err)
	}
	real := adapter.session.(*realSession)
	real.machineListings = false
	if _, err := adapter.Stat(context.Background(), logicalPath); err != nil {
		t.Fatal(err)
	}
	iterator, err = adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	for iterator.Next() {
	}
	if err := iterator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Delete(context.Background(), logicalPath); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), Config{
		Address:        listener.Addr().String(),
		Username:       "user",
		Password:       "wrong",
		TLSMode:        TLSPlaintext,
		AllowPlaintext: true,
		Timeout:        5 * time.Second,
	}); err == nil {
		t.Fatal("New(wrong password) error = nil")
	}
	if err := real.client.Quit(); err != nil {
		t.Fatal(err)
	}
	if _, err := real.List("/"); err == nil {
		t.Fatal("realSession.List() error = nil after client close")
	}
	adapter.mu.Lock()
	adapter.session = nil
	adapter.mu.Unlock()
}

func TestConformance(t *testing.T) {
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		state := newFakeState()
		adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
			return &fakeSession{state: state}, nil
		}, "/storage", 100, Profile{TLSMode: TLSExplicit, DataMode: Passive})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = adapter.Close() })
		return adapter
	})
}

func TestNewRejectsUnsafeOrIncompleteConfiguration(t *testing.T) {
	t.Parallel()

	secureTLS := &tls.Config{ServerName: "ftp.example.test", MinVersion: tls.VersionTLS12}
	valid := Config{
		Address:   "ftp.example.test:21",
		Username:  "user",
		Password:  "super-secret-credential",
		TLSMode:   TLSExplicit,
		TLSConfig: secureTLS,
	}
	tests := map[string]func(*Config){
		"missing address":  func(config *Config) { config.Address = "" },
		"missing username": func(config *Config) { config.Username = "" },
		"missing password": func(config *Config) { config.Password = "" },
		"missing TLS":      func(config *Config) { config.TLSConfig = nil },
		"insecure TLS": func(config *Config) {
			config.TLSConfig = &tls.Config{InsecureSkipVerify: true}
		},
		"plaintext without opt in": func(config *Config) {
			config.TLSMode = TLSPlaintext
			config.TLSConfig = nil
		},
		"invalid root": func(config *Config) {
			configurePlaintext(config)
			config.Root = "../escape"
		},
		"invalid TLS mode":  func(config *Config) { config.TLSMode = TLSMode(99) },
		"invalid data mode": func(config *Config) { config.DataMode = DataMode(99) },
		"negative timeout": func(config *Config) {
			configurePlaintext(config)
			config.Timeout = -time.Second
		},
		"negative list bound": func(config *Config) {
			configurePlaintext(config)
			config.MaxListEntries = -1
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			configuration := valid
			mutate(&configuration)
			_, err := New(context.Background(), configuration)
			if err == nil {
				t.Fatal("New() error = nil")
			}
			if strings.Contains(err.Error(), valid.Password) {
				t.Fatalf("New() error leaked password: %v", err)
			}
		})
	}
}

func configurePlaintext(configuration *Config) {
	configuration.TLSMode = TLSPlaintext
	configuration.TLSConfig = nil
	configuration.AllowPlaintext = true
}

func TestPlaintextRequiresExplicitOptIn(t *testing.T) {
	t.Parallel()

	configuration := Config{
		Address:        "127.0.0.1:1",
		Username:       "user",
		Password:       "password",
		TLSMode:        TLSPlaintext,
		AllowPlaintext: true,
		Timeout:        time.Millisecond,
	}
	_, err := New(context.Background(), configuration)
	if err == nil {
		t.Fatal("New() unexpectedly connected")
	}
	if strings.Contains(err.Error(), configuration.Password) {
		t.Fatalf("New() error leaked password: %v", err)
	}
}

func TestProfileExposesTLSAndDataModes(t *testing.T) {
	t.Parallel()

	profile := Profile{TLSMode: TLSImplicit, DataMode: Active, MachineListings: true}
	adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
		return &fakeSession{state: newFakeState()}, nil
	}, "/storage", 100, profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	if adapter.Profile() != profile {
		t.Fatalf("Profile() = %+v, want %+v", adapter.Profile(), profile)
	}
}

func TestStatReconnectsOnceAfterConnectionLoss(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	state.objects["/storage/file.txt"] = []byte("content")
	connector := &scriptedConnector{sessions: []remoteSession{
		&fakeSession{state: state, statError: io.EOF},
		&fakeSession{state: state},
	}}
	adapter, err := newAdapter(context.Background(), connector.Connect, "/storage", 100, Profile{})
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

func TestOpenReconnectsWhenPreflightLosesConnection(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	state.objects["/storage/file.txt"] = []byte("content")
	connector := &scriptedConnector{sessions: []remoteSession{
		&fakeSession{state: state, statError: io.EOF},
		&fakeSession{state: state},
	}}
	adapter, err := newAdapter(context.Background(), connector.Connect, "/storage", 100, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	stream, err := adapter.Open(context.Background(), filesystem.MustParsePath("file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read error = %v, close error = %v", readErr, closeErr)
	}
	if string(content) != "content" {
		t.Fatalf("Open() content = %q", content)
	}
	if connector.Calls() != 2 {
		t.Fatalf("connector calls = %d, want 2", connector.Calls())
	}
}

func TestMachineListingFeatureDetection(t *testing.T) {
	t.Parallel()

	for _, features := range []map[string]string{{"MLST": "type*;size*"}, {"MLSD": ""}} {
		if !supportsMachineListings(features) {
			t.Fatalf("supportsMachineListings(%v) = false", features)
		}
	}
	if supportsMachineListings(map[string]string{"SIZE": ""}) {
		t.Fatal("supportsMachineListings(SIZE) = true")
	}
}

func TestWriteDoesNotReplayConsumedStreamAfterConnectionLoss(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	connector := &scriptedConnector{sessions: []remoteSession{
		&fakeSession{state: state, storeError: io.ErrUnexpectedEOF},
		&fakeSession{state: state},
	}}
	adapter, err := newAdapter(context.Background(), connector.Connect, "/storage", 100, Profile{})
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
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Write() error = %v", err)
	}
	if connector.Calls() != 1 {
		t.Fatalf("connector calls = %d, want 1", connector.Calls())
	}
	if len(state.objects) != 0 {
		t.Fatalf("partial objects = %v", state.objects)
	}
}

func TestCanceledOpenAbortsTransferAndReleasesSession(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	state.objects["/storage/file.txt"] = []byte("content")
	session := newBlockingSession(state)
	adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
		return session, nil
	}, "/storage", 100, Profile{})
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
	if err := stream.Close(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-session.aborted:
	case <-time.After(time.Second):
		t.Fatal("transfer was not aborted")
	}
}

func TestCapabilitiesDoNotClaimInconsistentFTPFeatures(t *testing.T) {
	t.Parallel()

	adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
		return &fakeSession{state: newFakeState()}, nil
	}, "/storage", 100, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	for _, capability := range []filesystem.Capability{
		filesystem.CapabilityRead,
		filesystem.CapabilityWrite,
		filesystem.CapabilityDelete,
		filesystem.CapabilityList,
		filesystem.CapabilityStat,
	} {
		if !adapter.Capabilities().Supports(capability) {
			t.Errorf("Capabilities().Supports(%q) = false", capability)
		}
	}
	for _, capability := range []filesystem.Capability{
		filesystem.CapabilityCopy,
		filesystem.CapabilityMove,
		filesystem.CapabilityRangeRead,
		filesystem.CapabilityMetadata,
		filesystem.CapabilityChecksum,
		filesystem.CapabilityTemporaryURL,
		filesystem.CapabilityVisibility,
	} {
		if adapter.Capabilities().Supports(capability) {
			t.Errorf("Capabilities().Supports(%q) = true", capability)
		}
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
		return nil, errors.New("no scripted FTP session")
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
	state           *fakeState
	statError       error
	storeError      error
	retrieveError   error
	retrieveContent []byte
	listError       error
	makeDirError    error
	deleteError     error
	renameError     error
	abortError      error
	quitError       error
	machineListings bool
	listEntries     []remoteEntry
	statEntry       *remoteEntry
	aborted         chan struct{}
	abortOnce       sync.Once
}

func (s *fakeSession) Retrieve(name string, destination io.Writer) error {
	if s.retrieveError != nil {
		if len(s.retrieveContent) > 0 {
			_, _ = destination.Write(s.retrieveContent)
		}
		return s.retrieveError
	}
	s.state.mu.Lock()
	content, found := s.state.objects[name]
	s.state.mu.Unlock()
	if !found {
		return errFileUnavailable
	}
	_, err := destination.Write(append([]byte(nil), content...))
	return err
}

func (s *fakeSession) Store(name string, source io.Reader) error {
	content, err := io.ReadAll(source)
	if err != nil {
		return err
	}
	if s.storeError != nil {
		return s.storeError
	}
	s.state.mu.Lock()
	s.state.objects[name] = append([]byte(nil), content...)
	s.state.mu.Unlock()
	return nil
}

func (s *fakeSession) Stat(name string) (remoteEntry, error) {
	if s.statError != nil {
		err := s.statError
		s.statError = nil
		return remoteEntry{}, err
	}
	if s.statEntry != nil {
		return *s.statEntry, nil
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if content, found := s.state.objects[name]; found {
		return remoteEntry{Name: path.Base(name), Size: int64(len(content)), Modified: fixedTime()}, nil
	}
	prefix := strings.TrimSuffix(name, "/") + "/"
	for candidate := range s.state.objects {
		if strings.HasPrefix(candidate, prefix) {
			return remoteEntry{Name: path.Base(name), Directory: true, Modified: fixedTime()}, nil
		}
	}
	return remoteEntry{}, errFileUnavailable
}

func (s *fakeSession) List(directory string) ([]remoteEntry, error) {
	if s.listError != nil {
		return nil, s.listError
	}
	if s.listEntries != nil {
		return append([]remoteEntry(nil), s.listEntries...), nil
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	prefix := strings.TrimSuffix(directory, "/") + "/"
	entries := make(map[string]remoteEntry)
	for candidate, content := range s.state.objects {
		if !strings.HasPrefix(candidate, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(candidate, prefix)
		parts := strings.SplitN(remainder, "/", 2)
		if len(parts) == 1 {
			entries[parts[0]] = remoteEntry{Name: parts[0], Size: int64(len(content)), Modified: fixedTime()}
		} else {
			entries[parts[0]] = remoteEntry{Name: parts[0], Directory: true, Modified: fixedTime()}
		}
	}
	result := make([]remoteEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result, nil
}

func (s *fakeSession) MakeDir(string) error { return s.makeDirError }

func (s *fakeSession) Delete(name string) error {
	if s.deleteError != nil {
		return s.deleteError
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if _, found := s.state.objects[name]; !found {
		return errFileUnavailable
	}
	delete(s.state.objects, name)
	return nil
}

func (s *fakeSession) Rename(source, destination string) error {
	if s.renameError != nil {
		return s.renameError
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	content, found := s.state.objects[source]
	if !found {
		return errFileUnavailable
	}
	s.state.objects[destination] = content
	delete(s.state.objects, source)
	return nil
}

func (s *fakeSession) Abort() error {
	s.abortOnce.Do(func() {
		if s.aborted != nil {
			close(s.aborted)
		}
	})
	return s.abortError
}

func (s *fakeSession) Quit() error { return s.quitError }

func (s *fakeSession) MachineListings() bool { return s.machineListings }

type blockingSession struct {
	*fakeSession
	aborted chan struct{}
}

func newBlockingSession(state *fakeState) *blockingSession {
	aborted := make(chan struct{})
	return &blockingSession{
		fakeSession: &fakeSession{state: state, aborted: aborted},
		aborted:     aborted,
	}
}

func (s *blockingSession) Retrieve(string, io.Writer) error {
	<-s.aborted
	return context.Canceled
}

func fixedTime() time.Time {
	return time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
}
