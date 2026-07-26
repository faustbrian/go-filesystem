package memory_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

func TestCanceledOperationsStopBeforeMutation(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	path := filesystem.MustParsePath("object.txt")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("data"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	checks := []struct {
		name string
		call func() error
	}{
		{name: "open", call: func() error { _, err := adapter.Open(ctx, path); return err }},
		{name: "delete", call: func() error { return adapter.Delete(ctx, path) }},
		{name: "stat", call: func() error { _, err := adapter.Stat(ctx, path); return err }},
		{name: "list", call: func() error { _, err := adapter.List(ctx, filesystem.Root(), filesystem.ListOptions{}); return err }},
		{name: "copy", call: func() error {
			return adapter.Copy(ctx, path, filesystem.MustParsePath("copy"), filesystem.CopyOptions{})
		}},
		{name: "move", call: func() error {
			return adapter.Move(ctx, path, filesystem.MustParsePath("move"), filesystem.MoveOptions{})
		}},
		{name: "metadata", call: func() error { return adapter.SetMetadata(ctx, path, nil) }},
		{name: "checksum", call: func() error { _, err := adapter.Checksum(ctx, path, filesystem.ChecksumMD5); return err }},
		{name: "visibility", call: func() error { return adapter.SetVisibility(ctx, path, filesystem.VisibilityPublic) }},
	}
	for _, check := range checks {
		check := check
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestOpenRangeValidatesAndClamps(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	path := filesystem.MustParsePath("range.txt")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("0123"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, byteRange := range []filesystem.ByteRange{
		{Offset: -1, Length: 1},
		{Offset: 0, Length: 0},
		{Offset: 4, Length: 1},
	} {
		if _, err := adapter.OpenRange(context.Background(), path, byteRange); !errors.Is(err, filesystem.ErrInvalidRange) {
			t.Fatalf("OpenRange(%+v) error = %v", byteRange, err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.OpenRange(canceled, path, filesystem.ByteRange{Length: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenRange(canceled) error = %v", err)
	}
	if _, err := adapter.OpenRange(context.Background(), filesystem.MustParsePath("missing"), filesystem.ByteRange{Length: 1}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("OpenRange(missing) error = %v", err)
	}
	stream, err := adapter.OpenRange(context.Background(), path, filesystem.ByteRange{Offset: 2, Length: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	content, err := io.ReadAll(stream)
	if err != nil || string(content) != "23" {
		t.Fatalf("range content = %q, %v", content, err)
	}
	overflowed, err := adapter.OpenRange(context.Background(), path, filesystem.ByteRange{Offset: 2, Length: int64(^uint64(0) >> 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = overflowed.Close() }()
	content, err = io.ReadAll(overflowed)
	if err != nil || string(content) != "23" {
		t.Fatalf("overflow range content = %q, %v", content, err)
	}
}

func TestWriteRejectsInvalidInputsAndCreateConflict(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	if _, err := adapter.Write(context.Background(), filesystem.Root(), strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrInvalidPath) {
		t.Fatalf("Write(root) error = %v", err)
	}
	path := filesystem.MustParsePath("object.txt")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{Visibility: "group"}); err == nil {
		t.Fatal("Write(invalid visibility) error = nil")
	}
	injected := errors.New("source failed")
	if _, err := adapter.Write(context.Background(), path, errorReader{err: injected}, filesystem.WriteOptions{}); !errors.Is(err, injected) {
		t.Fatalf("Write(failing source) error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	if _, err := adapter.Write(canceled, path, cancelAtEOFReader{cancel: cancel}, filesystem.WriteOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write(cancel at EOF) error = %v", err)
	}
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("first"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("second"), filesystem.WriteOptions{IfNoneMatch: true}); !errors.Is(err, filesystem.ErrPreconditionFailed) {
		t.Fatalf("Write(create conflict) error = %v", err)
	}
}

func TestMissingMutationAndDestinationConflicts(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	missing := filesystem.MustParsePath("missing")
	destination := filesystem.MustParsePath("destination")
	if err := adapter.Delete(context.Background(), missing); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := adapter.Copy(context.Background(), missing, destination, filesystem.CopyOptions{}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Copy(missing) error = %v", err)
	}
	if err := adapter.Move(context.Background(), missing, destination, filesystem.MoveOptions{}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Move(missing) error = %v", err)
	}
	if err := adapter.SetMetadata(context.Background(), missing, nil); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("SetMetadata(missing) error = %v", err)
	}
	if err := adapter.SetVisibility(context.Background(), missing, filesystem.VisibilityPrivate); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("SetVisibility(missing) error = %v", err)
	}

	source := filesystem.MustParsePath("source")
	if _, err := adapter.Write(context.Background(), source, strings.NewReader("source"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write(context.Background(), destination, strings.NewReader("destination"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Copy(context.Background(), source, destination, filesystem.CopyOptions{}); !errors.Is(err, filesystem.ErrAlreadyExists) {
		t.Fatalf("Copy(conflict) error = %v", err)
	}
	if err := adapter.Move(context.Background(), source, destination, filesystem.MoveOptions{}); !errors.Is(err, filesystem.ErrAlreadyExists) {
		t.Fatalf("Move(conflict) error = %v", err)
	}
}

func TestListValidationDirectoryProjectionAndIteratorEdges(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{Limit: -1}); err == nil {
		t.Fatal("List(negative limit) error = nil")
	}
	for _, name := range []string{"outside", "prefix/direct", "prefix/nested/item"} {
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
	if entry := iterator.Entry(); entry.Path.String() != "prefix/nested" {
		t.Fatalf("Entry(after exhaustion) = %+v", entry)
	}
	if err := iterator.Close(); err != nil || iterator.Next() {
		t.Fatalf("closed iterator = %v, next %v", err, iterator.Next())
	}
	limited, err := adapter.List(
		context.Background(),
		filesystem.Root(),
		filesystem.ListOptions{Recursive: true, Limit: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = limited.Close() }()
	if !limited.Next() || limited.Next() {
		t.Fatal("limited listing did not return exactly one entry")
	}
}

func TestChecksumAlgorithmsAndVisibilityValidation(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	path := filesystem.MustParsePath("object")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, algorithm := range []filesystem.ChecksumAlgorithm{filesystem.ChecksumMD5, filesystem.ChecksumCRC32C} {
		checksum, err := adapter.Checksum(context.Background(), path, algorithm)
		if err != nil || checksum.Algorithm != algorithm || checksum.Value == "" {
			t.Fatalf("Checksum(%q) = %+v, %v", algorithm, checksum, err)
		}
	}
	if _, err := adapter.Checksum(context.Background(), filesystem.MustParsePath("missing"), filesystem.ChecksumMD5); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Checksum(missing) error = %v", err)
	}
	if _, err := adapter.Checksum(context.Background(), path, "sha1"); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Checksum(sha1) error = %v", err)
	}
	if err := adapter.SetVisibility(context.Background(), path, "group"); err == nil {
		t.Fatal("SetVisibility(invalid) error = nil")
	}
	if err := adapter.SetVisibility(context.Background(), path, filesystem.VisibilityPublic); err != nil {
		t.Fatal(err)
	}
	visibility, err := adapter.Visibility(context.Background(), path)
	if err != nil || visibility != filesystem.VisibilityPublic {
		t.Fatalf("Visibility() = %q, %v", visibility, err)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type cancelAtEOFReader struct{ cancel context.CancelFunc }

func (r cancelAtEOFReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, io.EOF
}
