// Package memory provides a deterministic, concurrency-safe in-memory
// filesystem adapter intended for tests and ephemeral workloads.
package memory

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/internal/streamwriter"
)

var capabilities = filesystem.NewCapabilitySet(
	filesystem.CapabilityRead,
	filesystem.CapabilityWrite,
	filesystem.CapabilityStreamingWrite,
	filesystem.CapabilityDelete,
	filesystem.CapabilityList,
	filesystem.CapabilityStat,
	filesystem.CapabilityCopy,
	filesystem.CapabilityMove,
	filesystem.CapabilityRangeRead,
	filesystem.CapabilityMetadata,
	filesystem.CapabilityChecksum,
	filesystem.CapabilityVisibility,
)

// Option configures an Adapter.
type Option func(*Adapter)

// WithClock replaces the clock used for modification times.
func WithClock(clock func() time.Time) Option {
	return func(adapter *Adapter) {
		if clock != nil {
			adapter.clock = clock
		}
	}
}

// Adapter stores objects in memory and is safe for concurrent use.
type Adapter struct {
	mu      sync.RWMutex
	objects map[string]object
	clock   func() time.Time
}

type object struct {
	content     []byte
	metadata    map[string]string
	contentType string
	visibility  filesystem.Visibility
	modified    time.Time
}

// New constructs an empty adapter.
func New(options ...Option) *Adapter {
	adapter := &Adapter{
		objects: make(map[string]object),
		clock:   func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(adapter)
	}

	return adapter
}

// Capabilities reports the adapter's explicitly supported operations.
func (a *Adapter) Capabilities() filesystem.CapabilitySet {
	return capabilities
}

// Open returns an independent reader over an immutable snapshot.
func (a *Adapter) Open(ctx context.Context, path filesystem.Path) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.RLock()
	stored, found := a.objects[path.String()]
	a.mu.RUnlock()
	if !found {
		return nil, notFound(path)
	}

	return io.NopCloser(bytes.NewReader(append([]byte(nil), stored.content...))), nil
}

// OpenRange returns an independent reader over a bounded object snapshot.
func (a *Adapter) OpenRange(ctx context.Context, path filesystem.Path, byteRange filesystem.ByteRange) (io.ReadCloser, error) {
	if byteRange.Offset < 0 || byteRange.Length <= 0 {
		return nil, fmt.Errorf("%w: offset=%d length=%d", filesystem.ErrInvalidRange, byteRange.Offset, byteRange.Length)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.RLock()
	stored, found := a.objects[path.String()]
	a.mu.RUnlock()
	if !found {
		return nil, notFound(path)
	}
	content := stored.content
	if byteRange.Offset >= int64(len(content)) {
		return nil, fmt.Errorf("%w: offset=%d size=%d", filesystem.ErrInvalidRange, byteRange.Offset, len(content))
	}
	end := byteRange.Offset + byteRange.Length
	if end < byteRange.Offset || end > int64(len(content)) {
		end = int64(len(content))
	}

	return io.NopCloser(bytes.NewReader(content[byteRange.Offset:end])), nil
}

// Write consumes source before atomically publishing the object.
func (a *Adapter) Write(ctx context.Context, path filesystem.Path, source io.Reader, options filesystem.WriteOptions) (filesystem.Metadata, error) {
	if path.IsRoot() {
		return filesystem.Metadata{}, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
	}
	visibility, err := normalizeVisibility(options.Visibility)
	if err != nil {
		return filesystem.Metadata{}, err
	}
	var content bytes.Buffer
	if _, err := io.Copy(&content, contextReader{ctx: ctx, reader: source}); err != nil {
		return filesystem.Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return filesystem.Metadata{}, err
	}

	now := a.clock()
	stored := object{
		content:     append([]byte(nil), content.Bytes()...),
		metadata:    cloneMap(options.Metadata),
		contentType: options.ContentType,
		visibility:  visibility,
		modified:    now,
	}
	a.mu.Lock()
	if _, exists := a.objects[path.String()]; exists && options.IfNoneMatch {
		a.mu.Unlock()
		return filesystem.Metadata{}, fmt.Errorf("%w: %s", filesystem.ErrPreconditionFailed, path)
	}
	a.objects[path.String()] = stored
	a.mu.Unlock()

	return metadataFor(path, stored), nil
}

// OpenWriter returns a streaming writer that publishes through Write on
// Close. Abandoning it without closing leaves the publication incomplete.
func (a *Adapter) OpenWriter(ctx context.Context, path filesystem.Path, options filesystem.WriteOptions) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path.IsRoot() {
		return nil, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
	}
	return streamwriter.New(func(source io.Reader) error {
		_, err := a.Write(ctx, path, source, options)
		return err
	}), nil
}

