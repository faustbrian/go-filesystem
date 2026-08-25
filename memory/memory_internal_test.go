package memory

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem"
)

func TestEffectiveListLimitBoundaries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		requested int
		available int
		want      int
	}{
		{requested: 0, available: 3, want: 3},
		{requested: 1, available: 3, want: 1},
		{requested: 3, available: 3, want: 3},
		{requested: 4, available: 3, want: 3},
	} {
		if got := effectiveListLimit(test.requested, test.available); got != test.want {
			t.Errorf("effectiveListLimit(%d, %d) = %d, want %d", test.requested, test.available, got, test.want)
		}
	}
}

func TestRangeAndIteratorExactBoundaries(t *testing.T) {
	t.Parallel()

	adapter := New()
	path := filesystem.MustParsePath("object")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("0123"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		byteRange filesystem.ByteRange
		want      string
	}{
		{byteRange: filesystem.ByteRange{Offset: 1, Length: 2}, want: "12"},
		{byteRange: filesystem.ByteRange{Offset: 1, Length: 3}, want: "123"},
		{byteRange: filesystem.ByteRange{Offset: 1, Length: 4}, want: "123"},
	} {
		stream, err := adapter.OpenRange(context.Background(), path, test.byteRange)
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || string(content) != test.want {
			t.Errorf("OpenRange(%+v) = %q, %v, %v", test.byteRange, content, readErr, closeErr)
		}
	}

	entry := filesystem.Entry{Path: path}
	iterator := &iterator{entries: []filesystem.Entry{entry}}
	if got := iterator.Entry(); !got.Path.IsRoot() {
		t.Fatalf("Entry(before Next) = %+v", got)
	}
	if !iterator.Next() || iterator.Entry().Path != path || iterator.Next() {
		t.Fatal("iterator did not expose exactly one entry")
	}
	if iterator.Entry().Path != path {
		t.Fatal("iterator lost current entry after exhaustion")
	}
	if err := iterator.Close(); err != nil || iterator.Next() {
		t.Fatalf("Close() = %v", err)
	}
}

func TestContextReaderStopsBeforeSource(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := contextReader{ctx: ctx, reader: strings.NewReader("content")}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() error = %v", err)
	}
}

func TestWriteCopyAndMovePreconditionMatrices(t *testing.T) {
	t.Parallel()

	adapter := New()
	source := filesystem.MustParsePath("source")
	destination := filesystem.MustParsePath("destination")
	if _, err := adapter.Write(context.Background(), source, strings.NewReader("source"), filesystem.WriteOptions{IfNoneMatch: true}); err != nil {
		t.Fatalf("create-only Write(new) error = %v", err)
	}
	if _, err := adapter.Write(context.Background(), source, strings.NewReader("replacement"), filesystem.WriteOptions{}); err != nil {
		t.Fatalf("overwrite Write(existing) error = %v", err)
	}
	if err := adapter.Copy(context.Background(), source, destination, filesystem.CopyOptions{}); err != nil {
		t.Fatalf("Copy(new destination) error = %v", err)
	}
	if err := adapter.Copy(context.Background(), source, destination, filesystem.CopyOptions{Overwrite: true}); err != nil {
		t.Fatalf("Copy(overwrite) error = %v", err)
	}
	moveDestination := filesystem.MustParsePath("move-destination")
	if err := adapter.Move(context.Background(), destination, moveDestination, filesystem.MoveOptions{}); err != nil {
		t.Fatalf("Move(new destination) error = %v", err)
	}
	if _, err := adapter.Write(context.Background(), destination, strings.NewReader("occupied"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Move(context.Background(), moveDestination, destination, filesystem.MoveOptions{Overwrite: true}); err != nil {
		t.Fatalf("Move(overwrite) error = %v", err)
	}
}
