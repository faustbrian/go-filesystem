package local

import (
	"reflect"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMoveWindowsObjectAttributesABI(t *testing.T) {
	// Compare the encoded length with the actual pinned x/sys type on each native
	// Windows compiler, independently of the explicit production byte layout.
	attributes := reflect.TypeOf(windows.OBJECT_ATTRIBUTES{})
	if got := windowsObjectAttributesLength(windowsPointerBytes); uint64(got) != uint64(attributes.Size()) {
		t.Fatalf("OBJECT_ATTRIBUTES length=%d, native=%d", got, attributes.Size())
	}
	if windowsRenameFilenameCapacity != syscall.MAX_PATH {
		t.Fatal("native filename component bound changed")
	}
}
