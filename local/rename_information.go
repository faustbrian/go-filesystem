package local

import (
	"encoding/binary"
	"strconv"
)

const (
	windowsPointerBytes           = strconv.IntSize / 8
	windowsRenameFilenameCapacity = 260
)

// windowsObjectAttributesLength follows the pinned OBJECT_ATTRIBUTES fields:
// ULONG, HANDLE, pointer, ULONG, pointer, pointer. Default Windows alignment
// occupies 24 bytes with 32-bit pointers and 48 bytes with 64-bit pointers.
func windowsObjectAttributesLength(pointerBytes int) uint32 {
	return uint32(6 * pointerBytes)
}

// marshalWindowsRenameInformation encodes the legacy Windows rename ABI without
// converting Go memory into a byte pointer. The caller supplies 4 or 8 pointer
// bytes and at most 260 UTF-16 units including the terminating NUL. The fixed
// storage and trailing pointer-alignment padding match Go's native Renameat;
// ReplaceIfExists and all padding remain zero. FileNameLength excludes the NUL.
func marshalWindowsRenameInformation(pointerBytes int, directory uint64, filename []uint16) []byte {
	lengthOffset := 2 * pointerBytes
	filenameOffset := lengthOffset + 4
	size := filenameOffset + 2*windowsRenameFilenameCapacity
	size = (size + pointerBytes - 1) & ^(pointerBytes - 1)
	information := make([]byte, size)
	if pointerBytes == 4 {
		binary.LittleEndian.PutUint32(information[pointerBytes:lengthOffset], uint32(directory))
	} else {
		binary.LittleEndian.PutUint64(information[pointerBytes:lengthOffset], directory)
	}
	binary.LittleEndian.PutUint32(information[lengthOffset:filenameOffset], uint32((len(filename)-1)*2))
	for index, unit := range filename {
		binary.LittleEndian.PutUint16(information[filenameOffset+2*index:], unit)
	}
	return information
}
