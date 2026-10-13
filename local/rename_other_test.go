//go:build !linux && !darwin && !windows

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem/v2"
)

func TestMoveNoOverwriteUnavailablePlatformPreservesSource(t *testing.T) {
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
	destination := filesystem.MustParsePath("destination")
	if err := adapter.Move(context.Background(), source, destination, filesystem.MoveOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Move=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "source"))
	if err != nil || string(data) != "content" {
		t.Fatalf("source=%q,%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "destination")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination changed=%v", err)
	}
	if err := adapter.Move(context.Background(), source, destination, filesystem.MoveOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
}
