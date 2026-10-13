//go:build !linux && !darwin && !windows

package local

import (
	filesystem "github.com/faustbrian/go-filesystem/v2"
	"os"
)

// renameNoReplace rejects an unavailable exclusive operation without mutating
// either entry. Overwrite-enabled Move still uses the portable os.Root API.
func renameNoReplace(*os.File, string, *os.File, string) error {
	return filesystem.Unsupported("local", filesystem.CapabilityMove, filesystem.OperationMove)
}
