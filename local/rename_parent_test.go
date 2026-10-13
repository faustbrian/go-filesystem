package local

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestMoveExclusiveRenameMissingDestinationParent preserves the admitted source
// and reports the actual parent-open failure before any native rename executes.
func TestMoveExclusiveRenameMissingDestinationParent(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "source"), []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	err = (osRoot{root: root}).RenameNoReplace("source", "missing/destination")
	var parentError *os.PathError
	if !errors.Is(err, os.ErrNotExist) || !errors.As(err, &parentError) || parentError.Op != "openat" && parentError.Op != "open" {
		t.Fatalf("destination parent error=%v", err)
	}
	if filepath.ToSlash(parentError.Path) != "missing" {
		t.Fatal("failure was not attributed to the missing destination parent")
	}
	content, err := root.ReadFile("source")
	if err != nil || string(content) != "unchanged" {
		t.Fatal("failed parent acquisition changed the source")
	}
	if _, err := root.Stat("missing/destination"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed parent acquisition published a destination")
	}
}
