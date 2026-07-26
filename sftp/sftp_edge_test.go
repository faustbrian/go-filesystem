package sftp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

var errInjected = errors.New("injected SFTP failure")

func TestConfigurationAndInternalConstructorValidation(t *testing.T) {
	t.Parallel()

	base := Config{
		Address:         "127.0.0.1:1",
		User:            "user",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	for _, mutate := range []func(*Config){
		func(config *Config) { config.Root = "relative" },
		func(config *Config) { config.Timeout = -time.Second },
		func(config *Config) { config.MaxListEntries = -1 },
	} {
		configuration := base
		mutate(&configuration)
		if _, err := New(context.Background(), configuration); err == nil {
			t.Fatal("New() accepted invalid configuration")
		}
	}
	if _, err := New(context.Background(), base); err == nil {
		t.Fatal("New() did not report a dial failure")
	}
	if _, err := newAdapter(context.Background(), nil, "/", 1); err == nil {
		t.Fatal("newAdapter(nil connector) error = nil")
	}
	connector := func(context.Context) (remoteSession, error) { return nil, errInjected }
	if _, err := newAdapter(context.Background(), connector, "relative", 1); err == nil {
		t.Fatal("newAdapter(relative root) error = nil")
	}
	if _, err := newAdapter(context.Background(), connector, "/", 0); err == nil {
		t.Fatal("newAdapter(invalid maximum) error = nil")
	}
	if _, err := newAdapter(context.Background(), connector, "/", 1); !errors.Is(err, errInjected) {
		t.Fatalf("newAdapter(connect) error = %v", err)
	}
}

func TestCloseIsIdempotentAndPropagatesSessionFailure(t *testing.T) {
	t.Parallel()

	session := &fakeSession{state: newFakeState(), closeError: errInjected}
	adapter := testAdapter(t, session)
	if err := adapter.Close(); !errors.Is(err, errInjected) {
		t.Fatalf("Close() error = %v", err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}
	withoutSession := &Adapter{}
	if err := withoutSession.Close(); err != nil {
		t.Fatalf("Close(nil session) error = %v", err)
	}
}

func TestOpenAndRangeFailureMatrix(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("object")
	for _, byteRange := range []filesystem.ByteRange{
		{Offset: -1, Length: 1},
		{Length: 0},
		{Offset: 2, Length: int64(^uint64(0) >> 1)},
	} {
		adapter := testAdapter(t, &fakeSession{state: newFakeState()})
		if _, err := adapter.OpenRange(context.Background(), path, byteRange); !errors.Is(err, filesystem.ErrInvalidRange) {
			t.Fatalf("OpenRange(%+v) error = %v", byteRange, err)
		}
	}
	for _, session := range []*fakeSession{
		{state: newFakeState(), openError: errInjected},
		{state: newFakeState(), openedFile: &fakeFile{seekError: errInjected}},
		{state: newFakeState(), lstatError: errInjected},
		{state: newFakeState(), lstatError: fs.ErrNotExist, openError: errInjected},
	} {
		adapter := testAdapter(t, session)
		if _, err := adapter.OpenRange(context.Background(), path, filesystem.ByteRange{Length: 1}); !errors.Is(err, errInjected) {
			t.Fatalf("OpenRange() error = %v", err)
		}
	}
	adapter := testAdapter(t, &fakeSession{state: newFakeState(), lstatError: errInjected})
	if _, err := adapter.Open(context.Background(), path); !errors.Is(err, errInjected) {
		t.Fatalf("Open(symlink check) error = %v", err)
	}
}

func TestWriteFailureAndPublicationMatrix(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("directory/object")
	if _, err := testAdapter(t, &fakeSession{state: newFakeState()}).Write(context.Background(), filesystem.Root(), strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrInvalidPath) {
		t.Fatalf("Write(root) error = %v", err)
	}
	if _, err := testAdapter(t, &fakeSession{state: newFakeState()}).Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{IfNoneMatch: true}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Write(create only) error = %v", err)
	}
	closed := testAdapter(t, &fakeSession{state: newFakeState()})
	_ = closed.Close()
	if _, err := closed.Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write(closed) error = %v", err)
	}
	if _, err := testAdapter(t, &fakeSession{state: newFakeState(), lstatInfo: fakeInfo{name: "directory", mode: fs.ModeSymlink}}).Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{}); err == nil {
		t.Fatal("Write(symlink) error = nil")
	}
	state := newFakeState()
	state.objects["/storage/directory/object"] = []byte("existing")
	if _, err := testAdapter(t, &fakeSession{state: state}).Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Write(non-atomic replace) error = %v", err)
	}
	tests := []struct {
		name    string
		session *fakeSession
		random  io.Reader
	}{
		{name: "stat", session: &fakeSession{state: newFakeState(), statError: errInjected}},
		{name: "mkdir", session: &fakeSession{state: newFakeState(), mkdirError: errInjected}},
		{name: "temporary", session: &fakeSession{state: newFakeState()}, random: strings.NewReader("")},
		{name: "open", session: &fakeSession{state: newFakeState(), openFileError: errInjected}},
		{name: "copy", session: &fakeSession{state: newFakeState(), createdFile: &fakeFile{writeError: errInjected}}},
		{name: "sync", session: &fakeSession{state: newFakeState(), createdFile: &fakeFile{syncError: errInjected}}},
		{name: "close", session: &fakeSession{state: newFakeState(), createdFile: &fakeFile{closeError: errInjected}}},
		{name: "rename", session: &fakeSession{state: newFakeState(), renameError: errInjected}},
		{name: "posix rename", session: &fakeSession{state: newFakeState(), atomicRename: true, posixRenameError: errInjected}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := testAdapter(t, test.session)
			if test.random != nil {
				adapter.random = test.random
			}
			if _, err := adapter.Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{}); err == nil {
				t.Fatal("Write() error = nil")
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	adapter := testAdapter(t, &fakeSession{state: newFakeState()})
	if _, err := adapter.Write(ctx, path, cancelAtEOFReader{cancel: cancel}, filesystem.WriteOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write(cancel at EOF) error = %v", err)
	}
	unsupportedSync := &fakeFile{syncError: &pkgsftp.StatusError{Code: 8}}
	adapter = testAdapter(t, &fakeSession{state: newFakeState(), atomicRename: true, createdFile: unsupportedSync})
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{}); err != nil {
		t.Fatalf("Write(unsupported sync) error = %v", err)
	}
}

