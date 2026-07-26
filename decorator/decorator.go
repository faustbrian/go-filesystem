// Package decorator provides composable policy wrappers for filesystem
// adapters without changing their underlying guarantees.
package decorator

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"reflect"
	"strings"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

// Backend is the common adapter surface wrapped by decorators. Optional
// operations still report support through Capabilities.
type Backend interface {
	filesystem.CapabilityReporter
	filesystem.Reader
	filesystem.RangeReader
	filesystem.Writer
	filesystem.WriteOpener
	filesystem.Deleter
	filesystem.Lister
	filesystem.Statter
	filesystem.Copier
	filesystem.Mover
	filesystem.MetadataSetter
	filesystem.Checksummer
	filesystem.VisibilityManager
}

// Option configures an Adapter decorator.
type Option func(*configuration) error

type configuration struct {
	prefix    filesystem.Path
	readOnly  bool
	checksums bool
	retry     *RetryPolicy
	observer  Observer
}

// WithPrefix isolates every logical path beneath prefix in the wrapped
// backend.
func WithPrefix(prefix string) Option {
	return func(configuration *configuration) error {
		parsed, err := filesystem.ParsePath(prefix)
		if err != nil {
			return fmt.Errorf("decorator: invalid prefix: %w", err)
		}
		configuration.prefix = parsed
		return nil
	}
}

// ReadOnly removes every mutating capability and returns typed capability
// errors from mutation methods.
func ReadOnly() Option {
	return func(configuration *configuration) error {
		configuration.readOnly = true
		return nil
	}
}

// WithChecksums adds MD5, SHA-256, and CRC-32C by streaming reads through the
// wrapper. It is explicit because it performs an additional full object read.
func WithChecksums() Option {
	return func(configuration *configuration) error {
		configuration.checksums = true
		return nil
	}
}

// RetryPolicy controls retries for read-safe setup operations. Retryable and
// Backoff may be called concurrently and must be safe for concurrent use.
type RetryPolicy struct {
	// Attempts includes the initial call and must be positive.
	Attempts int
	// Retryable classifies errors whose operation setup may be repeated.
	Retryable func(error) bool
	// Backoff returns the delay before the next attempt; nil means no delay.
	Backoff func(attempt int) time.Duration
}

// WithRetry retries only Open, OpenRange, List, Stat, Checksum, TemporaryURL,
// and Visibility setup. Mutating operations are never replayed.
func WithRetry(policy RetryPolicy) Option {
	return func(configuration *configuration) error {
		if policy.Attempts <= 0 {
			return errors.New("decorator: retry attempts must be positive")
		}
		if policy.Retryable == nil {
			return errors.New("decorator: retry classifier is required")
		}
		configuration.retry = &policy
		return nil
	}
}

// Event describes one decorator operation setup without including object
// content, credentials, metadata values, or signed URLs.
type Event struct {
	// Operation identifies the public action.
	Operation filesystem.Operation
	// Path is the caller-visible logical source path.
	Path filesystem.Path
	// Destination is populated for copy and move operations.
	Destination filesystem.Path
	// Started is the operation setup start time.
	Started time.Time
	// Duration is the elapsed setup time.
	Duration time.Duration
	// Error is the returned setup or operation error.
	Error error
}

// Observer receives synchronous operation events and must be concurrency-safe.
type Observer func(Event)

// WithObserver instruments operation setup and completion. Stream lifetime is
// represented by OpenWriter's Close result rather than an observer event.
func WithObserver(observer Observer) Option {
	return func(configuration *configuration) error {
		if observer == nil {
			return errors.New("decorator: observer is required")
		}
		configuration.observer = observer
		return nil
	}
}

// Adapter applies configured policies to a backend.
type Adapter struct {
	backend   Backend
	prefix    filesystem.Path
	readOnly  bool
	checksums bool
	retry     *RetryPolicy
	observer  Observer
}

// New constructs a composable adapter decorator.
func New(backend Backend, options ...Option) (*Adapter, error) {
	if backend == nil ||
		(reflect.ValueOf(backend).Kind() == reflect.Pointer && reflect.ValueOf(backend).IsNil()) {
		return nil, errors.New("decorator: backend is required")
	}
	var configuration configuration
	for _, option := range options {
		if err := option(&configuration); err != nil {
			return nil, err
		}
	}
	return &Adapter{
		backend:   backend,
		prefix:    configuration.prefix,
		readOnly:  configuration.readOnly,
		checksums: configuration.checksums,
		retry:     configuration.retry,
		observer:  configuration.observer,
	}, nil
}

