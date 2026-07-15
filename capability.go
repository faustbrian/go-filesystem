package filesystem

import (
	"fmt"
)

// Capability identifies an independently supported filesystem operation.
type Capability string

const (
	// CapabilityRead opens complete objects for streaming reads.
	CapabilityRead Capability = "read"
	// CapabilityWrite consumes readers and publishes objects.
	CapabilityWrite Capability = "write"
	// CapabilityDelete removes objects.
	CapabilityDelete Capability = "delete"
	// CapabilityList enumerates bounded directory snapshots.
	CapabilityList Capability = "list"
	// CapabilityStat retrieves object metadata.
	CapabilityStat Capability = "stat"
	// CapabilityCopy copies objects within one adapter.
	CapabilityCopy Capability = "copy"
	// CapabilityMove renames or moves objects with documented atomicity.
	CapabilityMove Capability = "move"
	// CapabilityRangeRead opens bounded object byte ranges.
	CapabilityRangeRead Capability = "range-read"
	// CapabilityMetadata reads and updates user-controlled metadata.
	CapabilityMetadata Capability = "metadata"
	// CapabilityChecksum returns explicitly identified digest algorithms.
	CapabilityChecksum Capability = "checksum"
	// CapabilityTemporaryURL signs time-limited read URLs.
	CapabilityTemporaryURL Capability = "temporary-url"
	// CapabilityVisibility reads and changes coarse object visibility.
	CapabilityVisibility Capability = "visibility"
	// CapabilityMultipart uses bounded multipart publication for large writes.
	CapabilityMultipart Capability = "multipart-upload"
	// CapabilityStreamingWrite opens incremental io.Writer uploads.
	CapabilityStreamingWrite Capability = "streaming-write"
)

var knownCapabilities = map[Capability]struct{}{
	CapabilityRead:           {},
	CapabilityWrite:          {},
	CapabilityDelete:         {},
	CapabilityList:           {},
	CapabilityStat:           {},
	CapabilityCopy:           {},
	CapabilityMove:           {},
	CapabilityRangeRead:      {},
	CapabilityMetadata:       {},
	CapabilityChecksum:       {},
	CapabilityTemporaryURL:   {},
	CapabilityVisibility:     {},
	CapabilityMultipart:      {},
	CapabilityStreamingWrite: {},
}

// CapabilitySet is an immutable collection of supported capabilities.
type CapabilitySet struct {
	values []Capability
	index  map[Capability]struct{}
}

// NewCapabilitySet constructs a deterministic capability set. It panics for
// unknown values because advertising an invented capability is a programming
// error in an adapter.
func NewCapabilitySet(capabilities ...Capability) CapabilitySet {
	index := make(map[Capability]struct{}, len(capabilities))
	values := make([]Capability, 0, len(capabilities))
	for _, capability := range capabilities {
		if _, known := knownCapabilities[capability]; !known {
			panic(fmt.Sprintf("filesystem: unknown capability %q", capability))
		}
		if _, duplicate := index[capability]; duplicate {
			continue
		}
		index[capability] = struct{}{}
		values = append(values, capability)
	}

	return CapabilitySet{values: values, index: index}
}

// Supports reports whether capability is advertised.
func (s CapabilitySet) Supports(capability Capability) bool {
	_, supported := s.index[capability]
	return supported
}

// List returns a copy in the order capabilities were declared.
func (s CapabilitySet) List() []Capability {
	return append([]Capability(nil), s.values...)
}

// CapabilityReporter exposes an adapter's supported operations.
type CapabilityReporter interface {
	Capabilities() CapabilitySet
}