func TestDeleteStatListCopyAndMoveFailures(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("object")
	closed := testAdapter(t, &fakeSession{state: newFakeState()})
	_ = closed.Close()
	if err := closed.Delete(context.Background(), path); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Delete(closed) error = %v", err)
	}
	for _, session := range []*fakeSession{
		{state: newFakeState(), lstatError: errInjected},
		{state: newFakeState(), removeError: errInjected},
	} {
		adapter := testAdapter(t, session)
		if err := adapter.Delete(context.Background(), path); !errors.Is(err, errInjected) {
			t.Fatalf("Delete() error = %v", err)
		}
	}
	directoryState := newFakeState()
	directoryState.objects["/storage/directory/file"] = []byte("x")
	metadata, err := testAdapter(t, &fakeSession{state: directoryState}).Stat(context.Background(), filesystem.MustParsePath("directory"))
	if err != nil || metadata.Kind != filesystem.EntryKindDirectory {
		t.Fatalf("Stat(directory) = %+v, %v", metadata, err)
	}
	if _, err := testAdapter(t, &fakeSession{state: newFakeState(), statError: errInjected}).Stat(context.Background(), path); !errors.Is(err, errInjected) {
		t.Fatalf("Stat(error) = %v", err)
	}
	if _, err := testAdapter(t, &fakeSession{state: newFakeState(), lstatInfo: fakeInfo{name: "object", mode: fs.ModeSymlink}}).Stat(context.Background(), path); err == nil {
		t.Fatal("Stat(symlink) error = nil")
	}
	if _, err := testAdapter(t, &fakeSession{state: newFakeState()}).List(context.Background(), filesystem.Root(), filesystem.ListOptions{Limit: -1}); err == nil {
		t.Fatal("List(negative limit) error = nil")
	}
	for _, session := range []*fakeSession{
		{state: newFakeState(), lstatError: errInjected},
		{state: newFakeState(), readDirError: errInjected},
	} {
		if _, err := testAdapter(t, session).List(context.Background(), filesystem.MustParsePath("directory"), filesystem.ListOptions{}); !errors.Is(err, errInjected) {
			t.Fatalf("List() error = %v", err)
		}
	}
	if err := testAdapter(t, &fakeSession{state: newFakeState()}).Copy(context.Background(), path, filesystem.MustParsePath("copy"), filesystem.CopyOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Copy(no overwrite) = %v", err)
	}
	if err := testAdapter(t, &fakeSession{state: newFakeState(), openError: errInjected}).Copy(context.Background(), path, filesystem.MustParsePath("copy"), filesystem.CopyOptions{Overwrite: true}); !errors.Is(err, errInjected) {
		t.Fatalf("Copy(open failure) = %v", err)
	}
	if err := testAdapter(t, &fakeSession{state: newFakeState()}).Move(context.Background(), path, filesystem.MustParsePath("move"), filesystem.MoveOptions{Overwrite: true}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Move(no extension) = %v", err)
	}
	for _, session := range []*fakeSession{
		{state: newFakeState(), atomicRename: true, lstatError: errInjected},
		{state: newFakeState(), atomicRename: true, mkdirError: errInjected},
		{state: newFakeState(), atomicRename: true, posixRenameError: errInjected},
	} {
		if err := testAdapter(t, session).Move(context.Background(), path, filesystem.MustParsePath("move"), filesystem.MoveOptions{Overwrite: true}); !errors.Is(err, errInjected) {
			t.Fatalf("Move() error = %v", err)
		}
	}
	closed = testAdapter(t, &fakeSession{state: newFakeState(), atomicRename: true})
	_ = closed.Close()
	if err := closed.Move(context.Background(), path, filesystem.MustParsePath("directory/move"), filesystem.MoveOptions{Overwrite: true}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Move(closed) error = %v", err)
	}
	destinationSymlink := &destinationSymlinkSession{fakeSession: fakeSession{state: newFakeState(), atomicRename: true}}
	if err := testAdapter(t, destinationSymlink).Move(context.Background(), path, filesystem.MustParsePath("directory/move"), filesystem.MoveOptions{Overwrite: true}); err == nil {
		t.Fatal("Move(destination symlink) error = nil")
	}
}