// Capabilities preserves the wrapped adapter's explicit capability set.
func (a *Adapter) Capabilities() filesystem.CapabilitySet {
	capabilities := a.backend.Capabilities().List()
	if a.readOnly {
		filtered := make([]filesystem.Capability, 0, len(capabilities))
		for _, capability := range capabilities {
			switch capability {
			case filesystem.CapabilityWrite,
				filesystem.CapabilityStreamingWrite,
				filesystem.CapabilityDelete,
				filesystem.CapabilityCopy,
				filesystem.CapabilityMove,
				filesystem.CapabilityMetadata,
				filesystem.CapabilityVisibility,
				filesystem.CapabilityMultipart:
				continue
			default:
				filtered = append(filtered, capability)
			}
		}
		capabilities = filtered
	}
	if a.checksums {
		found := false
		for _, capability := range capabilities {
			found = found || capability == filesystem.CapabilityChecksum
		}
		if !found {
			capabilities = append(capabilities, filesystem.CapabilityChecksum)
		}
	}
	return filesystem.NewCapabilitySet(capabilities...)
}

// Open opens the prefixed object stream.
func (a *Adapter) Open(ctx context.Context, path filesystem.Path) (io.ReadCloser, error) {
	return observeCall(a.observer, filesystem.OperationRead, path, filesystem.Path{}, func() (io.ReadCloser, error) {
		return retryCall(ctx, a.retry, func() (io.ReadCloser, error) {
			return a.backend.Open(ctx, a.internal(path))
		})
	})
}

// OpenRange opens a prefixed bounded byte range.
func (a *Adapter) OpenRange(ctx context.Context, path filesystem.Path, byteRange filesystem.ByteRange) (io.ReadCloser, error) {
	return observeCall(a.observer, filesystem.OperationRangeRead, path, filesystem.Path{}, func() (io.ReadCloser, error) {
		return retryCall(ctx, a.retry, func() (io.ReadCloser, error) {
			return a.backend.OpenRange(ctx, a.internal(path), byteRange)
		})
	})
}

// Write publishes a prefixed object from source.
func (a *Adapter) Write(ctx context.Context, path filesystem.Path, source io.Reader, options filesystem.WriteOptions) (filesystem.Metadata, error) {
	return observeCall(a.observer, filesystem.OperationWrite, path, filesystem.Path{}, func() (filesystem.Metadata, error) {
		if a.readOnly {
			return filesystem.Metadata{}, a.unsupported(filesystem.CapabilityWrite, filesystem.OperationWrite)
		}
		if path.IsRoot() {
			return filesystem.Metadata{}, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
		}
		metadata, err := a.backend.Write(ctx, a.internal(path), source, options)
		metadata.Path = path
		return metadata, err
	})
}

// OpenWriter opens an incremental prefixed upload.
func (a *Adapter) OpenWriter(ctx context.Context, path filesystem.Path, options filesystem.WriteOptions) (io.WriteCloser, error) {
	return observeCall(a.observer, filesystem.OperationWrite, path, filesystem.Path{}, func() (io.WriteCloser, error) {
		if a.readOnly {
			return nil, a.unsupported(filesystem.CapabilityStreamingWrite, filesystem.OperationWrite)
		}
		if path.IsRoot() {
			return nil, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
		}
		return a.backend.OpenWriter(ctx, a.internal(path), options)
	})
}

// Delete removes a prefixed object.
func (a *Adapter) Delete(ctx context.Context, path filesystem.Path) error {
	_, err := observeCall(a.observer, filesystem.OperationDelete, path, filesystem.Path{}, func() (struct{}, error) {
		if a.readOnly {
			return struct{}{}, a.unsupported(filesystem.CapabilityDelete, filesystem.OperationDelete)
		}
		return struct{}{}, a.backend.Delete(ctx, a.internal(path))
	})
	return err
}

