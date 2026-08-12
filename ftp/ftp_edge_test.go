package ftp

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	protocol "github.com/gonzalop/ftp"
)

var errInjected = errors.New("injected FTP failure")

func TestConstructorOptionAndInternalValidation(t *testing.T) {
	t.Parallel()

	if _, err := New(context.Background(), Config{
		Address:   "example.test:21",
		Username:  "user",
		Password:  "secret",
		TLSMode:   TLSExplicit,
		TLSConfig: &tls.Config{ServerName: "example.test", MinVersion: tls.VersionTLS11},
	}); err == nil {
		t.Fatal("New(old TLS) error = nil")
	}
	if _, err := New(context.Background(), Config{
		Address:   "missing-port",
		Username:  "user",
		Password:  "secret",
		TLSMode:   TLSExplicit,
		TLSConfig: &tls.Config{},
	}); err == nil {
		t.Fatal("New(missing port) error = nil")
	}
	for _, configuration := range []Config{
		{
			Address: "127.0.0.1:1", Username: "user", Password: "secret",
			TLSMode: TLSPlaintext, AllowPlaintext: true, DataMode: Active,
			DisableEPSV: true,
		},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := New(ctx, configuration); !errors.Is(err, context.Canceled) {
			t.Fatalf("New(canceled %+v) error = %v", configuration, err)
		}
	}
	if _, err := newAdapter(context.Background(), nil, "/", 1, Profile{}); err == nil {
		t.Fatal("newAdapter(nil) error = nil")
	}
	connector := func(context.Context) (remoteSession, error) { return nil, errInjected }
	if _, err := newAdapter(context.Background(), connector, "relative", 1, Profile{}); err == nil {
		t.Fatal("newAdapter(relative root) error = nil")
	}
	if _, err := newAdapter(context.Background(), connector, "/", 0, Profile{}); err == nil {
		t.Fatal("newAdapter(limit) error = nil")
	}
	if _, err := newAdapter(context.Background(), connector, "/", 1, Profile{}); !errors.Is(err, errInjected) {
		t.Fatalf("newAdapter(connect) error = %v", err)
	}
	session := &fakeSession{state: newFakeState(), machineListings: true}
	adapter := testFTPAdapter(t, session)
	if !adapter.Profile().MachineListings {
		t.Fatal("newAdapter did not merge machine-listing support")
	}
}

func TestConfigurationBoundaryNormalization(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		version uint16
		invalid bool
	}{
		{name: "unspecified", version: 0},
		{name: "TLS 1.1", version: tls.VersionTLS11, invalid: true},
		{name: "TLS 1.2", version: tls.VersionTLS12},
		{name: "TLS 1.3", version: tls.VersionTLS13},
	} {
		if got := invalidTLSMinimum(test.version); got != test.invalid {
			t.Errorf("invalidTLSMinimum(%s) = %t, want %t", test.name, got, test.invalid)
		}
	}
	if got := defaultTimeout(0); got != 30*time.Second {
		t.Fatalf("defaultTimeout(0) = %s", got)
	}
	if got := defaultTimeout(time.Nanosecond); got != time.Nanosecond {
		t.Fatalf("defaultTimeout(1ns) = %s", got)
	}
	if got := defaultMaxList(0); got != 10_000 {
		t.Fatalf("defaultMaxList(0) = %d", got)
	}
	if got := defaultMaxList(1); got != 1 {
		t.Fatalf("defaultMaxList(1) = %d", got)
	}
	for _, test := range []struct {
		requested int
		want      int
	}{
		{requested: 0, want: 100},
		{requested: 1, want: 1},
		{requested: 100, want: 100},
		{requested: 101, want: 100},
	} {
		if got := effectiveListLimit(test.requested, 100); got != test.want {
			t.Errorf("effectiveListLimit(%d, 100) = %d, want %d", test.requested, got, test.want)
		}
	}

	connector := func(context.Context) (remoteSession, error) { return nil, errInjected }
	for _, limit := range []int{0, -1} {
		if _, err := newAdapter(context.Background(), connector, "/", limit, Profile{}); err == nil || err.Error() != "ftp: maximum list entries must be positive" {
			t.Fatalf("newAdapter(limit %d) error = %v", limit, err)
		}
	}
}

