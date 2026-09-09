package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	iofstest "testing/fstest"
	"time"

	filesystem "github.com/faustbrian/go-filesystem"
)

var errInjected = errors.New("injected operating system failure")

type fakeSystem struct {
	mkdirErr error
	root     rootFS
	openErr  error
}

func (s fakeSystem) MkdirAll(string, fs.FileMode) error { return s.mkdirErr }
func (s fakeSystem) OpenRoot(string) (rootFS, error)    { return s.root, s.openErr }

type trackingSystem struct {
	mkdirCalls int
	openCalls  int
	root       rootFS
	afterMkdir func()
	afterOpen  func()
}

func (s *trackingSystem) MkdirAll(string, fs.FileMode) error {
	s.mkdirCalls++
	if s.afterMkdir != nil {
		s.afterMkdir()
	}
	return nil
}

func (s *trackingSystem) OpenRoot(string) (rootFS, error) {
	s.openCalls++
	if s.afterOpen != nil {
		s.afterOpen()
	}
	return s.root, nil
}

type fakeRoot struct {
	openFile   localFile
	openErr    error
	create     localFile
	createErr  error
	statInfo   fs.FileInfo
	statErr    error
	lstatInfo  fs.FileInfo
	lstatErr   error
	mkdirErr   error
	removeErr  error
	linkErr    error
	renameErr  error
	fsys       fs.FS
	closeErr   error
	closeCalls int
	openFlags  int
	openMode   fs.FileMode
}

func (r *fakeRoot) Open(string) (localFile, error) { return r.openFile, r.openErr }
func (r *fakeRoot) OpenFile(_ string, flags int, mode fs.FileMode) (localFile, error) {
	r.openFlags = flags
	r.openMode = mode
	return r.create, r.createErr
}
func (r *fakeRoot) Stat(string) (fs.FileInfo, error)   { return r.statInfo, r.statErr }
func (r *fakeRoot) Lstat(string) (fs.FileInfo, error)  { return r.lstatInfo, r.lstatErr }
func (r *fakeRoot) MkdirAll(string, fs.FileMode) error { return r.mkdirErr }
func (r *fakeRoot) Remove(string) error                { return r.removeErr }
func (r *fakeRoot) Link(string, string) error          { return r.linkErr }
func (r *fakeRoot) Rename(string, string) error        { return r.renameErr }
func (r *fakeRoot) FS() fs.FS                          { return r.fsys }
func (r *fakeRoot) Close() error {
	r.closeCalls++
	return r.closeErr
}

type fakeFile struct {
	reader   io.Reader
	writeErr error
	statInfo fs.FileInfo
	statErr  error
	seekErr  error
	syncErr  error
	closeErr error
}

func (f *fakeFile) Read(buffer []byte) (int, error) {
	if f.reader == nil {
		return 0, io.EOF
	}
	return f.reader.Read(buffer)
}
func (f *fakeFile) Write(buffer []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(buffer), nil
}
func (f *fakeFile) Seek(int64, int) (int64, error) { return 0, f.seekErr }
func (f *fakeFile) Close() error                   { return f.closeErr }
func (f *fakeFile) Stat() (fs.FileInfo, error)     { return f.statInfo, f.statErr }
func (f *fakeFile) Sync() error                    { return f.syncErr }

type fakeInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }

func fakeAdapter(root *fakeRoot) *Adapter {
	if root.lstatErr == nil && root.lstatInfo == nil {
		root.lstatErr = fs.ErrNotExist
	}
	return &Adapter{
		root:          root,
		random:        bytes.NewReader(make([]byte, 16)),
		fileMode:      0o600,
		directoryMode: 0o700,
	}
}

func TestNewAdapterAndOSSystemFailures(t *testing.T) {
	if adapter, err := openAdapter(context.Background(), "root", fakeSystem{mkdirErr: errInjected}); err == nil {
		_ = adapter.Close()
		t.Fatal("openAdapter(mkdir) error = nil")
	}
	if adapter, err := openAdapter(context.Background(), "root", fakeSystem{openErr: errInjected}); err == nil {
		_ = adapter.Close()
		t.Fatal("openAdapter(open) error = nil")
	}
	if _, err := (osSystem{}).OpenRoot("bad\x00root"); err == nil {
		t.Fatal("osSystem.OpenRoot() error = nil")
	}
}

