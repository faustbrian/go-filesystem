package local

import (
	"errors"
	"os"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"golang.org/x/sys/unix"
)

// renameNoReplace performs one exclusive rename; an unavailable primitive must
// never fall back to a racy rename or a separate source unlink.
func renameNoReplace(source *os.File, sourceName string, destination *os.File, destinationName string) error {
	err := unix.Renameat2(int(source.Fd()), sourceName, int(destination.Fd()), destinationName, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
		return errors.Join(filesystem.Unsupported("local", filesystem.CapabilityMove, filesystem.OperationMove), err)
	}
	return err
}
