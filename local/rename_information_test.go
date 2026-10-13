package local

import (
	"bytes"
	"testing"
)

func TestMoveWindowsRenameBufferABI(t *testing.T) {
	// The fixed expected layouts come from the Windows FILE_RENAME_INFORMATION
	// declaration, not from the serializer's offset/alignment calculations.
	for _, test := range []struct {
		name             string
		pointerBytes     int
		directory        uint64
		size             int
		prefix           []byte
		attributesLength uint32
	}{
		{"32-bit", 4, 0x44332211, 532, []byte{0, 0, 0, 0, 0x11, 0x22, 0x33, 0x44, 4, 0, 0, 0, 0x61, 0, 0xac, 0x20, 0, 0}, 24},
		{"64-bit", 8, 0x8877665544332211, 544, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 4, 0, 0, 0, 0x61, 0, 0xac, 0x20, 0, 0}, 48},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := marshalWindowsRenameInformation(test.pointerBytes, test.directory, []uint16{'a', 0x20ac, 0})
			want := make([]byte, test.size)
			copy(want, test.prefix)
			if !bytes.Equal(got, want) {
				t.Fatalf("rename ABI differs: size=%d prefix=%x", len(got), got[:len(test.prefix)])
			}
			if got := windowsObjectAttributesLength(test.pointerBytes); got != test.attributesLength {
				t.Fatalf("OBJECT_ATTRIBUTES length=%d, want%d", got, test.attributesLength)
			}
		})
	}
}

func TestMoveWindowsRenameBufferMaximumName(t *testing.T) {
	filename := make([]uint16, 260)
	for index := range filename[:259] {
		filename[index] = 'x'
	}
	for _, test := range []struct{ pointerBytes, size, lengthOffset, filenameOffset int }{
		{4, 532, 8, 12}, {8, 544, 16, 20},
	} {
		got := marshalWindowsRenameInformation(test.pointerBytes, 0, filename)
		if len(got) != test.size {
			t.Fatalf("buffer length=%d", len(got))
		}
		// 259 UTF-16 units occupy 518 bytes: little-endian length 0x00000206.
		if !bytes.Equal(got[test.lengthOffset:test.lengthOffset+4], []byte{6, 2, 0, 0}) {
			t.Fatal("maximum filename length includes terminator or is truncated")
		}
		for index := range 259 {
			if !bytes.Equal(got[test.filenameOffset+2*index:test.filenameOffset+2*index+2], []byte{'x', 0}) {
				t.Fatalf("filename unit%d differs", index)
			}
		}
		if !bytes.Equal(got[test.filenameOffset+518:], make([]byte, test.size-test.filenameOffset-518)) {
			t.Fatal("filename terminator/alignment padding changed")
		}
	}
}

func TestMoveWindowsRenameBufferSurrogatePair(t *testing.T) {
	got := marshalWindowsRenameInformation(8, 0, []uint16{'a', 0xd834, 0xdd1e, 0})
	// FileNameLength is UTF-16 bytes, not UTF-8 bytes or Unicode scalar count.
	if !bytes.Equal(got[16:28], []byte{6, 0, 0, 0, 'a', 0, 0x34, 0xd8, 0x1e, 0xdd, 0, 0}) {
		t.Fatalf("surrogate filename ABI=%x", got[16:28])
	}
}
