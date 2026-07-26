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

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

var errInjected = errors.New("injected operating system failure")

type fakeSystem struct {
	mkdirErr error
	root     rootFS
	openErr  error
}

func (s fakeSystem) MkdirAll(string, fs.FileMode) error { return s.mkdirErr }
func (s fakeSystem) OpenRoot(string) (rootFS, error)    { return s.root, s.openErr }

type fakeRoot struct {
	openFile  localFile
	openErr   error
	create    localFile
	createErr error
	statInfo  fs.FileInfo
	statErr   error
	lstatInfo fs.FileInfo
	lstatErr  error
	mkdirErr  error
	removeErr error
	renameErr error
	fsys      fs.FS
	closeErr  error
}

func (r *fakeRoot) Open(string) (localFile, error) { return r.openFile, r.openErr }
func (r *fakeRoot) OpenFile(string, int, fs.FileMode) (localFile, error) {
	return r.create, r.createErr
}
func (r *fakeRoot) Stat(string) (fs.FileInfo, error)   { return r.statInfo, r.statErr }
func (r *fakeRoot) Lstat(string) (fs.FileInfo, error)  { return r.lstatInfo, r.lstatErr }
func (r *fakeRoot) MkdirAll(string, fs.FileMode) error { return r.mkdirErr }
func (r *fakeRoot) Remove(string) error                { return r.removeErr }
func (r *fakeRoot) Rename(string, string) error        { return r.renameErr }
func (r *fakeRoot) FS() fs.FS                          { return r.fsys }
func (r *fakeRoot) Close() error                       { return r.closeErr }

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
	if adapter, err := newAdapter("root", fakeSystem{mkdirErr: errInjected}); err == nil {
		_ = adapter.Close()
		t.Fatal("newAdapter(mkdir) error = nil")
	}
	if adapter, err := newAdapter("root", fakeSystem{openErr: errInjected}); err == nil {
		_ = adapter.Close()
		t.Fatal("newAdapter(open) error = nil")
	}
	if _, err := (osSystem{}).OpenRoot("bad\x00root"); err == nil {
		t.Fatal("osSystem.OpenRoot() error = nil")
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
			if test.name == "precondition stat" {
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
