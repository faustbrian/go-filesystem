package local_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/local"
)

func TestOptionsValidateModesAndSymlinkPolicy(t *testing.T) {
	t.Parallel()

	for _, option := range []local.Option{
		local.WithFileMode(0),
		local.WithDirectoryMode(0),
		local.WithSymlinkPolicy(local.SymlinkPolicy(99)),
	} {
		if adapter, err := local.New(t.TempDir(), option); err == nil {
			_ = adapter.Close()
			t.Fatal("New() error = nil for invalid option")
		}
	}
	root := t.TempDir()
	adapter, err := local.New(
		root,
		local.WithFileMode(0o640),
		local.WithDirectoryMode(0o750),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	if _, err := adapter.Write(
		context.Background(),
		filesystem.MustParsePath("directory/file"),
		strings.NewReader("x"),
		filesystem.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(filepath.Join(root, "directory", "file"))
	if err != nil || fileInfo.Mode().Perm() != 0o640 {
		t.Fatalf("file mode = %v, %v", fileInfo.Mode().Perm(), err)
	}
	directoryInfo, err := os.Stat(filepath.Join(root, "directory"))
	if err != nil || directoryInfo.Mode().Perm() != 0o750 {
		t.Fatalf("directory mode = %v, %v", directoryInfo.Mode().Perm(), err)
	}
}

func TestNewRejectsUncreatableRoot(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if adapter, err := local.New(filepath.Join(file, "child")); err == nil {
		_ = adapter.Close()
		t.Fatal("New() error = nil")
	}
}

func TestOpenStatDeleteAndRangeErrors(t *testing.T) {
	t.Parallel()

	adapter, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	path := filesystem.MustParsePath("object")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("0123"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Open(canceled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open(canceled) error = %v", err)
	}
	if _, err := adapter.Stat(canceled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stat(canceled) error = %v", err)
	}
	if err := adapter.Delete(canceled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete(canceled) error = %v", err)
	}
	missing := filesystem.MustParsePath("missing")
	if _, err := adapter.Open(context.Background(), missing); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Open(missing) error = %v", err)
	}
	if _, err := adapter.Stat(context.Background(), missing); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Stat(missing) error = %v", err)
	}
	if err := adapter.Delete(context.Background(), missing); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Delete(missing) error = %v", err)
	}
	for _, byteRange := range []filesystem.ByteRange{{Offset: -1, Length: 1}, {Length: 0}, {Offset: 4, Length: 1}} {
		if _, err := adapter.OpenRange(context.Background(), path, byteRange); !errors.Is(err, filesystem.ErrInvalidRange) {
			t.Fatalf("OpenRange(%+v) error = %v", byteRange, err)
		}
	}
	if _, err := adapter.OpenRange(context.Background(), missing, filesystem.ByteRange{Length: 1}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("OpenRange(missing) error = %v", err)
	}
	stream, err := adapter.OpenRange(context.Background(), path, filesystem.ByteRange{Offset: 1, Length: 2})
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || string(content) != "12" {
		t.Fatalf("range = %q, read %v, close %v", content, readErr, closeErr)
	}
	info, err := adapter.Stat(context.Background(), filesystem.Root())
	if err != nil || info.Kind != filesystem.EntryKindDirectory {
		t.Fatalf("Stat(root) = %+v, %v", info, err)
	}
}

func TestOpenStreamObservesCancellation(t *testing.T) {
	t.Parallel()

	adapter, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	path := filesystem.MustParsePath("object")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("data"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := adapter.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	cancel()
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read(canceled) error = %v", err)
	}
}

func TestWriteValidationCancellationAndPublicationFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	adapter, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	if _, err := adapter.Write(context.Background(), filesystem.Root(), strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrInvalidPath) {
		t.Fatalf("Write(root) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Write(ctx, filesystem.MustParsePath("canceled"), strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write(canceled) error = %v", err)
	}
	path := filesystem.MustParsePath("object")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("first"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("second"), filesystem.WriteOptions{IfNoneMatch: true}); !errors.Is(err, filesystem.ErrPreconditionFailed) {
		t.Fatalf("Write(create conflict) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "parent"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write(context.Background(), filesystem.MustParsePath("parent/child"), strings.NewReader("x"), filesystem.WriteOptions{}); err == nil {
		t.Fatal("Write(blocked parent) error = nil")
	}
	if err := os.Mkdir(filepath.Join(root, "destination"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write(context.Background(), filesystem.MustParsePath("destination"), strings.NewReader("x"), filesystem.WriteOptions{}); err == nil {
		t.Fatal("Write(directory destination) error = nil")
	}

	lateCtx, lateCancel := context.WithCancel(context.Background())
	if _, err := adapter.Write(lateCtx, filesystem.MustParsePath("late"), cancelAtEOF{cancel: lateCancel}, filesystem.WriteOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write(cancel at EOF) error = %v", err)
	}
}

func TestListBoundsProjectionSymlinksAndErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	adapter, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{Limit: -1}); err == nil {
		t.Fatal("List(negative limit) error = nil")
	}
	for _, name := range []string{"prefix/direct", "prefix/nested/item", "outside"} {
		if _, err := adapter.Write(context.Background(), filesystem.MustParsePath(name), strings.NewReader(name), filesystem.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	iterator, err := adapter.List(context.Background(), filesystem.MustParsePath("prefix"), filesystem.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if entry := iterator.Entry(); !entry.Path.IsRoot() {
		t.Fatalf("Entry(before Next) = %+v", entry)
	}
	var entries []filesystem.Entry
	for iterator.Next() {
		entries = append(entries, iterator.Entry())
	}
	if len(entries) != 2 || entries[0].Path.String() != "prefix/direct" || entries[1].Kind != filesystem.EntryKindDirectory {
		t.Fatalf("entries = %+v", entries)
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
		t.Fatal("limited listing did not return one entry")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.List(canceled, filesystem.Root(), filesystem.ListOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("List(canceled) error = %v", err)
	}
	if err := os.Symlink("outside", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{}); err == nil {
		t.Fatal("List(symlink) error = nil")
	}
	if _, err := adapter.List(context.Background(), filesystem.MustParsePath("link"), filesystem.ListOptions{}); err == nil {
		t.Fatal("List(symlink directory) error = nil")
	}
	if err := os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestCopyMoveAndUnsupportedOperations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	adapter, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	source := filesystem.MustParsePath("source")
	destination := filesystem.MustParsePath("destination")
	missing := filesystem.MustParsePath("missing")
	if _, err := adapter.Write(context.Background(), source, strings.NewReader("source"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write(context.Background(), destination, strings.NewReader("destination"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Copy(context.Background(), source, destination, filesystem.CopyOptions{}); !errors.Is(err, filesystem.ErrAlreadyExists) {
		t.Fatalf("Copy(conflict) error = %v", err)
	}
	if err := adapter.Copy(context.Background(), missing, filesystem.MustParsePath("copy"), filesystem.CopyOptions{}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Copy(missing) error = %v", err)
	}
	if err := adapter.Move(context.Background(), source, destination, filesystem.MoveOptions{}); !errors.Is(err, filesystem.ErrAlreadyExists) {
		t.Fatalf("Move(conflict) error = %v", err)
	}
	if err := adapter.Move(context.Background(), missing, filesystem.MustParsePath("move"), filesystem.MoveOptions{Overwrite: true}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Move(missing) error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.Move(canceled, source, filesystem.MustParsePath("move"), filesystem.MoveOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Move(canceled) error = %v", err)
	}
	if err := adapter.SetVisibility(context.Background(), source, filesystem.VisibilityPublic); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("SetVisibility() error = %v", err)
	}
}

func TestChecksumAlgorithmsErrorsAndCanceledRead(t *testing.T) {
	t.Parallel()

	adapter, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	path := filesystem.MustParsePath("object")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, algorithm := range []filesystem.ChecksumAlgorithm{filesystem.ChecksumMD5, filesystem.ChecksumSHA256, filesystem.ChecksumCRC32C} {
		checksum, err := adapter.Checksum(context.Background(), path, algorithm)
		if err != nil || checksum.Algorithm != algorithm || checksum.Value == "" {
			t.Fatalf("Checksum(%q) = %+v, %v", algorithm, checksum, err)
		}
	}
	if _, err := adapter.Checksum(context.Background(), path, "sha1"); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Checksum(sha1) error = %v", err)
	}
	if _, err := adapter.Checksum(context.Background(), filesystem.MustParsePath("missing"), filesystem.ChecksumMD5); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Checksum(missing) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Checksum(ctx, path, filesystem.ChecksumMD5); !errors.Is(err, context.Canceled) {
		t.Fatalf("Checksum(canceled) error = %v", err)
	}
}

func TestClosedAdapterMapsOperationalErrors(t *testing.T) {
	t.Parallel()

	adapter, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	path := filesystem.MustParsePath("object")
	for _, call := range []func() error{
		func() error { _, err := adapter.Open(context.Background(), path); return err },
		func() error { _, err := adapter.Stat(context.Background(), path); return err },
		func() error { return adapter.Delete(context.Background(), path) },
		func() error {
			_, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{})
			return err
		},
	} {
		if err := call(); err == nil || errors.Is(err, filesystem.ErrNotFound) {
			t.Fatalf("closed operation error = %v", err)
		}
	}
}

func TestSymlinkPolicyAppliesToMutations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target", "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	adapter, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	linked := filesystem.MustParsePath("link/file")
	for _, call := range []func() error{
		func() error {
			_, err := adapter.Write(context.Background(), linked, strings.NewReader("x"), filesystem.WriteOptions{})
			return err
		},
		func() error { return adapter.Delete(context.Background(), linked) },
		func() error { _, err := adapter.Stat(context.Background(), linked); return err },
		func() error {
			return adapter.Move(context.Background(), linked, filesystem.MustParsePath("moved"), filesystem.MoveOptions{})
		},
		func() error {
			return adapter.Move(context.Background(), filesystem.MustParsePath("target/file"), linked, filesystem.MoveOptions{})
		},
	} {
		if err := call(); err == nil {
			t.Fatal("symlink mutation error = nil")
		}
	}
}

func TestConcurrentSymlinkReplacementCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	pivot := filepath.Join(root, "pivot")
	staged := filepath.Join(root, "staged")
	if err := os.Mkdir(pivot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pivot, "file"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, staged); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	adapter, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	var swapErrors []error
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 500 {
			if err := os.Rename(pivot, filepath.Join(root, "inside")); err != nil {
				mu.Lock()
				swapErrors = append(swapErrors, err)
				mu.Unlock()
				return
			}
			if err := os.Rename(staged, pivot); err != nil {
				return
			}
			if err := os.Rename(pivot, staged); err != nil {
				return
			}
			if err := os.Rename(filepath.Join(root, "inside"), pivot); err != nil {
				return
			}
		}
	}()
	logicalPath := filesystem.MustParsePath("pivot/file")
	for range 1_000 {
		stream, err := adapter.Open(context.Background(), logicalPath)
		if err != nil {
			continue
		}
		content, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr == nil && closeErr == nil && string(content) != "inside" {
			t.Fatalf("Open() escaped root and read %q", content)
		}
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(swapErrors) > 0 {
		t.Fatalf("symlink swap failed: %v", swapErrors[0])
	}
}

type cancelAtEOF struct{ cancel context.CancelFunc }

func (r cancelAtEOF) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.EOF
}