func TestCloseAndSessionStateFailures(t *testing.T) {
	t.Parallel()

	session := &fakeSession{state: newFakeState(), quitError: errInjected}
	adapter := testFTPAdapter(t, session)
	if err := adapter.Close(); !errors.Is(err, errInjected) {
		t.Fatalf("Close() error = %v", err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close(second) = %v", err)
	}
	if err := (&Adapter{}).Close(); err != nil {
		t.Fatalf("Close(nil session) = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Adapter{}).currentSession(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("currentSession(canceled) = %v", err)
	}
	if _, err := (&Adapter{closed: true}).currentSession(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("currentSession(closed) = %v", err)
	}
	if _, err := (&Adapter{connector: func(context.Context) (remoteSession, error) { return nil, errInjected }}).currentSession(context.Background()); !errors.Is(err, errInjected) {
		t.Fatalf("currentSession(connect) = %v", err)
	}
}

func TestOpenPreflightAndTransferFailures(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("object")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := testFTPAdapter(t, &fakeSession{state: newFakeState()}).Open(canceled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open(canceled) = %v", err)
	}
	closed := testFTPAdapter(t, &fakeSession{state: newFakeState()})
	_ = closed.Close()
	if _, err := closed.Open(context.Background(), path); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Open(closed) = %v", err)
	}
	for _, entry := range []remoteEntry{
		{Name: "object", Link: true},
		{Name: "object", Directory: true},
	} {
		adapter := testFTPAdapter(t, &fakeSession{state: newFakeState(), statEntry: &entry})
		if _, err := adapter.Open(context.Background(), path); err == nil {
			t.Fatalf("Open(%+v) error = nil", entry)
		}
	}
	if _, err := testFTPAdapter(t, &fakeSession{state: newFakeState(), statError: errInjected}).Open(context.Background(), path); !errors.Is(err, errInjected) {
		t.Fatalf("Open(preflight) = %v", err)
	}
	preflightConnector := &scriptedConnector{sessions: []remoteSession{
		&fakeSession{state: newFakeState(), statError: io.EOF},
	}}
	preflight, err := newAdapter(context.Background(), preflightConnector.Connect, "/storage", 10, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preflight.Open(context.Background(), path); err == nil || preflightConnector.Calls() != 2 {
		t.Fatalf("Open(reconnect failure) = %v, calls %d", err, preflightConnector.Calls())
	}

	state := newFakeState()
	state.objects["/storage/object"] = []byte("content")
	connector := &scriptedConnector{sessions: []remoteSession{
		&fakeSession{state: state, retrieveError: io.ErrUnexpectedEOF},
		&fakeSession{state: state},
	}}
	adapter, err := newAdapter(context.Background(), connector.Connect, "/storage", 10, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	stream, err := adapter.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || string(content) != "content" || connector.Calls() != 2 {
		t.Fatalf("reconnected read = %q, read %v close %v calls %d", content, readErr, closeErr, connector.Calls())
	}

	partial := testFTPAdapter(t, &fakeSession{
		state:           state,
		retrieveContent: []byte("part"),
		retrieveError:   io.ErrUnexpectedEOF,
	})
	stream, err = partial.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr = io.ReadAll(stream)
	_ = stream.Close()
	if string(content) != "part" || !errors.Is(readErr, io.ErrUnexpectedEOF) {
		t.Fatalf("partial read = %q, %v", content, readErr)
	}
	failedConnector := &scriptedConnector{sessions: []remoteSession{
		&fakeSession{state: state, retrieveError: io.ErrUnexpectedEOF},
	}}
	failed, err := newAdapter(context.Background(), failedConnector.Connect, "/storage", 10, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	stream, err = failed.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(stream); err == nil || failedConnector.Calls() != 2 {
		t.Fatalf("transfer reconnect failure = %v, calls %d", err, failedConnector.Calls())
	}
	_ = stream.Close()
}

func TestWriteFailureMatrix(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("directory/object")
	if _, err := testFTPAdapter(t, &fakeSession{state: newFakeState()}).Write(context.Background(), filesystem.Root(), strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrInvalidPath) {
		t.Fatalf("Write(root) = %v", err)
	}
	if _, err := testFTPAdapter(t, &fakeSession{state: newFakeState()}).Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{IfNoneMatch: true}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Write(create only) = %v", err)
	}
	closed := testFTPAdapter(t, &fakeSession{state: newFakeState()})
	_ = closed.Close()
	if _, err := closed.Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write(closed) = %v", err)
	}
	existing := remoteEntry{Name: "object"}
	if _, err := testFTPAdapter(t, &fakeSession{state: newFakeState(), statEntry: &existing}).Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Write(existing) = %v", err)
	}
	tests := []struct {
		name    string
		session *fakeSession
		random  io.Reader
	}{
		{name: "link", session: &fakeSession{state: newFakeState(), statEntry: &remoteEntry{Link: true}}},
		{name: "stat", session: &fakeSession{state: newFakeState(), statError: errInjected}},
		{name: "mkdir", session: &fakeSession{state: newFakeState(), makeDirError: errInjected}},
		{name: "temporary", session: &fakeSession{state: newFakeState()}, random: strings.NewReader("")},
		{name: "store", session: &fakeSession{state: newFakeState(), storeError: errInjected}},
		{name: "rename", session: &fakeSession{state: newFakeState(), renameError: errInjected}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := testFTPAdapter(t, test.session)
			if test.random != nil {
				adapter.random = test.random
			}
			if _, err := adapter.Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{}); err == nil {
				t.Fatal("Write() error = nil")
			}
		})
	}
	sequential := &sequentialStatSession{fakeSession: fakeSession{state: newFakeState()}}
	if _, err := testFTPAdapter(t, sequential).Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{}); !errors.Is(err, errInjected) {
		t.Fatalf("Write(destination stat) = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := testFTPAdapter(t, &fakeSession{state: newFakeState()}).Write(ctx, path, cancelAtEOF{cancel: cancel}, filesystem.WriteOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write(cancel at EOF) = %v", err)
	}
}