func TestOpenAdapterValidatesBeforeAcquisition(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cancellationCause := errors.New("caller canceled acquisition")
	canceledWithCause, cancelWithCause := context.WithCancelCause(context.Background())
	cancelWithCause(cancellationCause)

	for _, test := range []struct {
		name            string
		ctx             context.Context
		option          Option
		want            error
		wantOptionCalls int
	}{
		{
			name: "nil context",
			ctx:  nil,
			option: func(*config) error {
				t.Fatal("option called for nil context")
				return nil
			},
			want: filesystem.ErrContextRequired,
		},
		{
			name: "pre-canceled context",
			ctx:  canceled,
			option: func(*config) error {
				t.Fatal("option called for pre-canceled context")
				return nil
			},
			want: context.Canceled,
		},
		{
			name: "pre-canceled context with cause",
			ctx:  canceledWithCause,
			option: func(*config) error {
				t.Fatal("option called for pre-canceled context")
				return nil
			},
			want: cancellationCause,
		},
		{
			name: "invalid option",
			ctx:  context.Background(),
			option: func(*config) error {
				return errInjected
			},
			want:            errInjected,
			wantOptionCalls: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			option := func(configuration *config) error {
				calls++
				return test.option(configuration)
			}
			system := &trackingSystem{root: &fakeRoot{}}
			adapter, err := openAdapter(test.ctx, "root", system, option)
			if adapter != nil {
				_ = adapter.Close()
				t.Fatal("openAdapter() adapter != nil")
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("openAdapter() error = %v, want %v", err, test.want)
			}
			if calls != test.wantOptionCalls {
				t.Fatalf("option calls = %d, want %d", calls, test.wantOptionCalls)
			}
			if system.mkdirCalls != 0 || system.openCalls != 0 {
				t.Fatalf("system calls = MkdirAll %d, OpenRoot %d; want zero", system.mkdirCalls, system.openCalls)
			}
		})
	}
}

func TestOpenAdapterAcquiresOneCallerOwnedRoot(t *testing.T) {
	root := &fakeRoot{}
	system := &trackingSystem{root: root}
	adapter, err := openAdapter(
		context.Background(),
		"root",
		system,
		WithFileMode(0o640),
		WithDirectoryMode(0o750),
		WithSymlinkPolicy(AllowInternalSymlinks),
	)
	if err != nil {
		t.Fatal(err)
	}
	if system.mkdirCalls != 1 || system.openCalls != 1 {
		t.Fatalf("system calls = MkdirAll %d, OpenRoot %d; want one each", system.mkdirCalls, system.openCalls)
	}
	if adapter.fileMode != 0o640 || adapter.directoryMode != 0o750 || adapter.symlinkPolicy != AllowInternalSymlinks {
		t.Fatalf("adapter configuration = file %o, directory %o, symlinks %d", adapter.fileMode, adapter.directoryMode, adapter.symlinkPolicy)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if root.closeCalls != 1 {
		t.Fatalf("root Close calls = %d, want 1", root.closeCalls)
	}
}

func TestOpenAdapterStopsWhenContextBecomesUnavailableDuringAcquisition(t *testing.T) {
	cancellationCause := errors.New("caller canceled acquisition")
	tests := []struct {
		name           string
		configure      func(context.CancelCauseFunc, *trackingSystem) Option
		closeErr       error
		wantMkdirCalls int
		wantOpenCalls  int
		wantCloseCalls int
	}{
		{
			name: "option",
			configure: func(cancel context.CancelCauseFunc, _ *trackingSystem) Option {
				return func(*config) error {
					cancel(cancellationCause)
					return nil
				}
			},
		},
		{
			name: "root creation",
			configure: func(cancel context.CancelCauseFunc, system *trackingSystem) Option {
				system.afterMkdir = func() { cancel(cancellationCause) }
				return func(*config) error { return nil }
			},
			wantMkdirCalls: 1,
		},
		{
			name: "root open",
			configure: func(cancel context.CancelCauseFunc, system *trackingSystem) Option {
				system.afterOpen = func() { cancel(cancellationCause) }
				return func(*config) error { return nil }
			},
			wantMkdirCalls: 1,
			wantOpenCalls:  1,
			wantCloseCalls: 1,
		},
		{
			name: "root open close failure",
			configure: func(cancel context.CancelCauseFunc, system *trackingSystem) Option {
				system.afterOpen = func() { cancel(cancellationCause) }
				return func(*config) error { return nil }
			},
			closeErr:       errInjected,
			wantMkdirCalls: 1,
			wantOpenCalls:  1,
			wantCloseCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			root := &fakeRoot{closeErr: test.closeErr}
			system := &trackingSystem{root: root}
			option := test.configure(cancel, system)

			adapter, err := openAdapter(ctx, "root", system, option)
			if adapter != nil {
				_ = adapter.Close()
				t.Fatal("openAdapter() adapter != nil")
			}
			if !errors.Is(err, cancellationCause) {
				t.Fatalf("openAdapter() error = %v, want cancellation cause", err)
			}
			if test.closeErr != nil && !errors.Is(err, test.closeErr) {
				t.Fatalf("openAdapter() error = %v, want close error", err)
			}
			if system.mkdirCalls != test.wantMkdirCalls || system.openCalls != test.wantOpenCalls {
				t.Fatalf(
					"system calls = MkdirAll %d, OpenRoot %d; want %d, %d",
					system.mkdirCalls,
					system.openCalls,
					test.wantMkdirCalls,
					test.wantOpenCalls,
				)
			}
			if root.closeCalls != test.wantCloseCalls {
				t.Fatalf("root Close calls = %d, want %d", root.closeCalls, test.wantCloseCalls)
			}
		})
	}
}

