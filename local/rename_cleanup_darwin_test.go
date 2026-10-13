package local

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

// TestMoveFailedParentAcquisitionClosesSourceHandleDarwin isolates descriptor
// accounting from parallel tests and checks actual source-parent handle cleanup.
func TestMoveFailedParentAcquisitionClosesSourceHandleDarwin(t *testing.T) {
	const childFlag = "GOLIB_TEST_PARENT_CLEANUP_CHILD"
	if os.Getenv(childFlag) != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestMoveFailedParentAcquisitionClosesSourceHandleDarwin$", "-test.count=1", "-test.timeout=30s")
		command.Env = append(os.Environ(), childFlag+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated handle cleanup: %v\n%s", err, output)
		}
		return
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	count := func() int {
		entries, err := os.ReadDir("/dev/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()
	for range 32 {
		if err := (osRoot{root: root}).RenameNoReplace("source", "missing/destination"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("missing destination parent was not rejected")
		}
	}
	if count() != before {
		t.Fatal("failed destination-parent acquisition leaked source-parent handles")
	}
}
