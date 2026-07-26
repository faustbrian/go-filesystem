package filesystem_test

import (
	"errors"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

func TestCapabilitySetReportsSupportedOperations(t *testing.T) {
	t.Parallel()

	set := filesystem.NewCapabilitySet(
		filesystem.CapabilityRead,
		filesystem.CapabilityWrite,
		filesystem.CapabilityRangeRead,
	)

	if !set.Supports(filesystem.CapabilityRead) {
		t.Fatal("Supports(read) = false, want true")
	}
	if set.Supports(filesystem.CapabilityMove) {
		t.Fatal("Supports(move) = true, want false")
	}

	got := set.List()
	want := []filesystem.Capability{
		filesystem.CapabilityRead,
		filesystem.CapabilityWrite,
		filesystem.CapabilityRangeRead,
	}
	if len(got) != len(want) {
		t.Fatalf("List() length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("List()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestCapabilitySetIsImmutableFromCallers(t *testing.T) {
	t.Parallel()

	set := filesystem.NewCapabilitySet(filesystem.CapabilityRead)
	listed := set.List()
	listed[0] = filesystem.CapabilityWrite

	if !set.Supports(filesystem.CapabilityRead) {
		t.Fatal("mutating List result changed the set")
	}
}

func TestCapabilitySetDropsDuplicates(t *testing.T) {
	t.Parallel()

	set := filesystem.NewCapabilitySet(
		filesystem.CapabilityRead,
		filesystem.CapabilityRead,
	)
	if listed := set.List(); len(listed) != 1 || listed[0] != filesystem.CapabilityRead {
		t.Fatalf("List() = %v, want one read capability", listed)
	}
}

func TestUnsupportedCreatesTypedCapabilityError(t *testing.T) {
	t.Parallel()

	err := filesystem.Unsupported(
		"memory",
		filesystem.CapabilityTemporaryURL,
		filesystem.OperationTemporaryURL,
	)

	if !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("errors.Is() = false for ErrUnsupportedCapability: %v", err)
	}

	var capabilityError *filesystem.CapabilityError
	if !errors.As(err, &capabilityError) {
		t.Fatalf("errors.As() = false for CapabilityError: %v", err)
	}
	if capabilityError.Adapter != "memory" {
		t.Fatalf("Adapter = %q, want memory", capabilityError.Adapter)
	}
	if capabilityError.Capability != filesystem.CapabilityTemporaryURL {
		t.Fatalf("Capability = %q, want temporary-url", capabilityError.Capability)
	}
	if capabilityError.Operation != filesystem.OperationTemporaryURL {
		t.Fatalf("Operation = %q, want temporary-url", capabilityError.Operation)
	}
	if got := err.Error(); got != `filesystem: adapter "memory" does not support capability "temporary-url" for operation "temporary-url"` {
		t.Fatalf("Error() = %q", got)
	}
}

func TestCapabilitySetRejectsUnknownCapability(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewCapabilitySet() did not panic")
		}
	}()

	filesystem.NewCapabilitySet(filesystem.Capability("invented"))
}