func TestListHostileEntriesRecursionAndIteratorEdges(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	state.objects["/storage/directory/nested/file"] = []byte("x")
	adapter := testAdapter(t, &fakeSession{state: state})
	iterator, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{Recursive: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if entry := iterator.Entry(); !entry.Path.IsRoot() {
		t.Fatalf("Entry(before Next) = %+v", entry)
	}
	for iterator.Next() {
	}
	if err := iterator.Close(); err != nil || iterator.Next() {
		t.Fatalf("Close() = %v", err)
	}
	limited, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{Recursive: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = limited.Close() }()
	if !limited.Next() || limited.Next() {
		t.Fatal("limited list did not stop at one entry")
	}

	infos := []fakeInfo{
		fakeInfo{name: "link", mode: fs.ModeSymlink},
		fakeInfo{name: ".."},
	}
	for _, info := range infos {
		session := &listingSession{fakeSession: fakeSession{state: newFakeState()}, infos: []fs.FileInfo{info}}
		if _, err := testAdapter(t, session).List(context.Background(), filesystem.Root(), filesystem.ListOptions{}); err == nil {
			t.Fatalf("List(%q) error = nil", info.Name())
		}
	}
}

type listingSession struct {
	fakeSession
	infos []fs.FileInfo
}

func (s *listingSession) ReadDirContext(context.Context, string) ([]fs.FileInfo, error) {
	return s.infos, nil
}

func TestChecksumsUnsupportedOperationsAndHelpers(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	state.objects["/storage/object"] = []byte("content")
	adapter := testAdapter(t, &fakeSession{state: state})
	path := filesystem.MustParsePath("object")
	for _, algorithm := range []filesystem.ChecksumAlgorithm{filesystem.ChecksumMD5, filesystem.ChecksumSHA256, filesystem.ChecksumCRC32C} {
		checksum, err := adapter.Checksum(context.Background(), path, algorithm)
		if err != nil || checksum.Algorithm != algorithm || checksum.Value == "" {
			t.Fatalf("Checksum(%q) = %+v, %v", algorithm, checksum, err)
		}
	}
	if _, err := testAdapter(t, &fakeSession{state: newFakeState(), openError: errInjected}).Checksum(context.Background(), path, filesystem.ChecksumMD5); !errors.Is(err, errInjected) {
		t.Fatalf("Checksum(open error) = %v", err)
	}
	for _, algorithm := range []filesystem.ChecksumAlgorithm{filesystem.ChecksumMD5, filesystem.ChecksumSHA256, filesystem.ChecksumCRC32C} {
		session := &fakeSession{state: newFakeState(), openedFile: &fakeFile{readError: errInjected}}
		if _, err := testAdapter(t, session).Checksum(context.Background(), path, algorithm); !errors.Is(err, errInjected) {
			t.Fatalf("Checksum(%q read error) = %v", algorithm, err)
		}
	}
	if _, err := adapter.Checksum(context.Background(), path, "sha1"); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Checksum(sha1) = %v", err)
	}
	for _, call := range []func() error{
		func() error {
			_, err := adapter.TemporaryURL(context.Background(), path, time.Minute, filesystem.TemporaryURLOptions{})
			return err
		},
		func() error { return adapter.SetVisibility(context.Background(), path, filesystem.VisibilityPublic) },
	} {
		if err := call(); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
			t.Fatalf("unsupported operation = %v", err)
		}
	}
	for _, root := range []string{"", "/", "/storage/../escape", `C:\\storage`, "/bad\nroot"} {
		validated, err := validateRoot(root)
		if (root == "" || root == "/") != (err == nil) {
			t.Fatalf("validateRoot(%q) = %q, %v", root, validated, err)
		}
	}
	if _, err := temporaryPath("/storage", strings.NewReader("")); err == nil {
		t.Fatal("temporaryPath() error = nil")
	}
	if !isOperationUnsupported(pkgsftp.ErrSSHFxOpUnsupported) || isOperationUnsupported(errInjected) {
		t.Fatal("isOperationUnsupported() classification is wrong")
	}
	if err := mapError(path, nil); err != nil {
		t.Fatalf("mapError(nil) = %v", err)
	}
}

