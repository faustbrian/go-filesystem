package memory_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"github.com/faustbrian/go-filesystem/v2/memory"
)

type listingExpectation struct {
	path string
	kind filesystem.EntryKind
	size int64
}

func memoryListingFixture(t *testing.T) (*memory.Adapter, time.Time) {
	t.Helper()
	now := time.Date(2026, time.July, 15, 9, 0, 0, 0, time.UTC)
	adapter := memory.New(memory.WithClock(func() time.Time { return now }))
	objects := map[string]string{
		"prefix/a.txt": "aaaa", "prefix/z.txt": "z",
		"prefix/nested/a.txt": "aa", "prefix/nested/b.txt": "bbb",
		"prefix": "outside", "prefix-other/noise": "outside",
	}
	for index := range 64 {
		objects[fmt.Sprintf("outside/noise-%02d", index)] = "outside"
	}
	for name, content := range objects {
		if _, err := adapter.Write(t.Context(), filesystem.MustParsePath(name), strings.NewReader(content), filesystem.WriteOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	return adapter, now
}

func expectMemoryListing(t *testing.T, iterator filesystem.EntryIterator, now time.Time, want []listingExpectation) {
	t.Helper()
	defer func() {
		if err := iterator.Close(); err != nil {
			t.Errorf("close listing: %v", err)
		}
	}()
	var entries []filesystem.Entry
	for iterator.Next() {
		entries = append(entries, iterator.Entry())
	}
	if err := iterator.Err(); err != nil {
		t.Fatalf("read listing: %v", err)
	}
	if len(entries) != len(want) {
		t.Fatalf("listing entries = %+v, want %+v", entries, want)
	}
	for index, expected := range want {
		actual := entries[index]
		modified := now
		if expected.kind == filesystem.EntryKindDirectory {
			modified = time.Time{}
		}
		if actual.Path.String() != expected.path || actual.Kind != expected.kind || actual.Size != expected.size || !actual.Modified.Equal(modified) {
			t.Fatalf("listing entry %d = %+v, want %+v modified %v", index, actual, expected, modified)
		}
	}
}

func TestListSnapshotsRemainCompleteAmongUnrelatedObjects(t *testing.T) {
	t.Parallel()
	adapter, now := memoryListingFixture(t)
	tests := []struct {
		name    string
		options filesystem.ListOptions
		want    []listingExpectation
	}{
		{name: "direct children", want: []listingExpectation{
			{"prefix/a.txt", filesystem.EntryKindFile, 4},
			{"prefix/nested", filesystem.EntryKindDirectory, 0},
			{"prefix/z.txt", filesystem.EntryKindFile, 1},
		}},
		{name: "recursive", options: filesystem.ListOptions{Recursive: true}, want: []listingExpectation{
			{"prefix/a.txt", filesystem.EntryKindFile, 4},
			{"prefix/nested/a.txt", filesystem.EntryKindFile, 2},
			{"prefix/nested/b.txt", filesystem.EntryKindFile, 3},
			{"prefix/z.txt", filesystem.EntryKindFile, 1},
		}},
		{name: "sorted before limit", options: filesystem.ListOptions{Recursive: true, Limit: 1}, want: []listingExpectation{
			{"prefix/a.txt", filesystem.EntryKindFile, 4},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Repeated snapshots must not depend on the internal map traversal order.
			for range 64 {
				iterator, err := adapter.List(t.Context(), filesystem.MustParsePath("prefix"), test.options)
				if err != nil {
					t.Fatal(err)
				}
				expectMemoryListing(t, iterator, now, test.want)
			}
		})
	}
	iterator, err := adapter.List(t.Context(), filesystem.MustParsePath("absent"), filesystem.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expectMemoryListing(t, iterator, now, nil)
}

func TestListIteratorRetainsItsSnapshotAfterWrites(t *testing.T) {
	t.Parallel()
	adapter, now := memoryListingFixture(t)
	directory := filesystem.MustParsePath("prefix")
	options := filesystem.ListOptions{Recursive: true}
	before, err := adapter.List(t.Context(), directory, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Delete(t.Context(), filesystem.MustParsePath("prefix/a.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write(t.Context(), filesystem.MustParsePath("prefix/new.txt"), strings.NewReader("fresh"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	expectMemoryListing(t, before, now, []listingExpectation{
		{"prefix/a.txt", filesystem.EntryKindFile, 4},
		{"prefix/nested/a.txt", filesystem.EntryKindFile, 2},
		{"prefix/nested/b.txt", filesystem.EntryKindFile, 3},
		{"prefix/z.txt", filesystem.EntryKindFile, 1},
	})
	after, err := adapter.List(t.Context(), directory, options)
	if err != nil {
		t.Fatal(err)
	}
	expectMemoryListing(t, after, now, []listingExpectation{
		{"prefix/nested/a.txt", filesystem.EntryKindFile, 2},
		{"prefix/nested/b.txt", filesystem.EntryKindFile, 3},
		{"prefix/new.txt", filesystem.EntryKindFile, 5},
		{"prefix/z.txt", filesystem.EntryKindFile, 1},
	})
}
