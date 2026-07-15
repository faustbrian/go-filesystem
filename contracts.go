package filesystem

import (
	"context"
	"io"
	"time"
)

// Reader opens objects as streams. The caller must close successful results.
type Reader interface {
	Open(context.Context, Path) (io.ReadCloser, error)
}

// RangeReader opens a bounded region of an object as a stream.
type RangeReader interface {
	OpenRange(context.Context, Path, ByteRange) (io.ReadCloser, error)
}

// Writer consumes a stream and does not require the whole object in memory.
type Writer interface {
	Write(context.Context, Path, io.Reader, WriteOptions) (Metadata, error)
}

// WriteOpener exposes a streaming writer when a backend can support one
// without weakening its cleanup or error guarantees.
type WriteOpener interface {
	OpenWriter(context.Context, Path, WriteOptions) (io.WriteCloser, error)
}

// Deleter removes an object.
type Deleter interface {
	Delete(context.Context, Path) error
}

// Lister returns a bounded, closeable iterator over logical entries.
type Lister interface {
	List(context.Context, Path, ListOptions) (EntryIterator, error)
}

// Statter retrieves object metadata.
type Statter interface {
	Stat(context.Context, Path) (Metadata, error)
}

// Copier copies an object without implying atomicity.
type Copier interface {
	Copy(context.Context, Path, Path, CopyOptions) error
}

// Mover moves an object according to adapter-specific documented guarantees.
type Mover interface {
	Move(context.Context, Path, Path, MoveOptions) error
}

// MetadataSetter replaces user-controlled metadata where supported.
type MetadataSetter interface {
	SetMetadata(context.Context, Path, map[string]string) error
}

// Checksummer retrieves a backend checksum without assuming algorithms match.
type Checksummer interface {
	Checksum(context.Context, Path, ChecksumAlgorithm) (Checksum, error)
}

// TemporaryURLer creates a time-limited URL where the backend supports it.
type TemporaryURLer interface {
	TemporaryURL(context.Context, Path, time.Duration, TemporaryURLOptions) (string, error)
}

// VisibilityManager reads and changes coarse object visibility.
type VisibilityManager interface {
	Visibility(context.Context, Path) (Visibility, error)
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
	// Limit bounds returned entries; zero selects the adapter maximum.
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

// EntryIterator permits adapters to page listings without unbounded buffering.
type EntryIterator interface {
	Next() bool
	Entry() Entry
	Err() error
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
