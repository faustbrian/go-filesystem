package filesystem

import (
	"context"
	"io"
	"time"
)

// Reader opens objects as streams. The caller must close successful results.
type Reader interface {
	// Open returns a caller-owned stream on success; always close it.
	// Cancellation after acquisition and read/close concurrency are provider-specific.
	Open(context.Context, Path) (io.ReadCloser, error)
}

// RangeReader opens a bounded region of an object as a stream.
type RangeReader interface {
	// OpenRange requires nonnegative Offset and positive Length where supported.
	// Providers may clamp to EOF, return empty data, or reject an unsatisfiable range;
	// the caller closes successful streams and checks late read errors.
	OpenRange(context.Context, Path, ByteRange) (io.ReadCloser, error)
}

// Writer consumes a stream; providers decide whether to buffer the whole object.
type Writer interface {
	// Write borrows source without closing it. Keep option maps stable until return.
	// Buffering, create-only support, publication and durability differ by provider;
	// an error (including post-publication Stat) does not prove no object was written.
	Write(context.Context, Path, io.Reader, WriteOptions) (Metadata, error)
}

// WriteOpener exposes a streaming writer when a backend can support one
// without weakening its cleanup or error guarantees.
type WriteOpener interface {
	// OpenWriter transfers producer ownership. Close signals EOF and waits for the
	// final publication result. Built-in adapters unblock their owned pipe on
	// cancellation; borrowed callbacks/backend calls can still require cooperation.
	// Close even after cancellation, and keep options/maps valid until completion.
	OpenWriter(context.Context, Path, WriteOptions) (io.WriteCloser, error)
}

// Deleter removes an object.
type Deleter interface {
	// Delete removes an object under provider-specific absence/idempotency rules.
	// Do not replay ambiguous remote failures without an application-safe precondition.
	Delete(context.Context, Path) error
}

// Lister returns a closeable iterator over provider-selected logical entries.
type Lister interface {
	// List returns a caller-owned iterator; close it on exhaustion and errors.
	// A result limit need not bound upstream buffering or full snapshot construction.
	List(context.Context, Path, ListOptions) (EntryIterator, error)
}

// Statter retrieves object metadata.
type Statter interface {
	// Stat returns provider metadata; optional fields may be zero and it is not
	// a transactional snapshot with a later read or write.
	Stat(context.Context, Path) (Metadata, error)
}

// Copier copies an object without implying atomicity.
type Copier interface {
	// Copy applies destination options only where supported; unsupported no-clobber
	// requests must not be assumed to preserve an atomic absence condition.
	Copy(context.Context, Path, Path, CopyOptions) error
}

// Mover moves an object according to adapter-specific documented guarantees.
type Mover interface {
	// Move has provider-specific rename/overwrite guarantees. Memory equal-path
	// overwrite preserves the object; local no-overwrite requires a native exclusive
	// rename and rejects platforms/filesystems that cannot provide it safely.
	Move(context.Context, Path, Path, MoveOptions) error
}

// MetadataSetter replaces user-controlled metadata where supported.
type MetadataSetter interface {
	// SetMetadata replaces supported user metadata, borrowing the map until return.
	// Capability advertisement is required; arbitrary providers may reject it.
	SetMetadata(context.Context, Path, map[string]string) error
}

// Checksummer retrieves a backend checksum without assuming algorithms match.
type Checksummer interface {
	// Checksum returns the requested supported algorithm, not an ETag inference.
	// A digest is not authentication or a guarantee against concurrent object changes.
	Checksum(context.Context, Path, ChecksumAlgorithm) (Checksum, error)
}

// TemporaryURLer creates a time-limited URL where the backend supports it.
type TemporaryURLer interface {
	// TemporaryURL returns sensitive bearer data; keep it out of logs. Requested
	// lifetime and signing success do not prove access or full-lifetime availability.
	TemporaryURL(context.Context, Path, time.Duration, TemporaryURLOptions) (string, error)
}

// VisibilityManager reads and changes coarse object visibility.
type VisibilityManager interface {
	// Visibility retrieves supported coarse backend policy, not effective access
	// under every bucket, account or network authorization rule.
	Visibility(context.Context, Path) (Visibility, error)
	// SetVisibility changes supported coarse backend policy; errors and advertised
	// capabilities do not imply universal ACL support.
	SetVisibility(context.Context, Path, Visibility) error
}