// Delete removes an object.
func (a *Adapter) Delete(ctx context.Context, path filesystem.Path) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, found := a.objects[path.String()]; !found {
		return notFound(path)
	}
	delete(a.objects, path.String())
	return nil
}

// Stat returns a defensive metadata copy.
func (a *Adapter) Stat(ctx context.Context, path filesystem.Path) (filesystem.Metadata, error) {
	if err := ctx.Err(); err != nil {
		return filesystem.Metadata{}, err
	}
	a.mu.RLock()
	stored, found := a.objects[path.String()]
	a.mu.RUnlock()
	if !found {
		return filesystem.Metadata{}, notFound(path)
	}
	return metadataFor(path, stored), nil
}

// List returns a deterministic snapshot iterator.
func (a *Adapter) List(ctx context.Context, directory filesystem.Path, options filesystem.ListOptions) (filesystem.EntryIterator, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Limit < 0 {
		return nil, fmt.Errorf("filesystem: list limit must not be negative")
	}
	prefix := directory.String()
	if prefix != "" {
		prefix += "/"
	}

	a.mu.RLock()
	entriesByPath := make(map[string]filesystem.Entry)
	for name, stored := range a.objects {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(name, prefix)
		if !options.Recursive {
			if separator := strings.IndexByte(remainder, '/'); separator >= 0 {
				name = prefix + remainder[:separator]
				entriesByPath[name] = filesystem.Entry{Path: filesystem.MustParsePath(name), Kind: filesystem.EntryKindDirectory}
				continue
			}
		}
		entriesByPath[name] = filesystem.Entry{
			Path:     filesystem.MustParsePath(name),
			Kind:     filesystem.EntryKindFile,
			Size:     int64(len(stored.content)),
			Modified: stored.modified,
		}
	}
	a.mu.RUnlock()

	entries := make([]filesystem.Entry, 0, len(entriesByPath))
	for _, entry := range entriesByPath {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Path.String() < entries[right].Path.String()
	})
	if options.Limit > 0 && len(entries) > options.Limit {
		entries = entries[:options.Limit]
	}
	return &iterator{entries: entries, index: -1}, nil
}

// Copy duplicates an object and its properties.
func (a *Adapter) Copy(ctx context.Context, source, destination filesystem.Path, options filesystem.CopyOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, found := a.objects[source.String()]
	if !found {
		return notFound(source)
	}
	if _, exists := a.objects[destination.String()]; exists && !options.Overwrite {
		return fmt.Errorf("%w: %s", filesystem.ErrAlreadyExists, destination)
	}
	stored.content = append([]byte(nil), stored.content...)
	stored.metadata = cloneMap(stored.metadata)
	stored.modified = a.clock()
	a.objects[destination.String()] = stored
	return nil
}

// Move atomically renames an object within this adapter.
func (a *Adapter) Move(ctx context.Context, source, destination filesystem.Path, options filesystem.MoveOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, found := a.objects[source.String()]
	if !found {
		return notFound(source)
	}
	if _, exists := a.objects[destination.String()]; exists && !options.Overwrite {
		return fmt.Errorf("%w: %s", filesystem.ErrAlreadyExists, destination)
	}
	stored.modified = a.clock()
	a.objects[destination.String()] = stored
	delete(a.objects, source.String())
	return nil
}

