package local

import (
	"bytes"
	"reflect"
	"testing"
)

func TestMoveWindowsRenameBufferNativePointerABI(t *testing.T) {
	// The native uintptr type supplies an independent width oracle, while the
	// fixed fixtures describe the Windows ABI rather than recalculating offsets.
	nativeWidth := int(reflect.TypeOf(uintptr(0)).Size())
	if windowsPointerBytes != nativeWidth {
		t.Fatalf("rename pointer width=%d, native=%d", windowsPointerBytes, nativeWidth)
	}
	var size int
	var prefix []byte
	switch nativeWidth {
	case 4:
		size = 532
		prefix = []byte{0, 0, 0, 0, 0x11, 0x22, 0x33, 0x44, 2, 0, 0, 0, 'x', 0, 0, 0}
	case 8:
		size = 544
		prefix = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0x11, 0x22, 0x33, 0x44, 0, 0, 0, 0, 2, 0, 0, 0, 'x', 0, 0, 0}
	default:
		t.Fatalf("unsupported native pointer width=%d", nativeWidth)
	}
	want := make([]byte, size)
	copy(want, prefix)
	got := marshalWindowsRenameInformation(windowsPointerBytes, 0x44332211, []uint16{'x', 0})
	if !bytes.Equal(got, want) {
		t.Fatalf("native-pointer rename ABI differs: got %d bytes, want %d", len(got), len(want))
	}
}

func TestMoveWindowsRenameBufferZeroEncodedFilename(t *testing.T) {
	// A NUL-only input is an admitted serializer scalar with FileNameLength=0;
	// this does not promise that public Move accepts an empty basename.
	for _, test := range []struct{ width, size int }{{4, 532}, {8, 544}} {
		got := marshalWindowsRenameInformation(test.width, 0, []uint16{0})
		if !bytes.Equal(got, make([]byte, test.size)) {
			t.Errorf("zero-length filename ABI width=%d: got %d bytes, want %d zero bytes", test.width, len(got), test.size)
		}
	}
}

func TestMoveWindowsRenameBufferMaximum32BitDirectoryScalar(t *testing.T) {
	// The largest unsigned 32-bit scalar fits the ABI field exactly. This is a
	// serialization boundary, not a promise about operating-system handle validity.
	got := marshalWindowsRenameInformation(4, 0xffffffff, []uint16{'x', 0})
	want := make([]byte, 532)
	copy(want, []byte{0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 2, 0, 0, 0, 'x', 0, 0, 0})
	if !bytes.Equal(got, want) {
		t.Fatalf("maximum directory scalar ABI differs: got %d bytes, want %d", len(got), len(want))
	}
}