func TestSessionStateAndReconnectFailures(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	adapter := &Adapter{}
	if _, err := adapter.currentSession(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("currentSession(canceled) = %v", err)
	}
	adapter.closed = true
	if _, err := adapter.currentSession(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("currentSession(closed) = %v", err)
	}
	adapter = &Adapter{connector: func(context.Context) (remoteSession, error) { return nil, errInjected }}
	if _, err := adapter.currentSession(context.Background()); !errors.Is(err, errInjected) {
		t.Fatalf("currentSession(connect) = %v", err)
	}
	if _, err := withSession(adapter, context.Background(), true, func(remoteSession) (string, error) { return "", nil }); !errors.Is(err, errInjected) {
		t.Fatalf("withSession(current error) = %v", err)
	}

	first := &fakeSession{state: newFakeState(), statError: pkgsftp.ErrSSHFxConnectionLost}
	connector := &scriptedConnector{sessions: []remoteSession{first}}
	adapter, err := newAdapter(context.Background(), connector.Connect, "/storage", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Stat(context.Background(), filesystem.MustParsePath("object")); err == nil || connector.Calls() != 2 {
		t.Fatalf("Stat(reconnect failure) = %v, calls %d", err, connector.Calls())
	}
	adapter = testAdapter(t, &fakeSession{state: newFakeState()})
	result, err := withSession(adapter, context.Background(), false, func(remoteSession) (string, error) {
		return "partial", errInjected
	})
	if result != "partial" || !errors.Is(err, errInjected) {
		t.Fatalf("withSession(no retry) = %q, %v", result, err)
	}
}

func TestContextReaderAndSymlinkHelperEdges(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := contextReader{ctx: ctx, reader: strings.NewReader("content")}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("contextReader.Read() = %v", err)
	}
	adapter := testAdapter(t, &fakeSession{state: newFakeState()})
	if err := adapter.rejectSymlinks(
		&fakeSession{state: newFakeState(), lstatInfo: fakeInfo{name: "link", mode: fs.ModeSymlink}},
		filesystem.MustParsePath("link/file"),
		true,
	); err == nil {
		t.Fatal("rejectSymlinks() accepted a symbolic link")
	}
}

func TestContextReadCloserClosesFileOnCancellation(t *testing.T) {
	t.Parallel()

	closed := make(chan struct{})
	file := &fakeFile{reader: bytes.NewReader([]byte("content")), closeNotify: closed}
	ctx, cancel := context.WithCancel(context.Background())
	stream := newContextReadCloser(ctx, file)
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled context did not close the remote file")
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

type destinationSymlinkSession struct {
	fakeSession
	calls int
}

func (s *destinationSymlinkSession) Lstat(string) (fs.FileInfo, error) {
	s.calls++
	if s.calls == 1 {
		return fakeInfo{name: "object"}, nil
	}
	return fakeInfo{name: "directory", mode: fs.ModeSymlink}, nil
}

func testAdapter(t *testing.T, session remoteSession) *Adapter {
	t.Helper()
	adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
		return session, nil
	}, "/storage", 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	return adapter
}

type cancelAtEOFReader struct{ cancel context.CancelFunc }

func (r cancelAtEOFReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.EOF
}
