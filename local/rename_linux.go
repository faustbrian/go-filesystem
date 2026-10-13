package local

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace performs one exclusive rename; an unavailable primitive must
// never fall back to a racy rename or a separate source unlink.
func renameNoReplace(source *os.File, sourceName string, destination *os.File, destinationName string) error {
	err := unix.Renameat2(int(source.Fd()), sourceName, int(destination.Fd()), destinationName, unix.RENAME_NOREPLACE)
	return exclusiveRenameError(err)
}