func TestWriteRenamesExactlyOnceAndReleasesLockOnSessionFailure(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	session := &fakeSession{state: state}
	adapter := testFTPAdapter(t, session)
	logicalPath := filesystem.MustParsePath("directory/object")
	if _, err := adapter.Write(context.Background(), logicalPath, strings.NewReader("content"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if session.renameCalls != 1 || session.deleteCalls != 0 {
		t.Fatalf("rename calls = %d, delete calls = %d", session.renameCalls, session.deleteCalls)
	}

	closed := testFTPAdapter(t, &fakeSession{state: newFakeState()})
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	for range 2 {
		if _, err := closed.Write(ctx, logicalPath, strings.NewReader("content"), filesystem.WriteOptions{}); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Write(closed) error = %v", err)
		}
	}
}

func TestDeleteStatListAndUnsupportedEdges(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("object")
	closed := testFTPAdapter(t, &fakeSession{state: newFakeState()})
	_ = closed.Close()
	if err := closed.Delete(context.Background(), path); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Delete(closed) = %v", err)
	}
	for _, session := range []*fakeSession{
		{state: newFakeState(), statEntry: &remoteEntry{Link: true}},
		{state: newFakeState(), deleteError: errInjected},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if err := testFTPAdapter(t, session).Delete(ctx, path); err == nil {
			cancel()
			t.Fatal("Delete() error = nil")
		}
		cancel()
	}
	linkSession := &fakeSession{state: newFakeState(), statEntry: &remoteEntry{Link: true}}
	if err := testFTPAdapter(t, linkSession).Delete(context.Background(), path); err == nil || linkSession.deleteCalls != 0 {
		t.Fatalf("Delete(link) error = %v, delete calls = %d", err, linkSession.deleteCalls)
	}
	deleteState := newFakeState()
	deleteState.objects["/storage/object"] = []byte("content")
	deleteSession := &fakeSession{state: deleteState}
	if err := testFTPAdapter(t, deleteSession).Delete(context.Background(), path); err != nil || deleteSession.deleteCalls != 1 {
		t.Fatalf("Delete(object) error = %v, delete calls = %d", err, deleteSession.deleteCalls)
	}
	directory := remoteEntry{Name: "directory", Directory: true}
	metadata, err := testFTPAdapter(t, &fakeSession{state: newFakeState(), statEntry: &directory}).Stat(context.Background(), path)
	if err != nil || metadata.Kind != filesystem.EntryKindDirectory {
		t.Fatalf("Stat(directory) = %+v, %v", metadata, err)
	}
	if _, err := testFTPAdapter(t, &fakeSession{state: newFakeState(), statEntry: &remoteEntry{Link: true}}).Stat(context.Background(), path); err == nil {
		t.Fatal("Stat(link) error = nil")
	}
	if _, err := testFTPAdapter(t, &fakeSession{state: newFakeState()}).List(context.Background(), filesystem.Root(), filesystem.ListOptions{Limit: -1}); err == nil {
		t.Fatal("List(negative limit) error = nil")
	}
	for _, session := range []*fakeSession{
		{state: newFakeState(), statEntry: &remoteEntry{Link: true}},
		{state: newFakeState(), listError: errInjected},
		{state: newFakeState(), listEntries: []remoteEntry{{Name: "link", Link: true}}},
		{state: newFakeState(), listEntries: []remoteEntry{{Name: ".."}}},
	} {
		if _, err := testFTPAdapter(t, session).List(context.Background(), filesystem.MustParsePath("directory"), filesystem.ListOptions{}); err == nil {
			t.Fatal("List() error = nil")
		}
	}
	state := newFakeState()
	state.objects["/storage/a"] = []byte("a")
	state.objects["/storage/b"] = []byte("b")
	iterator, err := testFTPAdapter(t, &fakeSession{state: state}).List(context.Background(), filesystem.Root(), filesystem.ListOptions{Recursive: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if entry := iterator.Entry(); !entry.Path.IsRoot() {
		t.Fatalf("Entry(before Next) = %+v", entry)
	}
	if !iterator.Next() || iterator.Next() {
		t.Fatal("limited iterator count is not one")
	}
	if err := iterator.Close(); err != nil || iterator.Next() {
		t.Fatalf("Close() = %v", err)
	}
	unsorted := &fakeSession{state: newFakeState(), listEntries: []remoteEntry{{Name: "z"}, {Name: "a"}, {Name: "m"}}}
	iterator, err = testFTPAdapter(t, unsorted).List(context.Background(), filesystem.Root(), filesystem.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for iterator.Next() {
		listed = append(listed, iterator.Entry().Path.String())
	}
	if strings.Join(listed, ",") != "a,m,z" {
		t.Fatalf("sorted listing = %v", listed)
	}
	adapter := testFTPAdapter(t, &fakeSession{state: newFakeState()})
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
}

func TestLockReconnectDirectoryAndHelperEdges(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("object")
	adapter := &Adapter{connector: func(context.Context) (remoteSession, error) { return nil, errInjected }}
	if _, err := withLockedResult(adapter, context.Background(), true, func(remoteSession) (string, error) { return "", nil }); !errors.Is(err, errInjected) {
		t.Fatalf("withLockedResult(current) = %v", err)
	}
	adapter = testFTPAdapter(t, &fakeSession{state: newFakeState()})
	result, err := withLockedResult(adapter, context.Background(), false, func(remoteSession) (string, error) { return "partial", errInjected })
	if result != "partial" || !errors.Is(err, errInjected) {
		t.Fatalf("withLockedResult(no retry) = %q, %v", result, err)
	}
	first := &fakeSession{state: newFakeState(), statError: io.EOF}
	connector := &scriptedConnector{sessions: []remoteSession{first}}
	adapter, err = newAdapter(context.Background(), connector.Connect, "/storage", 1, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Stat(context.Background(), path); err == nil || connector.Calls() != 2 {
		t.Fatalf("Stat(reconnect) = %v, calls %d", err, connector.Calls())
	}

	session := &fakeSession{state: newFakeState(), makeDirError: errInjected, statEntry: &remoteEntry{Directory: true}}
	adapter = testFTPAdapter(t, session)
	if err := adapter.makeDirectories(session, "/first/second"); err != nil {
		t.Fatalf("makeDirectories(existing) = %v", err)
	}
	session.statEntry = &remoteEntry{Directory: false}
	if err := adapter.makeDirectories(session, "/first"); !errors.Is(err, errInjected) {
		t.Fatalf("makeDirectories(file) = %v", err)
	}
	if err := adapter.makeDirectories(session, "/"); err != nil {
		t.Fatalf("makeDirectories(root) = %v", err)
	}
	canceled, cancelList := context.WithCancel(context.Background())
	cancelList()
	if _, err := adapter.list(canceled, session, filesystem.Root(), false, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("list(canceled) = %v", err)
	}
	if err := adapter.rejectLink(&fakeSession{state: newFakeState(), statEntry: &remoteEntry{Link: true}}, filesystem.MustParsePath("link/file"), true); err == nil {
		t.Fatal("rejectLink accepted a link")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (contextReader{ctx: ctx, reader: strings.NewReader("x")}).Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("contextReader.Read() = %v", err)
	}
	for _, root := range []string{"", "/", "relative", "/parent/../escape", `C:\\root`, "/bad\nroot"} {
		_, err := validateRoot(root)
		if (root == "" || root == "/") != (err == nil) {
			t.Fatalf("validateRoot(%q) = %v", root, err)
		}
	}
	if _, err := temporaryPath("/", strings.NewReader("")); err == nil {
		t.Fatal("temporaryPath() error = nil")
	}
	if !isConnectionError(&protocol.ProtocolError{Code: 421}) || isConnectionError(&protocol.ProtocolError{Code: 550}) {
		t.Fatal("isConnectionError protocol classification is wrong")
	}
	var networkError = &net.OpError{Op: "read", Net: "tcp", Err: errInjected}
	if !isConnectionError(networkError) {
		t.Fatal("isConnectionError network classification is wrong")
	}
	entries := machineListEntries([]*protocol.MLEntry{
		{Name: ".", Type: "cdir"},
		{Name: "..", Type: "pdir"},
		{Name: "directory", Type: "dir"},
		{Name: "link", Type: "link"},
	})
	if len(entries) != 2 || !entries[0].Directory || !entries[1].Link {
		t.Fatalf("machineListEntries() = %+v", entries)
	}
}

func TestRetryDecisionMatrix(t *testing.T) {
	t.Parallel()

	state := newFakeState()
	for _, test := range []struct {
		name       string
		retry      bool
		firstError error
		wantCalls  int
		wantResult string
		wantError  error
	}{
		{name: "success", retry: true, wantCalls: 1, wantResult: "first"},
		{name: "retry disabled", firstError: io.EOF, wantCalls: 1, wantResult: "first", wantError: io.EOF},
		{name: "not connection", retry: true, firstError: errInjected, wantCalls: 1, wantResult: "first", wantError: errInjected},
		{name: "connection retry", retry: true, firstError: io.EOF, wantCalls: 2, wantResult: "second"},
	} {
		t.Run(test.name, func(t *testing.T) {
			connector := &scriptedConnector{sessions: []remoteSession{
				&fakeSession{state: state},
				&fakeSession{state: state},
			}}
			adapter, err := newAdapter(context.Background(), connector.Connect, "/storage", 1, Profile{})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			result, callErr := withLockedResult(adapter, context.Background(), test.retry, func(remoteSession) (string, error) {
				calls++
				if calls == 1 {
					return "first", test.firstError
				}
				return "second", nil
			})
			if result != test.wantResult || !errors.Is(callErr, test.wantError) || calls != test.wantCalls {
				t.Fatalf("result = %q, error = %v, calls = %d", result, callErr, calls)
			}
		})
	}
}

func TestWriterIteratorAndEntryBoundaries(t *testing.T) {
	t.Parallel()

	counting := &countingWriter{writer: io.Discard}
	if written, err := counting.Write(nil); written != 0 || err != nil || counting.wroteAny {
		t.Fatalf("empty write = %d, %v, wroteAny %t", written, err, counting.wroteAny)
	}
	if written, err := counting.Write([]byte("x")); written != 1 || err != nil || !counting.wroteAny {
		t.Fatalf("data write = %d, %v, wroteAny %t", written, err, counting.wroteAny)
	}

	entry := filesystem.Entry{Path: filesystem.MustParsePath("object")}
	iterator := &iterator{entries: []filesystem.Entry{entry}}
	if got := iterator.Entry(); !got.Path.IsRoot() {
		t.Fatalf("Entry(before Next) = %+v", got)
	}
	if !iterator.Next() || iterator.Entry().Path != entry.Path || iterator.Next() {
		t.Fatal("iterator did not expose exactly one entry")
	}
	if got := iterator.Entry(); got.Path != entry.Path {
		t.Fatalf("Entry(after exhaustion) = %+v", got)
	}
	if err := iterator.Close(); err != nil || iterator.Next() {
		t.Fatalf("Close() = %v", err)
	}

	modified := fixedTime()
	for _, test := range []struct {
		kind      string
		directory bool
		link      bool
	}{
		{kind: "file"},
		{kind: "dir", directory: true},
		{kind: "cdir", directory: true},
		{kind: "pdir", directory: true},
		{kind: "link", link: true},
	} {
		got := machineEntry(&protocol.MLEntry{Name: "name", Type: test.kind, Size: 7, ModTime: modified})
		if got.Name != "name" || got.Size != 7 || got.Modified != modified || got.Directory != test.directory || got.Link != test.link {
			t.Errorf("machineEntry(%q) = %+v", test.kind, got)
		}
	}
	for _, test := range []struct {
		kind      string
		directory bool
		link      bool
	}{
		{kind: "file"},
		{kind: "dir", directory: true},
		{kind: "link", link: true},
	} {
		got := listEntry(&protocol.Entry{Name: "name", Type: test.kind, Size: 7}, modified)
		if got.Name != "name" || got.Size != 7 || got.Modified != modified || got.Directory != test.directory || got.Link != test.link {
			t.Errorf("listEntry(%q) = %+v", test.kind, got)
		}
	}
}

func TestRealSessionMachineAndLegacyListingBranches(t *testing.T) {
	t.Parallel()

	modified := fixedTime()
	machine := &stubProtocolClient{
		mlStat: func(string) (*protocol.MLEntry, error) {
			return &protocol.MLEntry{Name: "object", Type: "dir", Size: 7, ModTime: modified}, nil
		},
		mlList: func(string) ([]*protocol.MLEntry, error) {
			return []*protocol.MLEntry{{Name: "object", Type: "file", Size: 7, ModTime: modified}}, nil
		},
	}
	session := &realSession{client: machine, machineListings: true}
	entry, err := session.Stat("/object")
	if err != nil || !entry.Directory || entry.Name != "object" || entry.Size != 7 || entry.Modified != modified {
		t.Fatalf("machine Stat() = %+v, %v", entry, err)
	}
	entries, err := session.List("/")
	if err != nil || len(entries) != 1 || entries[0].Name != "object" || entries[0].Size != 7 {
		t.Fatalf("machine List() = %+v, %v", entries, err)
	}

	legacy := &stubProtocolClient{
		mlStat: func(string) (*protocol.MLEntry, error) { return nil, errInjected },
		mlList: func(string) ([]*protocol.MLEntry, error) { return nil, errInjected },
		list: func(string) ([]*protocol.Entry, error) {
			return []*protocol.Entry{{Name: "object", Type: "link", Size: 9}}, nil
		},
		modTime: func(string) (time.Time, error) { return modified, nil },
	}
	session = &realSession{client: legacy, machineListings: true}
	entry, err = session.Stat("/object")
	if err != nil || !entry.Link || entry.Name != "object" || entry.Size != 9 || entry.Modified != modified {
		t.Fatalf("legacy Stat() = %+v, %v", entry, err)
	}
	entries, err = session.List("/")
	if err != nil || len(entries) != 1 || !entries[0].Link || entries[0].Modified != (time.Time{}) {
		t.Fatalf("legacy List() = %+v, %v", entries, err)
	}

	missing := &stubProtocolClient{list: func(string) ([]*protocol.Entry, error) { return nil, nil }}
	if _, err := (&realSession{client: missing}).Stat("/missing"); !errors.Is(err, errFileUnavailable) {
		t.Fatalf("Stat(missing) error = %v", err)
	}
	failing := &stubProtocolClient{list: func(string) ([]*protocol.Entry, error) { return nil, errInjected }}
	if _, err := (&realSession{client: failing}).Stat("/object"); !errors.Is(err, errInjected) {
		t.Fatalf("Stat(list failure) error = %v", err)
	}
	if _, err := (&realSession{client: failing}).List("/"); !errors.Is(err, errInjected) {
		t.Fatalf("List(failure) error = %v", err)
	}
}

func TestConnectCancellationCleansLateAuthenticatedSession(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	featureRequested := make(chan struct{})
	releaseFeature := make(chan struct{})
	quitReceived := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = io.WriteString(connection, "220 ready\r\n")
		reader := bufio.NewReader(connection)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "USER "):
				_, _ = io.WriteString(connection, "331 password required\r\n")
			case strings.HasPrefix(line, "PASS "):
				_, _ = io.WriteString(connection, "230 logged in\r\n")
			case strings.HasPrefix(line, "FEAT"):
				close(featureRequested)
				<-releaseFeature
				_, _ = io.WriteString(connection, "500 features unavailable\r\n")
			case strings.HasPrefix(line, "QUIT"):
				_, _ = io.WriteString(connection, "221 goodbye\r\n")
				close(quitReceived)
				return
			default:
				_, _ = io.WriteString(connection, "500 unsupported\r\n")
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-featureRequested
		cancel()
	}()
	if _, err := connect(ctx, listener.Addr().String(), "user", "secret", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("connect() error = %v", err)
	}
	close(releaseFeature)
	select {
	case <-quitReceived:
	case <-time.After(time.Second):
		t.Fatal("late authenticated session was not closed")
	}
}

type sequentialStatSession struct {
	fakeSession
	calls int
}

func (s *sequentialStatSession) Stat(string) (remoteEntry, error) {
	s.calls++
	if s.calls == 1 {
		return remoteEntry{}, errFileUnavailable
	}
	return remoteEntry{}, errInjected
}

func testFTPAdapter(t *testing.T, session remoteSession) *Adapter {
	t.Helper()
	adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
		return session, nil
	}, "/storage", 100, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	return adapter
}

type cancelAtEOF struct{ cancel context.CancelFunc }

func (r cancelAtEOF) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.EOF
}

type stubProtocolClient struct {
	protocolClient
	mlStat  func(string) (*protocol.MLEntry, error)
	mlList  func(string) ([]*protocol.MLEntry, error)
	list    func(string) ([]*protocol.Entry, error)
	modTime func(string) (time.Time, error)
}

func (c *stubProtocolClient) MLStat(name string) (*protocol.MLEntry, error) {
	if c.mlStat == nil {
		return nil, errInjected
	}
	return c.mlStat(name)
}

func (c *stubProtocolClient) MLList(name string) ([]*protocol.MLEntry, error) {
	if c.mlList == nil {
		return nil, errInjected
	}
	return c.mlList(name)
}

func (c *stubProtocolClient) List(name string) ([]*protocol.Entry, error) {
	return c.list(name)
}

func (c *stubProtocolClient) ModTime(name string) (time.Time, error) {
	return c.modTime(name)
}
