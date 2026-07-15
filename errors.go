package filesystem

import (
	"errors"
	"fmt"
)

var (
	// ErrUnsupportedCapability means an adapter cannot safely provide an
	// operation with its documented semantics.
	ErrUnsupportedCapability = errors.New("unsupported filesystem capability")
	// ErrNotFound means the requested logical path does not exist.
	ErrNotFound = errors.New("filesystem path not found")
	// ErrAlreadyExists means an operation required an absent destination.
	ErrAlreadyExists = errors.New("filesystem path already exists")
	// ErrInvalidRange means a byte range is malformed or unsatisfiable.
	ErrInvalidRange = errors.New("invalid filesystem byte range")
	// ErrPreconditionFailed means a conditional request was not satisfied.
	ErrPreconditionFailed = errors.New("filesystem precondition failed")
	// ErrPartialWrite means a backend accepted only part of a write.
	ErrPartialWrite = errors.New("partial filesystem write")
	// ErrResourceLimit means remote or caller-controlled data exceeded an
	// adapter's configured allocation or cardinality bound.
	ErrResourceLimit = errors.New("filesystem resource limit exceeded")
)

// Operation names a public filesystem action for errors and instrumentation.
type Operation string

const (
	// OperationRead opens a complete object.
	OperationRead Operation = "read"
	// OperationWrite publishes an object.
	OperationWrite Operation = "write"
	// OperationDelete removes an object.
	OperationDelete Operation = "delete"
	// OperationList enumerates a logical directory.
	OperationList Operation = "list"
	// OperationStat retrieves object metadata.
	OperationStat Operation = "stat"
	// OperationCopy copies an object.
	OperationCopy Operation = "copy"
	// OperationMove moves or renames an object.
	OperationMove Operation = "move"
	// OperationRangeRead opens a byte range.
	OperationRangeRead Operation = "range-read"
	// OperationSetMetadata replaces user metadata.
	OperationSetMetadata Operation = "set-metadata"
	// OperationChecksum calculates or retrieves a digest.
	OperationChecksum Operation = "checksum"
	// OperationTemporaryURL signs a temporary read URL.
	OperationTemporaryURL Operation = "temporary-url"
	// OperationVisibility reads object visibility.
	OperationVisibility Operation = "visibility"
	// OperationSetVisibility changes object visibility.
	OperationSetVisibility Operation = "set-visibility"
)

// CapabilityError describes a requested operation that an adapter cannot
// safely implement.
type CapabilityError struct {
	// Adapter identifies the implementation rejecting the operation.
	Adapter string
	// Capability is the unavailable semantic contract.
	Capability Capability
	// Operation is the attempted public action.
	Operation Operation
}

// Error implements error.
func (e *CapabilityError) Error() string {
	return fmt.Sprintf(
		"filesystem: adapter %q does not support capability %q for operation %q",
		e.Adapter,
		e.Capability,
		e.Operation,
	)
}

// Unwrap allows errors.Is to classify all capability errors.
func (e *CapabilityError) Unwrap() error {
	return ErrUnsupportedCapability
}

// Unsupported constructs a typed unsupported-capability error.
func Unsupported(adapter string, capability Capability, operation Operation) error {
	return &CapabilityError{
		Adapter:    adapter,
		Capability: capability,
		Operation:  operation,
	}
}