func TestOpenRangeInjectedFileFailures(t *testing.T) {
	path := filesystem.MustParsePath("object")
	for _, test := range []struct {
		name string
		file *fakeFile
	}{
		{name: "stat", file: &fakeFile{statErr: errInjected}},
		{name: "seek", file: &fakeFile{statInfo: fakeInfo{size: 10}, seekErr: errInjected}},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := fakeAdapter(&fakeRoot{openFile: test.file})
			if _, err := adapter.OpenRange(context.Background(), path, filesystem.ByteRange{Length: 1}); !errors.Is(err, errInjected) {
				t.Fatalf("OpenRange() error = %v", err)
			}
		})
	}
}

func TestWriteInjectedPublicationFailures(t *testing.T) {
	path := filesystem.MustParsePath("directory/object")
	tests := []struct {
		name   string
		root   *fakeRoot
		random io.Reader
	}{
		{name: "precondition stat", root: &fakeRoot{statErr: errInjected}},
		{name: "mkdir", root: &fakeRoot{mkdirErr: errInjected}},
		{name: "temporary name", root: &fakeRoot{}, random: strings.NewReader("")},
		{name: "create", root: &fakeRoot{createErr: errInjected}},
		{name: "write", root: &fakeRoot{create: &fakeFile{writeErr: errInjected}}},
		{name: "sync", root: &fakeRoot{create: &fakeFile{syncErr: errInjected}}},
		{name: "close", root: &fakeRoot{create: &fakeFile{closeErr: errInjected}}},
		{name: "link", root: &fakeRoot{create: &fakeFile{}, statErr: fs.ErrNotExist, linkErr: errInjected}},
		{name: "remove published temporary", root: &fakeRoot{create: &fakeFile{}, statErr: fs.ErrNotExist, removeErr: errInjected}},
		{name: "rename", root: &fakeRoot{create: &fakeFile{}, renameErr: errInjected}},
		{name: "final stat", root: &fakeRoot{create: &fakeFile{}, statErr: errInjected}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := fakeAdapter(test.root)
			if test.random != nil {
				adapter.random = test.random
			}
			options := filesystem.WriteOptions{}
			switch test.name {
			case "precondition stat", "link", "remove published temporary":
				options.IfNoneMatch = true
			}
			_, err := adapter.Write(context.Background(), path, strings.NewReader("content"), options)
			if !errors.Is(err, errInjected) && test.name != "temporary name" {
				t.Fatalf("Write() error = %v", err)
			}
			if test.name == "temporary name" && err == nil {
				t.Fatal("Write() error = nil")
			}
		})
	}
}

func TestCopyAndMoveInjectedFailures(t *testing.T) {
	source := filesystem.MustParsePath("source")
	destination := filesystem.MustParsePath("directory/destination")
	if err := fakeAdapter(&fakeRoot{statErr: errInjected}).Copy(context.Background(), source, destination, filesystem.CopyOptions{}); !errors.Is(err, errInjected) {
		t.Fatalf("Copy() error = %v", err)
	}
	for _, root := range []*fakeRoot{
		{statErr: errInjected},
		{statErr: fs.ErrNotExist, mkdirErr: errInjected},
		{statErr: fs.ErrNotExist, renameErr: errInjected},
	} {
		if err := fakeAdapter(root).Move(context.Background(), source, destination, filesystem.MoveOptions{}); !errors.Is(err, errInjected) {
			t.Fatalf("Move() error = %v", err)
		}
	}
}

