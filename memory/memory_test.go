package memory_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/fstest"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		return memory.New()
	})
}

func TestWriteCancellationDoesNotPublishPartialObject(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelingReader{cancel: cancel}
	path := filesystem.MustParsePath("partial.txt")

	_, err := adapter.Write(ctx, path, reader, filesystem.WriteOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Write() error = %v, want context.Canceled", err)
	}
	if _, err := adapter.Stat(context.Background(), path); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Stat() error = %v, want ErrNotFound", err)
	}
}

func TestMetadataIsCopiedAcrossAPIBoundary(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	path := filesystem.MustParsePath("metadata.txt")
	metadata := map[string]string{"owner": "first"}

	_, err := adapter.Write(
		context.Background(),
		path,
		strings.NewReader("content"),
		filesystem.WriteOptions{Metadata: metadata},
	)
	if err != nil {
		t.Fatal(err)
	}
	metadata["owner"] = "mutated"

	stat, err := adapter.Stat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.UserMetadata["owner"] != "first" {
		t.Fatalf("stored owner = %q, want first", stat.UserMetadata["owner"])
	}
	stat.UserMetadata["owner"] = "second mutation"

	stat, err = adapter.Stat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.UserMetadata["owner"] != "first" {
		t.Fatalf("stored owner after result mutation = %q, want first", stat.UserMetadata["owner"])
	}
}

func TestModifiedTimeUsesConfiguredClock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 9, 0, 0, 0, time.UTC)
	adapter := memory.New(memory.WithClock(func() time.Time { return now }))
	path := filesystem.MustParsePath("clock.txt")

	metadata, err := adapter.Write(
		context.Background(),
		path,
		strings.NewReader("content"),
		filesystem.WriteOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !metadata.Modified.Equal(now) {
		t.Fatalf("Modified = %v, want %v", metadata.Modified, now)
	}
}

type cancelingReader struct {
	cancel func()
	read   bool
}

func (r *cancelingReader) Read(buffer []byte) (int, error) {
	if !r.read {
		r.read = true
		copy(buffer, "partial")
		r.cancel()
		return len("partial"), nil
	}

	return 0, io.EOF
}
