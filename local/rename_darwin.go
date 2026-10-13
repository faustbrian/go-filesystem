package local

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace preserves the native directory and symlink-entry operation
// while excluding replacement as part of the same atomic rename.
func renameNoReplace(source *os.File, sourceName string, destination *os.File, destinationName string) error {
	err := unix.RenameatxNp(int(source.Fd()), sourceName, int(destination.Fd()), destinationName, unix.RENAME_EXCL)
	return exclusiveRenameError(err)
}
