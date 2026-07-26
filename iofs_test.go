package filesystem_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

func TestIOFSReadsStatsAndWalksLogicalDirectories(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	write := func(name, content string) {
		t.Helper()
		if _, err := adapter.Write(
			context.Background(),
			filesystem.MustParsePath(name),
			strings.NewReader(content),
			filesystem.WriteOptions{},
		); err != nil {
			t.Fatal(err)
		}
	}
	write("directory/first.txt", "first")
	write("directory/nested/second.txt", "second")
	write("root.txt", "root")

	bridge := filesystem.NewIOFS(adapter, adapter, adapter)
	content, err := fs.ReadFile(bridge, "directory/first.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first" {
		t.Fatalf("ReadFile() content = %q", content)
	}
	info, err := fs.Stat(bridge, "directory")
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Name() != "directory" {
		t.Fatalf("Stat(directory) = name %q, mode %v", info.Name(), info.Mode())
	}

	var walked []string
	err = fs.WalkDir(bridge, ".", func(name string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		walked = append(walked, name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ".,directory,directory/first.txt,directory/nested,directory/nested/second.txt,root.txt"
	if strings.Join(walked, ",") != want {
		t.Fatalf("WalkDir() paths = %v, want %s", walked, want)
	}
}

func TestIOFSRejectsAmbiguousNames(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	bridge := filesystem.NewIOFS(adapter, adapter, adapter)
	for _, name := range []string{"../escape", "/absolute", `directory\file.txt`, ""} {
		_, err := bridge.Open(name)
		if err == nil {
			t.Fatalf("Open(%q) error = nil", name)
		}
		var pathError *fs.PathError
		if !errors.As(err, &pathError) || !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("Open(%q) error = %v, want PathError wrapping ErrInvalid", name, err)
		}
	}
}

func TestIOFSMapsMissingObjectsToFSErrNotExist(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	bridge := filesystem.NewIOFS(adapter, adapter, adapter)
	_, err := bridge.Open("missing.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open() error = %v, want fs.ErrNotExist", err)
	}
}

func TestIOFSDirectoryFileRejectsRead(t *testing.T) {
	t.Parallel()

	adapter := memory.New()
	if _, err := adapter.Write(
		context.Background(),
		filesystem.MustParsePath("directory/file.txt"),
		strings.NewReader("content"),
		filesystem.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	bridge := filesystem.NewIOFS(adapter, adapter, adapter)
	directory, err := bridge.Open("directory")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = directory.Close() }()
	if _, err := directory.Read(make([]byte, 1)); err == nil {
		t.Fatal("directory Read() error = nil")
	}
}
