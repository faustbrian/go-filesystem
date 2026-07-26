package filesystem_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

type readerFunc func(context.Context, filesystem.Path) (io.ReadCloser, error)

func (function readerFunc) Open(ctx context.Context, path filesystem.Path) (io.ReadCloser, error) {
	return function(ctx, path)
}

type statterFunc func(context.Context, filesystem.Path) (filesystem.Metadata, error)

func (function statterFunc) Stat(ctx context.Context, path filesystem.Path) (filesystem.Metadata, error) {
	return function(ctx, path)
}

type listerFunc func(context.Context, filesystem.Path, filesystem.ListOptions) (filesystem.EntryIterator, error)

func (function listerFunc) List(ctx context.Context, path filesystem.Path, options filesystem.ListOptions) (filesystem.EntryIterator, error) {
	return function(ctx, path, options)
}

type edgeIterator struct {
	entries  []filesystem.Entry
	index    int
	err      error
	closeErr error
}

func (i *edgeIterator) Next() bool {
	if i.index >= len(i.entries) {
		return false
	}
	i.index++
	return true
}

func (i *edgeIterator) Entry() filesystem.Entry { return i.entries[i.index-1] }
func (i *edgeIterator) Err() error              { return i.err }
func (i *edgeIterator) Close() error            { return i.closeErr }

func TestIOFSRequiresEveryCapability(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewIOFS() did not panic for missing capabilities")
		}
	}()
	filesystem.NewIOFS(nil, nil, nil)
}

func TestIOFSMapsCapabilityAndPathFailures(t *testing.T) {
	t.Parallel()

	injected := errors.New("backend unavailable")
	reader := readerFunc(func(context.Context, filesystem.Path) (io.ReadCloser, error) {
		return nil, injected
	})
	statter := statterFunc(func(_ context.Context, path filesystem.Path) (filesystem.Metadata, error) {
		switch path.String() {
		case "file.txt":
			return filesystem.Metadata{Path: path, Kind: filesystem.EntryKindFile}, nil
		case "invalid.txt":
			return filesystem.Metadata{}, filesystem.ErrInvalidPath
		default:
			return filesystem.Metadata{}, injected
		}
	})
	lister := listerFunc(func(context.Context, filesystem.Path, filesystem.ListOptions) (filesystem.EntryIterator, error) {
		return nil, injected
	})
	bridge := filesystem.NewIOFS(reader, statter, lister)

	if _, err := bridge.Open("file.txt"); !errors.Is(err, injected) {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := bridge.Stat("backend.txt"); !errors.Is(err, injected) {
		t.Fatalf("Stat() error = %v", err)
	}
	if _, err := bridge.Stat("bad\x00name"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Stat(invalid) error = %v", err)
	}
	if _, err := bridge.ReadDir("../escape"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("ReadDir(invalid) error = %v", err)
	}
	if _, err := bridge.Open("invalid.txt"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Open(backend invalid) error = %v", err)
	}
}

func TestIOFSHandlesListingFailures(t *testing.T) {
	t.Parallel()

	injected := errors.New("listing failed")
	missing := statterFunc(func(context.Context, filesystem.Path) (filesystem.Metadata, error) {
		return filesystem.Metadata{}, filesystem.ErrNotFound
	})
	reader := readerFunc(func(context.Context, filesystem.Path) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("")), nil
	})

	t.Run("list call", func(t *testing.T) {
		lister := listerFunc(func(context.Context, filesystem.Path, filesystem.ListOptions) (filesystem.EntryIterator, error) {
			return nil, injected
		})
		bridge := filesystem.NewIOFS(reader, missing, lister)
		if _, err := bridge.Open("directory"); !errors.Is(err, injected) {
			t.Fatalf("Open() error = %v", err)
		}
	})

	for _, failure := range []struct {
		name     string
		iterator *edgeIterator
	}{
		{name: "iterator", iterator: &edgeIterator{err: injected}},
		{name: "close", iterator: &edgeIterator{closeErr: injected}},
	} {
		failure := failure
		t.Run(failure.name, func(t *testing.T) {
			lister := listerFunc(func(context.Context, filesystem.Path, filesystem.ListOptions) (filesystem.EntryIterator, error) {
				return failure.iterator, nil
			})
			bridge := filesystem.NewIOFS(reader, missing, lister)
			if _, err := bridge.Stat("directory"); !errors.Is(err, injected) {
				t.Fatalf("Stat() error = %v", err)
			}
		})
	}
}

