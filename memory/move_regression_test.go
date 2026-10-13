package memory

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/go-filesystem/v2"
)

func TestMoveSamePathPreservesObject(t *testing.T) {
	clockCalls := 0
	adapter := New(WithClock(func() time.Time { clockCalls++; return time.Unix(int64(clockCalls), 0) }))
	path := filesystem.MustParsePath("object")
	before, err := adapter.Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{ContentType: "text/plain", Metadata: map[string]string{"key": "value"}, Visibility: filesystem.VisibilityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Move(context.Background(), path, path, filesystem.MoveOptions{}); !errors.Is(err, filesystem.ErrAlreadyExists) {
		t.Fatalf("no overwrite = %v", err)
	}
	if err := adapter.Move(context.Background(), path, path, filesystem.MoveOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	after, err := adapter.Stat(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || clockCalls != 1 {
		t.Fatalf("self move changed metadata: before=%+v after=%+v calls=%d", before, after, clockCalls)
	}
	stream, err := adapter.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	content, err := io.ReadAll(stream)
	if err != nil || string(content) != "content" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	missing := filesystem.MustParsePath("missing")
	if err := adapter.Move(context.Background(), missing, missing, filesystem.MoveOptions{Overwrite: true}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("missing self move=%v", err)
	}
}
