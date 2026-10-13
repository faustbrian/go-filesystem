package local

import (
	"errors"
	"os"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"golang.org/x/sys/unix"
)

// renameNoReplace preserves the native directory and symlink-entry operation
// while excluding replacement as part of the same atomic rename.
func renameNoReplace(source *os.File, sourceName string, destination *os.File, destinationName string) error {
	err := unix.RenameatxNp(int(source.Fd()), sourceName, int(destination.Fd()), destinationName, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
		return errors.Join(filesystem.Unsupported("local", filesystem.CapabilityMove, filesystem.OperationMove), err)
	}
	return err
}
