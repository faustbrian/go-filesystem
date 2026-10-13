//go:build linux || darwin

package local

import (
	"errors"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"golang.org/x/sys/unix"
)

// exclusiveRenameError classifies only unavailable primitives while preserving
// the native cause. Other failures must not enable a fallback or replacement.
func exclusiveRenameError(err error) error {
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
		return errors.Join(filesystem.Unsupported("local", filesystem.CapabilityMove, filesystem.OperationMove), err)
	}
	return err
}
