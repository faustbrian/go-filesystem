//go:build linux || darwin || windows

package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem/v2"
)

// racingDestinationRoot creates a real competing destination at the parent
// creation boundary, after the old implementation's absence check.
type racingDestinationRoot struct {
	rootFS
	directory string
}

func (r racingDestinationRoot) MkdirAll(name string, mode fs.FileMode) error {
	if err := r.rootFS.MkdirAll(name, mode); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.directory, "directory/destination"), []byte("competitor"), 0o600)
}

func TestMoveNoOverwritePreservesConcurrentDestination(t *testing.T) {
	directory := t.TempDir()
	adapter, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	if err := os.WriteFile(filepath.Join(directory, "source"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter.root = racingDestinationRoot{rootFS: adapter.root, directory: directory}
	err = adapter.Move(context.Background(), filesystem.MustParsePath("source"), filesystem.MustParsePath("directory/destination"), filesystem.MoveOptions{})
	if !errors.Is(err, filesystem.ErrAlreadyExists) {
		t.Errorf("Move = %v, want AlreadyExists", err)
	}
	for name, want := range map[string]string{"source": "source", "directory/destination": "competitor"} {
		data, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", name, data, readErr, want)
		}
	}
}

type exclusiveRenameFailureRoot struct{ rootFS }

func (r exclusiveRenameFailureRoot) RenameNoReplace(string, string) error { return errInjected }

func TestMoveNoOverwriteFailureKeepsSource(t *testing.T) {
	directory := t.TempDir()
	adapter, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()
	if err := os.WriteFile(filepath.Join(directory, "source"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter.root = exclusiveRenameFailureRoot{adapter.root}
	err = adapter.Move(context.Background(), filesystem.MustParsePath("source"), filesystem.MustParsePath("destination"), filesystem.MoveOptions{})
	if !errors.Is(err, errInjected) {
		t.Fatalf("Move=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "source"))
	if err != nil || string(data) != "content" {
		t.Fatalf("source=%q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "destination")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("destination published=%v", err)
	}
}

func TestMoveNoOverwriteMovesRealFile(t *testing.T) {
	directory := t.TempDir()
	adapter, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()
	if err := os.WriteFile(filepath.Join(directory, "source"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filesystem.MustParsePath("source")
	destination := filesystem.MustParsePath("nested/destination")
	if err := adapter.Move(context.Background(), source, destination, filesystem.MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "source")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source remains: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "nested/destination"))
	if err != nil || string(data) != "content" {
		t.Fatalf("destination=%q, %v", data, err)
	}
	if err := adapter.Move(context.Background(), destination, destination, filesystem.MoveOptions{}); !errors.Is(err, filesystem.ErrAlreadyExists) {
		t.Fatalf("same-key no-overwrite=%v", err)
	}
	if err := adapter.Move(context.Background(), destination, destination, filesystem.MoveOptions{Overwrite: true}); err != nil {
		t.Fatalf("same-key overwrite=%v", err)
	}
}

// publicationInterleaveRoot inserts a public Write after the original source
// generation has reached destination, but before a two-step Move can remove it.
// The native exclusive-rename branch exercises the same externally observable
// interleaving without requiring a separate post-publication source deletion.
type publicationInterleaveRoot struct {
	rootFS
	publishSource func() error
}

func (r publicationInterleaveRoot) Link(source, destination string) error {
	if err := r.rootFS.Link(source, destination); err != nil {
		return err
	}
	return r.publishSource()
}

func (r publicationInterleaveRoot) RenameNoReplace(source, destination string) error {
	root := r.rootFS.(interface{ RenameNoReplace(string, string) error })
	if err := root.RenameNoReplace(source, destination); err != nil {
		return err
	}
	return r.publishSource()
}

func TestMoveNoOverwritePreservesNewSourceGeneration(t *testing.T) {
	directory := t.TempDir()
	adapter, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()
	source := filesystem.MustParsePath("source")
	if _, err := adapter.Write(context.Background(), source, strings.NewReader("original"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	adapter.root = publicationInterleaveRoot{rootFS: adapter.root, publishSource: func() error {
		_, err := adapter.Write(context.Background(), source, strings.NewReader("new generation"), filesystem.WriteOptions{})
		return err
	}}
	if err := adapter.Move(context.Background(), source, filesystem.MustParsePath("destination"), filesystem.MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"source": "new generation", "destination": "original"} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(data) != want {
			t.Errorf("%s=%q, %v; want %q", name, data, err, want)
		}
	}
}

func TestMoveNoOverwritePreservesDirectorySupport(t *testing.T) {
	directory := t.TempDir()
	adapter, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()
	if err := os.Mkdir(filepath.Join(directory, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "source/child"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Move(context.Background(), filesystem.MustParsePath("source"), filesystem.MustParsePath("nested/destination"), filesystem.MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "nested/destination/child"))
	if err != nil || string(data) != "content" {
		t.Fatalf("moved directory=%q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "source")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source remains=%v", err)
	}
}

type exclusiveUnsupportedRoot struct{ rootFS }

func (r exclusiveUnsupportedRoot) RenameNoReplace(string, string) error {
	return filesystem.Unsupported("local", filesystem.CapabilityMove, filesystem.OperationMove)
}

func TestMoveNoOverwriteUnsupportedDoesNotMutate(t *testing.T) {
	directory := t.TempDir()
	adapter, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()
	if err := os.WriteFile(filepath.Join(directory, "source"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter.root = exclusiveUnsupportedRoot{adapter.root}
	err = adapter.Move(context.Background(), filesystem.MustParsePath("source"), filesystem.MustParsePath("destination"), filesystem.MoveOptions{})
	if !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Move=%v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(directory, "source"))
	if readErr != nil || string(data) != "content" {
		t.Fatalf("source=%q,%v", data, readErr)
	}
	if _, err := os.Stat(filepath.Join(directory, "destination")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("destination changed=%v", err)
	}
	// Overwrite-enabled movement retains standard Rename even on an exclusive-
	// primitive implementation that refuses no-overwrite.
	if err := adapter.Move(context.Background(), filesystem.MustParsePath("source"), filesystem.MustParsePath("destination"), filesystem.MoveOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
}

func TestMoveNoOverwritePreservesSymlinkEntry(t *testing.T) {
	directory := t.TempDir()
	adapter, err := Open(context.Background(), directory, WithSymlinkPolicy(AllowInternalSymlinks))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()
	if err := os.WriteFile(filepath.Join(directory, "target"), []byte("target bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(directory, "source")); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Move(context.Background(), filesystem.MustParsePath("source"), filesystem.MustParsePath("destination"), filesystem.MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(directory, "destination"))
	if err != nil || target != "target" {
		t.Fatalf("moved link=%q,%v", target, err)
	}
	content, err := os.ReadFile(filepath.Join(directory, "target"))
	if err != nil || string(content) != "target bytes" {
		t.Fatalf("target changed=%q,%v", content, err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "source")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source link remains=%v", err)
	}
}

func TestMoveNoOverwriteRejectsEscapingParent(t *testing.T) {
	directory := t.TempDir()
	outside := t.TempDir()
	adapter, err := Open(context.Background(), directory, WithSymlinkPolicy(AllowInternalSymlinks))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()
	if err := os.WriteFile(filepath.Join(outside, "source"), []byte("outside bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Move(context.Background(), filesystem.MustParsePath("escape/source"), filesystem.MustParsePath("destination"), filesystem.MoveOptions{}); err == nil {
		t.Fatal("escaped source parent admitted")
	}
	content, err := os.ReadFile(filepath.Join(outside, "source"))
	if err != nil || string(content) != "outside bytes" {
		t.Fatalf("outside changed=%q,%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "destination")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("destination published=%v", err)
	}
}

func TestMoveNoOverwriteConcurrentSourcesPublishExactlyOnce(t *testing.T) {
	directory := t.TempDir()
	adapter, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()
	const attempts = 8
	for i := range attempts {
		name := fmt.Sprintf("source-%d", i)
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	type result struct {
		name string
		err  error
	}
	results := make(chan result, attempts)
	for i := range attempts {
		name := fmt.Sprintf("source-%d", i)
		go func() {
			<-start
			err := adapter.Move(context.Background(), filesystem.MustParsePath(name), filesystem.MustParsePath("destination"), filesystem.MoveOptions{})
			results <- result{name, err}
		}()
	}
	close(start)
	winner := ""
	for range attempts {
		result := <-results
		if result.err == nil {
			if winner != "" {
				t.Errorf("multiple successes: %s and %s", winner, result.name)
			}
			winner = result.name
			if _, err := os.Stat(filepath.Join(directory, result.name)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("winning source retained=%v", err)
			}
		} else {
			if !errors.Is(result.err, filesystem.ErrAlreadyExists) || !errors.Is(result.err, os.ErrExist) {
				t.Errorf("losing Move=%v", result.err)
			}
			content, err := os.ReadFile(filepath.Join(directory, result.name))
			if err != nil || string(content) != result.name {
				t.Errorf("losing source=%q,%v", content, err)
			}
		}
	}
	content, err := os.ReadFile(filepath.Join(directory, "destination"))
	if winner == "" || err != nil || string(content) != winner {
		t.Fatalf("destination=%q,%v winner=%q", content, err, winner)
	}
}
