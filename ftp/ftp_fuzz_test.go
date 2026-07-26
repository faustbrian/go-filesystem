package ftp

import (
	"context"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

func FuzzMalformedListingEntry(f *testing.F) {
	for _, seed := range []string{"object.txt", "directory", "..", "bad\x00name", "slash/name", "雪.txt"} {
		f.Add(seed, false, false)
	}
	f.Fuzz(func(t *testing.T, name string, directory, link bool) {
		session := &fakeSession{
			state:       newFakeState(),
			listEntries: []remoteEntry{{Name: name, Directory: directory, Link: link}},
		}
		adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
			return session, nil
		}, "/storage", 1, Profile{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = adapter.Close() }()
		iterator, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{})
		if err != nil {
			return
		}
		defer func() { _ = iterator.Close() }()
		if !iterator.Next() {
			t.Fatal("accepted listing did not produce an entry")
		}
		path := iterator.Entry().Path.String()
		reparsed, err := filesystem.ParsePath(path)
		if err != nil || reparsed.String() != path {
			t.Fatalf("listing accepted unstable path %q from %q: %v", path, name, err)
		}
	})
}
