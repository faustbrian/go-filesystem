package memory_test

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

func FuzzMetadataAndListings(f *testing.F) {
	f.Add("object.txt", "key", "value")
	f.Add("nested/object.txt", "unicode", "雪")
	f.Add("directory/item", "empty", "")

	f.Fuzz(func(t *testing.T, rawPath, key, value string) {
		path, err := filesystem.ParsePath(rawPath)
		if err != nil {
			t.Skip()
		}
		adapter := memory.New()
		metadata := map[string]string{key: value}
		if _, err := adapter.Write(
			context.Background(),
			path,
			strings.NewReader("content"),
			filesystem.WriteOptions{Metadata: metadata},
		); err != nil {
			t.Fatal(err)
		}
		metadata[key] = "mutated"
		stat, err := adapter.Stat(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if stat.UserMetadata[key] != value {
			t.Fatalf("metadata alias: got %q, want %q", stat.UserMetadata[key], value)
		}

		iterator, err := adapter.List(
			context.Background(),
			filesystem.Root(),
			filesystem.ListOptions{Recursive: true, Limit: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = iterator.Close() }()
		if !iterator.Next() || iterator.Entry().Path != path {
			t.Fatalf("listing did not return %q", path)
		}
		if iterator.Next() {
			t.Fatal("listing exceeded its limit")
		}
	})
}

func BenchmarkStreaming(b *testing.B) {
	payload := strings.Repeat("0123456789abcdef", 64*1024)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		adapter := memory.New()
		path := filesystem.MustParsePath("large-object.bin")
		if _, err := adapter.Write(
			context.Background(),
			path,
			strings.NewReader(payload),
			filesystem.WriteOptions{},
		); err != nil {
			b.Fatal(err)
		}
		stream, err := adapter.Open(context.Background(), path)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, stream); err != nil {
			b.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListing(b *testing.B) {
	adapter := memory.New()
	for index := 0; index < 1_000; index++ {
		name := filesystem.MustParsePath("objects/item-" + strconv.Itoa(index))
		if _, err := adapter.Write(
			context.Background(),
			name,
			strings.NewReader("x"),
			filesystem.WriteOptions{},
		); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		iterator, err := adapter.List(
			context.Background(),
			filesystem.Root(),
			filesystem.ListOptions{Recursive: true, Limit: 1_000},
		)
		if err != nil {
			b.Fatal(err)
		}
		for iterator.Next() {
		}
		if err := iterator.Err(); err != nil {
			b.Fatal(err)
		}
		if err := iterator.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