// ByteRange identifies a half-open byte range [Offset, Offset+Length).
type ByteRange struct {
	// Offset is the zero-based first byte.
	Offset int64
	// Length is the maximum number of bytes to return.
	Length int64
}

// WriteOptions controls object creation without assigning unsupported
// semantics to all adapters.
type WriteOptions struct {
	// ContentType records the media type where metadata is supported.
	ContentType string
	// Metadata is defensively copied user-controlled metadata.
	Metadata map[string]string
	// Visibility requests private or public publication where supported.
	Visibility Visibility
	// IfNoneMatch requires the destination to be absent where supported.
	IfNoneMatch bool
}

// ListOptions bounds and shapes a listing.
type ListOptions struct {
	// Recursive includes descendants rather than only direct children.
	Recursive bool
	// Limit bounds returned entries; zero selects provider defaults. Local and
	// memory may return all entries, while remote providers have configured caps.
	Limit int
}

// CopyOptions controls destination replacement for copies.
type CopyOptions struct {
	// Overwrite permits replacement where the adapter can guarantee it safely.
	Overwrite bool
}

// MoveOptions controls destination replacement for moves.
type MoveOptions struct {
	// Overwrite permits replacement where the adapter can guarantee it safely.
	Overwrite bool
}

// TemporaryURLOptions carries response properties understood by signing
// backends.
type TemporaryURLOptions struct {
	// DownloadName requests a response content-disposition filename.
	DownloadName string
	// ContentType requests a response content type.
	ContentType string
}

// EntryKind distinguishes objects from logical directories.
type EntryKind string

const (
	// EntryKindFile identifies an object containing bytes.
	EntryKindFile EntryKind = "file"
	// EntryKindDirectory identifies a real or synthesized directory.
	EntryKindDirectory EntryKind = "directory"
)

// Visibility is a deliberately small cross-adapter visibility vocabulary.
type Visibility string

const (
	// VisibilityPrivate restricts access according to backend policy.
	VisibilityPrivate Visibility = "private"
	// VisibilityPublic permits public access according to backend policy.
	VisibilityPublic Visibility = "public"
)

// Metadata describes an object without assuming every backend populates every
// field. Optional values are represented by their zero values.
type Metadata struct {
	// Path is the normalized logical object path.
	Path Path
	// Kind distinguishes files from directories.
	Kind EntryKind
	// Size is the byte length for files.
	Size int64
	// Modified is the backend-reported modification time.
	Modified time.Time
	// ETag is the opaque backend entity tag and is not assumed to be a digest.
	ETag string
	// ContentType is the backend-reported media type.
	ContentType string
	// UserMetadata is a defensive copy of supported custom metadata.
	UserMetadata map[string]string
	// Visibility is populated only by adapters that support it.
	Visibility Visibility
}

// Entry is one item produced by a listing.
type Entry struct {
	// Path is the normalized logical entry path.
	Path Path
	// Kind distinguishes files from directories.
	Kind EntryKind
	// Size is the byte length for files.
	Size int64
	// Modified is the backend-reported modification time.
	Modified time.Time
}

// EntryIterator supports either paged or materialized listings. It does not
// guarantee a remote snapshot or bounded provider working storage.
type EntryIterator interface {
	// Next advances the cursor and returns false on exhaustion or failure; then inspect Err.
	Next() bool
	// Entry exposes the current value; before the first successful Next it may be zero.
	Entry() Entry
	// Err reports iteration failure separately from exhaustion. A partial prefix may have been yielded.
	Err() error
	// Close releases iterator resources. Providers define idempotency and error behavior;
	// callers serialize cursor access unless an implementation says otherwise.
	Close() error
}

// ChecksumAlgorithm identifies an explicitly requested digest algorithm.
type ChecksumAlgorithm string

const (
	// ChecksumMD5 requests an MD5 digest.
	ChecksumMD5 ChecksumAlgorithm = "md5"
	// ChecksumSHA256 requests a SHA-256 digest.
	ChecksumSHA256 ChecksumAlgorithm = "sha256"
	// ChecksumCRC32C requests a CRC-32C Castagnoli digest.
	ChecksumCRC32C ChecksumAlgorithm = "crc32c"
)

// Checksum is a backend checksum and its unambiguous algorithm.
type Checksum struct {
	// Algorithm identifies how Value was calculated.
	Algorithm ChecksumAlgorithm
	// Value is the lowercase hexadecimal digest.
	Value string
}