// List lists a prefixed logical directory and removes the private prefix from
// returned entries.
func (a *Adapter) List(ctx context.Context, path filesystem.Path, options filesystem.ListOptions) (filesystem.EntryIterator, error) {
	return observeCall(a.observer, filesystem.OperationList, path, filesystem.Path{}, func() (filesystem.EntryIterator, error) {
		inner, err := retryCall(ctx, a.retry, func() (filesystem.EntryIterator, error) {
			return a.backend.List(ctx, a.internal(path), options)
		})
		if err != nil {
			return nil, err
		}
		return &iterator{inner: inner, prefix: a.prefix}, nil
	})
}

// Stat retrieves metadata for a prefixed object.
func (a *Adapter) Stat(ctx context.Context, path filesystem.Path) (filesystem.Metadata, error) {
	return observeCall(a.observer, filesystem.OperationStat, path, filesystem.Path{}, func() (filesystem.Metadata, error) {
		metadata, err := retryCall(ctx, a.retry, func() (filesystem.Metadata, error) {
			return a.backend.Stat(ctx, a.internal(path))
		})
		metadata.Path = path
		return metadata, err
	})
}

// Copy copies between prefixed object paths.
func (a *Adapter) Copy(ctx context.Context, source, destination filesystem.Path, options filesystem.CopyOptions) error {
	_, err := observeCall(a.observer, filesystem.OperationCopy, source, destination, func() (struct{}, error) {
		if a.readOnly {
			return struct{}{}, a.unsupported(filesystem.CapabilityCopy, filesystem.OperationCopy)
		}
		return struct{}{}, a.backend.Copy(ctx, a.internal(source), a.internal(destination), options)
	})
	return err
}

// Move moves between prefixed object paths.
func (a *Adapter) Move(ctx context.Context, source, destination filesystem.Path, options filesystem.MoveOptions) error {
	_, err := observeCall(a.observer, filesystem.OperationMove, source, destination, func() (struct{}, error) {
		if a.readOnly {
			return struct{}{}, a.unsupported(filesystem.CapabilityMove, filesystem.OperationMove)
		}
		return struct{}{}, a.backend.Move(ctx, a.internal(source), a.internal(destination), options)
	})
	return err
}

// SetMetadata replaces metadata on a prefixed object.
func (a *Adapter) SetMetadata(ctx context.Context, path filesystem.Path, metadata map[string]string) error {
	_, err := observeCall(a.observer, filesystem.OperationSetMetadata, path, filesystem.Path{}, func() (struct{}, error) {
		if a.readOnly {
			return struct{}{}, a.unsupported(filesystem.CapabilityMetadata, filesystem.OperationSetMetadata)
		}
		return struct{}{}, a.backend.SetMetadata(ctx, a.internal(path), metadata)
	})
	return err
}

// Checksum returns a checksum for a prefixed object.
func (a *Adapter) Checksum(ctx context.Context, path filesystem.Path, algorithm filesystem.ChecksumAlgorithm) (filesystem.Checksum, error) {
	return observeCall(a.observer, filesystem.OperationChecksum, path, filesystem.Path{}, func() (filesystem.Checksum, error) {
		return retryCall(ctx, a.retry, func() (filesystem.Checksum, error) {
			if !a.checksums {
				return a.backend.Checksum(ctx, a.internal(path), algorithm)
			}
			var digest hash.Hash
			switch algorithm {
			case filesystem.ChecksumMD5:
				digest = md5.New()
			case filesystem.ChecksumSHA256:
				digest = sha256.New()
			case filesystem.ChecksumCRC32C:
				digest = crc32.New(crc32.MakeTable(crc32.Castagnoli))
			default:
				return filesystem.Checksum{}, filesystem.Unsupported(
					"decorator/checksum",
					filesystem.CapabilityChecksum,
					filesystem.OperationChecksum,
				)
			}
			stream, err := a.backend.Open(ctx, a.internal(path))
			if err != nil {
				return filesystem.Checksum{}, err
			}
			if _, err := io.Copy(digest, stream); err != nil {
				_ = stream.Close()
				return filesystem.Checksum{}, err
			}
			if err := stream.Close(); err != nil {
				return filesystem.Checksum{}, err
			}
			return filesystem.Checksum{
				Algorithm: algorithm,
				Value:     hex.EncodeToString(digest.Sum(nil)),
			}, nil
		})
	})
}

