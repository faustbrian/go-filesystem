package local

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestMoveFailedParentAcquisitionClosesSourceHandle counts only handles for its
// unique owned root, avoiding unrelated descriptors opened by parallel tests.
func TestMoveFailedParentAcquisitionClosesSourceHandle(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	count := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, entry := range entries {
			if target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name())); err == nil && target == directory {
				found++
			}
		}
		return found
	}
	before := count()
	if before == 0 {
		t.Fatal("owned root handle not visible in procfs")
	}
	for range 32 {
		if err := (osRoot{root: root}).RenameNoReplace("source", "missing/destination"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("missing destination parent was not rejected")
		}
	}
	if count() != before {
		t.Fatal("failed destination-parent acquisition leaked source-parent handles")
	}
}
