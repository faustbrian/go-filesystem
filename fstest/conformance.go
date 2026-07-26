// Package fstest provides reusable adapter conformance tests.
package fstest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

// Filesystem is the initial read-write conformance surface. Adapters may
// expose additional capabilities independently.
type Filesystem interface {
	filesystem.CapabilityReporter
	filesystem.Reader
	filesystem.RangeReader
	filesystem.Writer
	filesystem.WriteOpener
	filesystem.Deleter
	filesystem.Lister
	filesystem.Statter
	filesystem.Copier
	filesystem.Mover
	filesystem.MetadataSetter
	filesystem.Checksummer
	filesystem.VisibilityManager
}

// Factory returns an isolated adapter for one conformance test.
type Factory func(*testing.T) Filesystem

// TestFilesystem runs the common read-write adapter contract.
func TestFilesystem(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("capabilities match conformance surface", func(t *testing.T) {
		adapter := factory(t)
		for _, capability := range []filesystem.Capability{
			filesystem.CapabilityRead,
			filesystem.CapabilityWrite,
			filesystem.CapabilityStreamingWrite,
			filesystem.CapabilityDelete,
			filesystem.CapabilityList,
			filesystem.CapabilityStat,
		} {
			if !adapter.Capabilities().Supports(capability) {
				t.Errorf("Capabilities().Supports(%q) = false", capability)
			}
		}
	})

	t.Run("open writer streams and publishes on close", func(t *testing.T) {
		adapter := factory(t)
		path := filesystem.MustParsePath("streamed/writer.txt")
		writer, err := adapter.OpenWriter(
			context.Background(),
			path,
			filesystem.WriteOptions{ContentType: "text/plain"},
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range []string{"hello ", "streaming ", "writer"} {
			if _, err := io.WriteString(writer, chunk); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		stream, err := adapter.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read error = %v, close error = %v", readErr, closeErr)
		}
		if string(content) != "hello streaming writer" {
			t.Fatalf("Open() content = %q", content)
		}
	})

	t.Run("open writer validates setup before starting", func(t *testing.T) {
		adapter := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := adapter.OpenWriter(
			ctx,
			filesystem.MustParsePath("canceled.txt"),
			filesystem.WriteOptions{},
		); !errors.Is(err, context.Canceled) {
			t.Fatalf("OpenWriter(canceled) error = %v", err)
		}
		if _, err := adapter.OpenWriter(
			context.Background(),
			filesystem.Root(),
			filesystem.WriteOptions{},
		); !errors.Is(err, filesystem.ErrInvalidPath) {
			t.Fatalf("OpenWriter(root) error = %v", err)
		}
	})

	t.Run("open writer reports cancellation from close", func(t *testing.T) {
		adapter := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		writer, err := adapter.OpenWriter(
			ctx,
			filesystem.MustParsePath("canceled-writer.txt"),
			filesystem.WriteOptions{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, "partial"); err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := writer.Close(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Close() error = %v, want context.Canceled", err)
		}
	})

	t.Run("write read and stat stream", func(t *testing.T) {
		adapter := factory(t)
		path := filesystem.MustParsePath("nested/object.txt")
		metadata, err := adapter.Write(
			context.Background(),
			path,
			strings.NewReader("hello filesystem"),
			filesystem.WriteOptions{ContentType: "text/plain"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Path != path || metadata.Size != int64(len("hello filesystem")) {
			t.Fatalf("Write() metadata = %+v", metadata)
		}

		stream, err := adapter.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read error = %v, close error = %v", readErr, closeErr)
		}
		if string(content) != "hello filesystem" {
			t.Fatalf("Open() content = %q", content)
		}

		stat, err := adapter.Stat(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if stat.Kind != filesystem.EntryKindFile {
			t.Fatalf("Stat() = %+v", stat)
		}
		if adapter.Capabilities().Supports(filesystem.CapabilityMetadata) && stat.ContentType != "text/plain" {
			t.Fatalf("Stat().ContentType = %q, want text/plain", stat.ContentType)
		}
	})

	t.Run("range read", func(t *testing.T) {
		adapter := factory(t)
		path := filesystem.MustParsePath("range.txt")
		if _, err := adapter.Write(context.Background(), path, strings.NewReader("0123456789"), filesystem.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
		stream, err := adapter.OpenRange(context.Background(), path, filesystem.ByteRange{Offset: 2, Length: 4})
		if !adapter.Capabilities().Supports(filesystem.CapabilityRangeRead) {
			if !errors.Is(err, filesystem.ErrUnsupportedCapability) {
				t.Fatalf("OpenRange() error = %v, want ErrUnsupportedCapability", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read error = %v, close error = %v", readErr, closeErr)
		}
		if string(content) != "2345" {
			t.Fatalf("OpenRange() content = %q, want 2345", content)
		}
	})

	t.Run("copy move list and delete", func(t *testing.T) {
		adapter := factory(t)
		source := filesystem.MustParsePath("source.txt")
		copyPath := filesystem.MustParsePath("directory/copy.txt")
		movedPath := filesystem.MustParsePath("directory/moved.txt")
		if _, err := adapter.Write(context.Background(), source, strings.NewReader("content"), filesystem.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
		listedPath := source
		if adapter.Capabilities().Supports(filesystem.CapabilityCopy) {
			if err := adapter.Copy(context.Background(), source, copyPath, filesystem.CopyOptions{Overwrite: true}); err != nil {
				t.Fatal(err)
			}
			listedPath = copyPath
		} else if err := adapter.Copy(context.Background(), source, copyPath, filesystem.CopyOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
			t.Fatalf("Copy() error = %v, want ErrUnsupportedCapability", err)
		}
		if adapter.Capabilities().Supports(filesystem.CapabilityMove) {
			if err := adapter.Move(context.Background(), listedPath, movedPath, filesystem.MoveOptions{Overwrite: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Stat(context.Background(), listedPath); !errors.Is(err, filesystem.ErrNotFound) {
				t.Fatalf("Stat(move source) error = %v, want ErrNotFound", err)
			}
			listedPath = movedPath
		} else if err := adapter.Move(context.Background(), listedPath, movedPath, filesystem.MoveOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
			t.Fatalf("Move() error = %v, want ErrUnsupportedCapability", err)
		}

		iterator, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{Recursive: true, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for iterator.Next() {
			entry := iterator.Entry()
			if entry.Kind == filesystem.EntryKindFile {
				paths = append(paths, entry.Path.String())
			}
		}
		if err := iterator.Err(); err != nil {
			t.Fatal(err)
		}
		if err := iterator.Close(); err != nil {
			t.Fatal(err)
		}
		if !containsPath(paths, listedPath.String()) || !containsPath(paths, source.String()) {
			t.Fatalf("List() paths = %v, want %q and %q", paths, listedPath, source)
		}

		if err := adapter.Delete(context.Background(), listedPath); err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.Open(context.Background(), listedPath); !errors.Is(err, filesystem.ErrNotFound) {
			t.Fatalf("Open(deleted) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("metadata checksum and visibility", func(t *testing.T) {
		adapter := factory(t)
		path := filesystem.MustParsePath("properties.txt")
		if _, err := adapter.Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if adapter.Capabilities().Supports(filesystem.CapabilityMetadata) {
			if err := adapter.SetMetadata(context.Background(), path, map[string]string{"owner": "test"}); err != nil {
				t.Fatal(err)
			}
		} else if err := adapter.SetMetadata(context.Background(), path, nil); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
			t.Fatalf("SetMetadata() error = %v, want ErrUnsupportedCapability", err)
		}
		if adapter.Capabilities().Supports(filesystem.CapabilityVisibility) {
			if err := adapter.SetVisibility(context.Background(), path, filesystem.VisibilityPublic); err != nil {
				t.Fatal(err)
			}
			visibility, err := adapter.Visibility(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if visibility != filesystem.VisibilityPublic {
				t.Fatalf("Visibility() = %q", visibility)
			}
		} else if _, err := adapter.Visibility(context.Background(), path); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
			t.Fatalf("Visibility() error = %v, want ErrUnsupportedCapability", err)
		}
		checksum, err := adapter.Checksum(context.Background(), path, filesystem.ChecksumSHA256)
		if !adapter.Capabilities().Supports(filesystem.CapabilityChecksum) {
			if !errors.Is(err, filesystem.ErrUnsupportedCapability) {
				t.Fatalf("Checksum() error = %v, want ErrUnsupportedCapability", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if checksum.Value != "ed7002b439e9ac845f22357d822bac1444730fbdb6016d3ec9432297b9ec9f73" {
			t.Fatalf("Checksum() = %+v", checksum)
		}
	})
}

func containsPath(paths []string, target string) bool {
	for _, candidate := range paths {
		if candidate == target {
			return true
		}
	}
	return false
}