// TemporaryURL creates a URL for a prefixed object when the backend supports
// URL signing.
func (a *Adapter) TemporaryURL(ctx context.Context, path filesystem.Path, lifetime time.Duration, options filesystem.TemporaryURLOptions) (string, error) {
	return observeCall(a.observer, filesystem.OperationTemporaryURL, path, filesystem.Path{}, func() (string, error) {
		signer, supported := a.backend.(filesystem.TemporaryURLer)
		if !supported {
			return "", filesystem.Unsupported("decorator", filesystem.CapabilityTemporaryURL, filesystem.OperationTemporaryURL)
		}
		return retryCall(ctx, a.retry, func() (string, error) {
			return signer.TemporaryURL(ctx, a.internal(path), lifetime, options)
		})
	})
}

// Visibility returns visibility for a prefixed object.
func (a *Adapter) Visibility(ctx context.Context, path filesystem.Path) (filesystem.Visibility, error) {
	return observeCall(a.observer, filesystem.OperationVisibility, path, filesystem.Path{}, func() (filesystem.Visibility, error) {
		if a.readOnly {
			return "", a.unsupported(filesystem.CapabilityVisibility, filesystem.OperationVisibility)
		}
		return retryCall(ctx, a.retry, func() (filesystem.Visibility, error) {
			return a.backend.Visibility(ctx, a.internal(path))
		})
	})
}

// SetVisibility changes visibility for a prefixed object.
func (a *Adapter) SetVisibility(ctx context.Context, path filesystem.Path, visibility filesystem.Visibility) error {
	_, err := observeCall(a.observer, filesystem.OperationSetVisibility, path, filesystem.Path{}, func() (struct{}, error) {
		if a.readOnly {
			return struct{}{}, a.unsupported(filesystem.CapabilityVisibility, filesystem.OperationSetVisibility)
		}
		return struct{}{}, a.backend.SetVisibility(ctx, a.internal(path), visibility)
	})
	return err
}

func (a *Adapter) unsupported(capability filesystem.Capability, operation filesystem.Operation) error {
	return filesystem.Unsupported("decorator/read-only", capability, operation)
}

func (a *Adapter) internal(path filesystem.Path) filesystem.Path {
	if a.prefix.IsRoot() {
		return path
	}
	if path.IsRoot() {
		return a.prefix
	}
	return filesystem.MustParsePath(a.prefix.String() + "/" + path.String())
}

type iterator struct {
	inner   filesystem.EntryIterator
	prefix  filesystem.Path
	current filesystem.Entry
	err     error
}

func (i *iterator) Next() bool {
	if i.err != nil || !i.inner.Next() {
		return false
	}
	entry := i.inner.Entry()
	path, err := externalPath(i.prefix, entry.Path)
	if err != nil {
		i.err = err
		return false
	}
	entry.Path = path
	i.current = entry
	return true
}

func (i *iterator) Entry() filesystem.Entry { return i.current }

func (i *iterator) Err() error {
	if i.err != nil {
		return i.err
	}
	return i.inner.Err()
}

func (i *iterator) Close() error { return i.inner.Close() }

func externalPath(prefix, path filesystem.Path) (filesystem.Path, error) {
	if prefix.IsRoot() {
		return path, nil
	}
	if path == prefix {
		return filesystem.Root(), nil
	}
	relative, found := strings.CutPrefix(path.String(), prefix.String()+"/")
	if !found {
		return filesystem.Path{}, fmt.Errorf("%w: backend listing escaped decorator prefix", filesystem.ErrInvalidPath)
	}
	return filesystem.ParsePath(relative)
}

func retryCall[T any](ctx context.Context, policy *RetryPolicy, call func() (T, error)) (T, error) {
	if policy == nil {
		return call()
	}
	var zero T
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		result, err := call()
		if err == nil || attempt == policy.Attempts ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			!policy.Retryable(err) {
			return result, err
		}
		var delay time.Duration
		if policy.Backoff != nil {
			delay = policy.Backoff(attempt)
		}
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			_ = timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}

func observeCall[T any](
	observer Observer,
	operation filesystem.Operation,
	path filesystem.Path,
	destination filesystem.Path,
	call func() (T, error),
) (T, error) {
	started := time.Now()
	result, err := call()
	if observer != nil {
		observer(Event{
			Operation:   operation,
			Path:        path,
			Destination: destination,
			Started:     started,
			Duration:    time.Since(started),
			Error:       err,
		})
	}
	return result, err
}