// SetMetadata replaces user metadata.
func (a *Adapter) SetMetadata(ctx context.Context, path filesystem.Path, metadata map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, found := a.objects[path.String()]
	if !found {
		return notFound(path)
	}
	stored.metadata = cloneMap(metadata)
	stored.modified = a.clock()
	a.objects[path.String()] = stored
	return nil
}

// Checksum computes an explicitly requested digest.
func (a *Adapter) Checksum(ctx context.Context, path filesystem.Path, algorithm filesystem.ChecksumAlgorithm) (filesystem.Checksum, error) {
	if err := ctx.Err(); err != nil {
		return filesystem.Checksum{}, err
	}
	a.mu.RLock()
	stored, found := a.objects[path.String()]
	a.mu.RUnlock()
	if !found {
		return filesystem.Checksum{}, notFound(path)
	}
	var value string
	switch algorithm {
	case filesystem.ChecksumMD5:
		digest := md5.Sum(stored.content)
		value = hex.EncodeToString(digest[:])
	case filesystem.ChecksumSHA256:
		digest := sha256.Sum256(stored.content)
		value = hex.EncodeToString(digest[:])
	case filesystem.ChecksumCRC32C:
		digest := crc32.Checksum(stored.content, crc32.MakeTable(crc32.Castagnoli))
		value = fmt.Sprintf("%08x", digest)
	default:
		return filesystem.Checksum{}, filesystem.Unsupported("memory", filesystem.CapabilityChecksum, filesystem.OperationChecksum)
	}
	return filesystem.Checksum{Algorithm: algorithm, Value: value}, nil
}

// Visibility retrieves an object's coarse visibility.
func (a *Adapter) Visibility(ctx context.Context, path filesystem.Path) (filesystem.Visibility, error) {
	metadata, err := a.Stat(ctx, path)
	return metadata.Visibility, err
}

// SetVisibility changes an object's coarse visibility.
func (a *Adapter) SetVisibility(ctx context.Context, path filesystem.Path, visibility filesystem.Visibility) error {
	visibility, err := normalizeVisibility(visibility)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, found := a.objects[path.String()]
	if !found {
		return notFound(path)
	}
	stored.visibility = visibility
	stored.modified = a.clock()
	a.objects[path.String()] = stored
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

type iterator struct {
	entries []filesystem.Entry
	index   int
	closed  bool
}

func (i *iterator) Next() bool {
	if i.closed || i.index+1 >= len(i.entries) {
		return false
	}
	i.index++
	return true
}

func (i *iterator) Entry() filesystem.Entry {
	if i.index < 0 || i.index >= len(i.entries) {
		return filesystem.Entry{}
	}
	return i.entries[i.index]
}

func (i *iterator) Err() error { return nil }

func (i *iterator) Close() error {
	i.closed = true
	return nil
}

func normalizeVisibility(visibility filesystem.Visibility) (filesystem.Visibility, error) {
	switch visibility {
	case "", filesystem.VisibilityPrivate:
		return filesystem.VisibilityPrivate, nil
	case filesystem.VisibilityPublic:
		return visibility, nil
	default:
		return "", fmt.Errorf("filesystem: invalid visibility %q", visibility)
	}
}

func metadataFor(path filesystem.Path, stored object) filesystem.Metadata {
	return filesystem.Metadata{
		Path:         path,
		Kind:         filesystem.EntryKindFile,
		Size:         int64(len(stored.content)),
		Modified:     stored.modified,
		ContentType:  stored.contentType,
		UserMetadata: cloneMap(stored.metadata),
		Visibility:   stored.visibility,
	}
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func notFound(path filesystem.Path) error {
	return fmt.Errorf("%w: %s", filesystem.ErrNotFound, path)
}
