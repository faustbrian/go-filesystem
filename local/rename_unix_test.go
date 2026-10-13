//go:build linux || darwin

package local

import (
	"errors"
	"fmt"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"golang.org/x/sys/unix"
)

// TestMoveExclusiveRenameErrorClassification characterizes unavailable native
// operations separately from collisions, cross-device moves and invalid handles.
// Injecting an errno here does not claim to exercise an unsupported host kernel.
func TestMoveExclusiveRenameErrorClassification(t *testing.T) {
	for _, unavailable := range []error{unix.ENOSYS, unix.EOPNOTSUPP, fmt.Errorf("native: %w", unix.ENOSYS)} {
		got := exclusiveRenameError(unavailable)
		var capabilityError *filesystem.CapabilityError
		if !errors.Is(got, filesystem.ErrUnsupportedCapability) || !errors.Is(got, unavailable) ||
			!errors.As(got, &capabilityError) || capabilityError.Adapter != "local" || capabilityError.Capability != filesystem.CapabilityMove || capabilityError.Operation != filesystem.OperationMove {
			t.Fatalf("unavailable rename classification=%v", got)
		}
	}
	for _, ordinary := range []error{nil, unix.EEXIST, unix.EXDEV, unix.EBADF, unix.EINVAL} {
		if got := exclusiveRenameError(ordinary); got != ordinary {
			t.Fatalf("native rename result replaced: got=%v want=%v", got, ordinary)
		}
	}
}