func TestChecksumPropagatesMidStreamFailures(t *testing.T) {
	path := filesystem.MustParsePath("object")
	for _, algorithm := range []filesystem.ChecksumAlgorithm{
		filesystem.ChecksumMD5,
		filesystem.ChecksumSHA256,
		filesystem.ChecksumCRC32C,
	} {
		adapter := fakeAdapter(&fakeRoot{openFile: &fakeFile{reader: errorOnlyReader{}}})
		if _, err := adapter.Checksum(context.Background(), path, algorithm); !errors.Is(err, errInjected) {
			t.Fatalf("Checksum(%q) error = %v", algorithm, err)
		}
	}
}

type errorOnlyReader struct{}

func (errorOnlyReader) Read([]byte) (int, error) { return 0, errInjected }

type cancelFS struct {
	cancel context.CancelFunc
	files  iofstest.MapFS
}

func (f cancelFS) Open(name string) (fs.File, error) {
	f.cancel()
	return f.files.Open(name)
}

func TestListPropagatesWalkAndMidWalkCancellation(t *testing.T) {
	adapter := fakeAdapter(&fakeRoot{fsys: errorOpenFS{}})
	if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{}); !errors.Is(err, errInjected) {
		t.Fatalf("List(walk error) = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	adapter = fakeAdapter(&fakeRoot{fsys: cancelFS{
		cancel: cancel,
		files:  iofstest.MapFS{"file": &iofstest.MapFile{Data: []byte("x")}},
	}})
	if _, err := adapter.List(ctx, filesystem.Root(), filesystem.ListOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("List(canceled walk) = %v", err)
	}
}

func TestListRejectsEntryInfoAndInvalidLogicalNames(t *testing.T) {
	for _, entry := range []fs.DirEntry{
		faultDirEntry{name: "file", infoErr: errInjected},
		faultDirEntry{name: "bad\nname", info: fakeInfo{name: "bad\nname"}},
	} {
		adapter := fakeAdapter(&fakeRoot{fsys: faultWalkFS{entry: entry}})
		if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{}); err == nil {
			t.Fatalf("List(%q) error = nil", entry.Name())
		}
	}
}

func TestContextReaderStopsBeforeSourceAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := contextReader{ctx: ctx, reader: strings.NewReader("content")}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v", err)
	}
}

func TestTemporaryNameAndIteratorBoundaries(t *testing.T) {
	t.Parallel()

	adapter := fakeAdapter(&fakeRoot{})
	if got, err := adapter.temporaryName("."); err != nil || !strings.HasPrefix(got, ".filesystem-") || strings.Contains(got, "/") {
		t.Fatalf("temporaryName(.) = %q, %v", got, err)
	}
	adapter.random = bytes.NewReader(make([]byte, 16))
	if got, err := adapter.temporaryName("directory"); err != nil || !strings.HasPrefix(got, "directory/.filesystem-") {
		t.Fatalf("temporaryName(directory) = %q, %v", got, err)
	}

	entry := filesystem.Entry{Path: filesystem.MustParsePath("object")}
	iterator := &iterator{entries: []filesystem.Entry{entry}}
	if got := iterator.Entry(); !got.Path.IsRoot() {
		t.Fatalf("Entry(before Next) = %+v", got)
	}
	if !iterator.Next() || iterator.Entry().Path != entry.Path || iterator.Next() {
		t.Fatal("iterator did not expose exactly one entry")
	}
	if iterator.Entry().Path != entry.Path {
		t.Fatal("iterator lost the current entry after exhaustion")
	}
	if err := iterator.Close(); err != nil || iterator.Next() {
		t.Fatalf("Close() = %v", err)
	}
}

type faultWalkFS struct{ entry fs.DirEntry }

func (f faultWalkFS) Open(string) (fs.File, error) { return nil, errInjected }
func (f faultWalkFS) Stat(name string) (fs.FileInfo, error) {
	return fakeInfo{name: name, mode: fs.ModeDir}, nil
}
func (f faultWalkFS) ReadDir(string) ([]fs.DirEntry, error) {
	return []fs.DirEntry{f.entry}, nil
}

type faultDirEntry struct {
	name    string
	info    fs.FileInfo
	infoErr error
}

func (e faultDirEntry) Name() string      { return e.name }
func (e faultDirEntry) IsDir() bool       { return false }
func (e faultDirEntry) Type() fs.FileMode { return 0 }
func (e faultDirEntry) Info() (fs.FileInfo, error) {
	return e.info, e.infoErr
}

type errorOpenFS struct{}

func (errorOpenFS) Open(string) (fs.File, error) { return nil, errInjected }