func TestIOFSDirectoryListingFailures(t *testing.T) {
	t.Parallel()

	injected := errors.New("page failed")
	directory := statterFunc(func(_ context.Context, path filesystem.Path) (filesystem.Metadata, error) {
		return filesystem.Metadata{Path: path, Kind: filesystem.EntryKindDirectory}, nil
	})
	reader := readerFunc(func(context.Context, filesystem.Path) (io.ReadCloser, error) {
		return nil, errors.New("unexpected read")
	})

	for _, failure := range []struct {
		name     string
		listErr  error
		iterator *edgeIterator
	}{
		{name: "list", listErr: injected},
		{name: "iterator", iterator: &edgeIterator{err: injected}},
		{name: "close", iterator: &edgeIterator{closeErr: injected}},
	} {
		failure := failure
		t.Run(failure.name, func(t *testing.T) {
			lister := listerFunc(func(context.Context, filesystem.Path, filesystem.ListOptions) (filesystem.EntryIterator, error) {
				if failure.iterator == nil {
					return nil, failure.listErr
				}
				return &edgeIterator{err: failure.iterator.err, closeErr: failure.iterator.closeErr}, nil
			})
			bridge := filesystem.NewIOFS(reader, directory, lister)
			if _, err := bridge.Open("directory"); !errors.Is(err, injected) {
				t.Fatalf("Open() error = %v", err)
			}
			if _, err := bridge.ReadDir("directory"); !errors.Is(err, injected) {
				t.Fatalf("ReadDir() error = %v", err)
			}
		})
	}
}

func TestIOFSReadDirRejectsFilesAndExposesEntryInfo(t *testing.T) {
	t.Parallel()

	adapter := memory.New(memory.WithClock(func() time.Time {
		return time.Unix(123, 0).UTC()
	}))
	path := filesystem.MustParsePath("directory/file.txt")
	if _, err := adapter.Write(
		context.Background(),
		path,
		strings.NewReader("data"),
		filesystem.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	bridge := filesystem.NewIOFS(adapter, adapter, adapter)
	if _, err := bridge.ReadDir("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadDir(missing) error = %v", err)
	}
	if _, err := bridge.ReadDir("directory/file.txt"); err == nil {
		t.Fatal("ReadDir(file) error = nil")
	}
	entries, err := bridge.ReadDir("directory")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type() != 0 {
		t.Fatalf("ReadDir() entries = %v", entries)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Name() != "file.txt" || info.Size() != 4 || info.ModTime() != time.Unix(123, 0).UTC() || info.Sys() != nil || info.Mode() != 0o444 {
		t.Fatalf("Info() = name %q size %d time %v sys %v mode %v", info.Name(), info.Size(), info.ModTime(), info.Sys(), info.Mode())
	}
	root, err := bridge.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	if root.Mode() != fs.ModeDir|0o555 {
		t.Fatalf("root mode = %v", root.Mode())
	}
}

func TestIOFSDirectoryFilePaginationAndClose(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	for _, name := range []string{"directory/a", "directory/b"} {
		if _, err := adapter.Write(
			context.Background(),
			filesystem.MustParsePath(name),
			strings.NewReader(name),
			filesystem.WriteOptions{},
		); err != nil {
			t.Fatal(err)
		}
	}
	bridge := filesystem.NewIOFS(adapter, adapter, adapter)
	opened, err := bridge.Open("directory")
	if err != nil {
		t.Fatal(err)
	}
	directory := opened.(fs.ReadDirFile)
	if info, err := directory.Stat(); err != nil || !info.IsDir() {
		t.Fatalf("Stat(open directory) = %v, %v", info, err)
	}
	first, err := directory.ReadDir(1)
	if err != nil || len(first) != 1 {
		t.Fatalf("ReadDir(1) = %v, %v", first, err)
	}
	second, err := directory.ReadDir(3)
	if err != nil || len(second) != 1 {
		t.Fatalf("ReadDir(3) = %v, %v", second, err)
	}
	if entries, err := directory.ReadDir(1); !errors.Is(err, io.EOF) || entries != nil {
		t.Fatalf("ReadDir(exhausted) = %v, %v", entries, err)
	}
	if err := directory.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.Stat(); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("Stat(closed) error = %v", err)
	}
	if _, err := directory.ReadDir(0); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("ReadDir(closed) error = %v", err)
	}

	opened, err = bridge.Open("directory")
	if err != nil {
		t.Fatal(err)
	}
	directory = opened.(fs.ReadDirFile)
	defer func() { _ = directory.Close() }()
	if entries, err := directory.ReadDir(0); err != nil || len(entries) != 2 {
		t.Fatalf("ReadDir(0) = %v, %v", entries, err)
	}
}
