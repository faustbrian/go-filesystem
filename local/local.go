// Package local provides a filesystem adapter contained within a local
// directory root.
package local

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

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
	filesystem.CapabilityChecksum,
)

// SymlinkPolicy controls whether logical paths may traverse symbolic links.
type SymlinkPolicy uint8

const (
	// DenySymlinks rejects any symbolic-link path component.
	DenySymlinks SymlinkPolicy = iota
	// AllowInternalSymlinks permits links that remain beneath the opened root.
	AllowInternalSymlinks
)

// Option configures an Adapter.
type Option func(*config) error

type config struct {
	fileMode      fs.FileMode
	directoryMode fs.FileMode
	symlinkPolicy SymlinkPolicy
}

type localFile interface {
	io.Reader
	io.Writer
	io.Seeker
	io.Closer
	Stat() (fs.FileInfo, error)
	Sync() error
}

type rootFS interface {
	Open(string) (localFile, error)
	OpenFile(string, int, fs.FileMode) (localFile, error)
	Stat(string) (fs.FileInfo, error)
	Lstat(string) (fs.FileInfo, error)
	MkdirAll(string, fs.FileMode) error
	Remove(string) error
	Rename(string, string) error
	FS() fs.FS
	Close() error
}

type system interface {
	MkdirAll(string, fs.FileMode) error
	OpenRoot(string) (rootFS, error)
}

// WithFileMode sets the mode used for newly written files.
func WithFileMode(mode fs.FileMode) Option {
	return func(config *config) error {
		if mode.Perm() == 0 {
			return errors.New("local: file mode has no permission bits")
		}
		config.fileMode = mode.Perm()
		return nil
	}
}

// WithDirectoryMode sets the mode used for newly created directories.
func WithDirectoryMode(mode fs.FileMode) Option {
	return func(config *config) error {
		if mode.Perm() == 0 {
			return errors.New("local: directory mode has no permission bits")
		}
		config.directoryMode = mode.Perm()
		return nil
	}
}

// WithSymlinkPolicy sets the explicit symbolic-link policy.
func WithSymlinkPolicy(policy SymlinkPolicy) Option {
	return func(config *config) error {
		if policy != DenySymlinks && policy != AllowInternalSymlinks {
			return fmt.Errorf("local: invalid symlink policy %d", policy)
		}
		config.symlinkPolicy = policy
		return nil
	}
}

// Adapter accesses files through an os.Root, which prevents path traversal and
// symlink escape even when directory entries change concurrently.
type Adapter struct {
	root          rootFS
	random        io.Reader
	fileMode      fs.FileMode
	directoryMode fs.FileMode
	symlinkPolicy SymlinkPolicy
}

// New opens root and creates it when absent.
func New(root string, options ...Option) (*Adapter, error) {
	return newAdapter(root, osSystem{}, options...)
}

func newAdapter(root string, system system, options ...Option) (*Adapter, error) {
	configuration := config{
		fileMode:      0o600,
		directoryMode: 0o700,
		symlinkPolicy: DenySymlinks,
	}
	for _, option := range options {
		if err := option(&configuration); err != nil {
			return nil, err
		}
	}
	if err := system.MkdirAll(root, configuration.directoryMode); err != nil {
		return nil, fmt.Errorf("local: create root: %w", err)
	}
	opened, err := system.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("local: open root: %w", err)
	}
	return &Adapter{
		root:          opened,
		random:        rand.Reader,
		fileMode:      configuration.fileMode,
		directoryMode: configuration.directoryMode,
		symlinkPolicy: configuration.symlinkPolicy,
	}, nil
}

// Close releases the root directory handle.
func (a *Adapter) Close() error {
	return a.root.Close()
}

// Capabilities reports the adapter's explicitly supported operations.
func (a *Adapter) Capabilities() filesystem.CapabilitySet {
	return capabilities
}

// Open opens a file stream beneath the configured root.
func (a *Adapter) Open(ctx context.Context, logicalPath filesystem.Path) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.checkSymlinks(logicalPath, true); err != nil {
		return nil, err
	}
	file, err := a.root.Open(logicalPath.String())
	if err != nil {
		return nil, mapError(logicalPath, err)
	}
	return &contextReadCloser{ctx: ctx, file: file}, nil
}

// OpenRange opens a bounded file stream beneath the configured root.
func (a *Adapter) OpenRange(ctx context.Context, logicalPath filesystem.Path, byteRange filesystem.ByteRange) (io.ReadCloser, error) {
	if byteRange.Offset < 0 || byteRange.Length <= 0 {
		return nil, fmt.Errorf("%w: offset=%d length=%d", filesystem.ErrInvalidRange, byteRange.Offset, byteRange.Length)
	}
	stream, err := a.Open(ctx, logicalPath)
	if err != nil {
		return nil, err
	}
	file := stream.(*contextReadCloser).file
	info, err := file.Stat()
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if byteRange.Offset >= info.Size() {
		_ = stream.Close()
		return nil, fmt.Errorf("%w: offset=%d size=%d", filesystem.ErrInvalidRange, byteRange.Offset, info.Size())
	}
	if _, err := file.Seek(byteRange.Offset, io.SeekStart); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &limitedReadCloser{Reader: io.LimitReader(stream, byteRange.Length), closer: stream}, nil
}

// Write atomically replaces a file after the source stream is fully persisted.
func (a *Adapter) Write(ctx context.Context, logicalPath filesystem.Path, source io.Reader, options filesystem.WriteOptions) (filesystem.Metadata, error) {
	if logicalPath.IsRoot() {
		return filesystem.Metadata{}, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
	}
	if err := ctx.Err(); err != nil {
		return filesystem.Metadata{}, err
	}
	if err := a.checkSymlinks(logicalPath, false); err != nil {
		return filesystem.Metadata{}, err
	}
	if options.IfNoneMatch {
		if _, err := a.root.Stat(logicalPath.String()); err == nil {
			return filesystem.Metadata{}, fmt.Errorf("%w: %s", filesystem.ErrPreconditionFailed, logicalPath)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return filesystem.Metadata{}, mapError(logicalPath, err)
		}
	}
	directory := path.Dir(logicalPath.String())
	if directory != "." {
		if err := a.root.MkdirAll(directory, a.directoryMode); err != nil {
			return filesystem.Metadata{}, fmt.Errorf("local: create parent directories: %w", err)
		}
	}
	temporary, err := a.temporaryName(directory)
	if err != nil {
		return filesystem.Metadata{}, err
	}
	file, err := a.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, a.fileMode)
	if err != nil {
		return filesystem.Metadata{}, fmt.Errorf("local: create temporary file: %w", err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = a.root.Remove(temporary)
		}
	}()
	if _, err := io.Copy(file, contextReader{ctx: ctx, reader: source}); err != nil {
		return filesystem.Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return filesystem.Metadata{}, err
	}
	if err := file.Sync(); err != nil {
		return filesystem.Metadata{}, fmt.Errorf("local: sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return filesystem.Metadata{}, fmt.Errorf("local: close temporary file: %w", err)
	}
	if err := a.root.Rename(temporary, logicalPath.String()); err != nil {
		return filesystem.Metadata{}, fmt.Errorf("local: publish temporary file: %w", err)
	}
	committed = true
	return a.Stat(ctx, logicalPath)
}

// OpenWriter returns a streaming writer that atomically publishes its
// temporary file on Close.
func (a *Adapter) OpenWriter(ctx context.Context, logicalPath filesystem.Path, options filesystem.WriteOptions) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if logicalPath.IsRoot() {
		return nil, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
	}
	return streamwriter.New(func(source io.Reader) error {
		_, err := a.Write(ctx, logicalPath, source, options)
		return err
	}), nil
}

// Delete removes a file.
func (a *Adapter) Delete(ctx context.Context, logicalPath filesystem.Path) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.checkSymlinks(logicalPath, true); err != nil {
		return err
	}
	if err := a.root.Remove(logicalPath.String()); err != nil {
		return mapError(logicalPath, err)
	}
	return nil
}

// Stat retrieves local file metadata.
func (a *Adapter) Stat(ctx context.Context, logicalPath filesystem.Path) (filesystem.Metadata, error) {
	if err := ctx.Err(); err != nil {
		return filesystem.Metadata{}, err
	}
	if err := a.checkSymlinks(logicalPath, true); err != nil {
		return filesystem.Metadata{}, err
	}
	name := logicalPath.String()
	if logicalPath.IsRoot() {
		name = "."
	}
	info, err := a.root.Stat(name)
	if err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	kind := filesystem.EntryKindFile
	if info.IsDir() {
		kind = filesystem.EntryKindDirectory
	}
	return filesystem.Metadata{
		Path:     logicalPath,
		Kind:     kind,
		Size:     info.Size(),
		Modified: info.ModTime(),
	}, nil
}

// List walks a directory in deterministic order and stops at the configured
// limit rather than buffering an unbounded tree.
func (a *Adapter) List(ctx context.Context, directory filesystem.Path, options filesystem.ListOptions) (filesystem.EntryIterator, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Limit < 0 {
		return nil, errors.New("local: list limit must not be negative")
	}
	start := directory.String()
	if start == "" {
		start = "."
	}
	if !directory.IsRoot() {
		if err := a.checkSymlinks(directory, true); err != nil {
			return nil, err
		}
	}
	var entries []filesystem.Entry
	stop := errors.New("local: listing limit reached")
	err := fs.WalkDir(a.root.FS(), start, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == start {
			return nil
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(name, start), "/")
		if !options.Recursive && strings.Contains(relative, "/") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 && a.symlinkPolicy == DenySymlinks {
			return fmt.Errorf("local: symbolic link %q is denied", name)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		kind := filesystem.EntryKindFile
		if entry.IsDir() {
			kind = filesystem.EntryKindDirectory
		}
		logical, err := filesystem.ParsePath(name)
		if err != nil {
			return err
		}
		entries = append(entries, filesystem.Entry{Path: logical, Kind: kind, Size: info.Size(), Modified: info.ModTime()})
		if options.Limit > 0 && len(entries) >= options.Limit {
			return stop
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		return nil, mapError(directory, err)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Path.String() < entries[right].Path.String()
	})
	return &iterator{entries: entries, index: -1}, nil
}

// Copy streams a source into an atomically published destination.
func (a *Adapter) Copy(ctx context.Context, source, destination filesystem.Path, options filesystem.CopyOptions) error {
	if !options.Overwrite {
		if _, err := a.root.Stat(destination.String()); err == nil {
			return fmt.Errorf("%w: %s", filesystem.ErrAlreadyExists, destination)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return mapError(destination, err)
		}
	}
	stream, err := a.Open(ctx, source)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	_, err = a.Write(ctx, destination, stream, filesystem.WriteOptions{IfNoneMatch: !options.Overwrite})
	return err
}

// Move renames a file atomically within the local root.
func (a *Adapter) Move(ctx context.Context, source, destination filesystem.Path, options filesystem.MoveOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.checkSymlinks(source, true); err != nil {
		return err
	}
	if err := a.checkSymlinks(destination, false); err != nil {
		return err
	}
	if !options.Overwrite {
		if _, err := a.root.Stat(destination.String()); err == nil {
			return fmt.Errorf("%w: %s", filesystem.ErrAlreadyExists, destination)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return mapError(destination, err)
		}
	}
	if directory := path.Dir(destination.String()); directory != "." {
		if err := a.root.MkdirAll(directory, a.directoryMode); err != nil {
			return err
		}
	}
	if err := a.root.Rename(source.String(), destination.String()); err != nil {
		return mapError(source, err)
	}
	return nil
}

// SetMetadata returns a typed error because portable local user metadata is
// not supported.
func (a *Adapter) SetMetadata(context.Context, filesystem.Path, map[string]string) error {
	return filesystem.Unsupported("local", filesystem.CapabilityMetadata, filesystem.OperationSetMetadata)
}

// Checksum streams a local file through an explicitly selected digest.
func (a *Adapter) Checksum(ctx context.Context, logicalPath filesystem.Path, algorithm filesystem.ChecksumAlgorithm) (filesystem.Checksum, error) {
	stream, err := a.Open(ctx, logicalPath)
	if err != nil {
		return filesystem.Checksum{}, err
	}
	defer func() { _ = stream.Close() }()
	var value string
	switch algorithm {
	case filesystem.ChecksumMD5:
		digest := md5.New()
		if _, err := io.Copy(digest, stream); err != nil {
			return filesystem.Checksum{}, err
		}
		value = hex.EncodeToString(digest.Sum(nil))
	case filesystem.ChecksumSHA256:
		digest := sha256.New()
		if _, err := io.Copy(digest, stream); err != nil {
			return filesystem.Checksum{}, err
		}
		value = hex.EncodeToString(digest.Sum(nil))
	case filesystem.ChecksumCRC32C:
		digest := crc32.New(crc32.MakeTable(crc32.Castagnoli))
		if _, err := io.Copy(digest, stream); err != nil {
			return filesystem.Checksum{}, err
		}
		value = fmt.Sprintf("%08x", digest.Sum32())
	default:
		return filesystem.Checksum{}, filesystem.Unsupported("local", filesystem.CapabilityChecksum, filesystem.OperationChecksum)
	}
	return filesystem.Checksum{Algorithm: algorithm, Value: value}, nil
}

// Visibility returns a typed unsupported error.
func (a *Adapter) Visibility(context.Context, filesystem.Path) (filesystem.Visibility, error) {
	return "", filesystem.Unsupported("local", filesystem.CapabilityVisibility, filesystem.OperationVisibility)
}

// SetVisibility returns a typed unsupported error.
func (a *Adapter) SetVisibility(context.Context, filesystem.Path, filesystem.Visibility) error {
	return filesystem.Unsupported("local", filesystem.CapabilityVisibility, filesystem.OperationSetVisibility)
}

func (a *Adapter) checkSymlinks(logicalPath filesystem.Path, includeFinal bool) error {
	if logicalPath.IsRoot() || a.symlinkPolicy == AllowInternalSymlinks {
		return nil
	}
	segments := strings.Split(logicalPath.String(), "/")
	if !includeFinal && len(segments) > 0 {
		segments = segments[:len(segments)-1]
	}
	for index := range segments {
		name := strings.Join(segments[:index+1], "/")
		info, err := a.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return mapError(logicalPath, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("local: symbolic link %q is denied", name)
		}
	}
	return nil
}

func (a *Adapter) temporaryName(directory string) (string, error) {
	var random [16]byte
	if _, err := io.ReadFull(a.random, random[:]); err != nil {
		return "", fmt.Errorf("local: generate temporary name: %w", err)
	}
	name := ".filesystem-" + hex.EncodeToString(random[:]) + ".tmp"
	if directory == "." {
		return name, nil
	}
	return directory + "/" + name, nil
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

type contextReadCloser struct {
	ctx  context.Context
	file localFile
}

func (r *contextReadCloser) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(buffer)
}

func (r *contextReadCloser) Close() error { return r.file.Close() }

type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *limitedReadCloser) Close() error { return r.closer.Close() }

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

func mapError(logicalPath filesystem.Path, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", filesystem.ErrNotFound, logicalPath)
	}
	return err
}

type osSystem struct{}

func (osSystem) MkdirAll(name string, mode fs.FileMode) error {
	return os.MkdirAll(name, mode)
}

func (osSystem) OpenRoot(name string) (rootFS, error) {
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return osRoot{root: root}, nil
}

type osRoot struct{ root *os.Root }

func (r osRoot) Open(name string) (localFile, error) {
	return r.root.Open(name)
}

func (r osRoot) OpenFile(name string, flag int, mode fs.FileMode) (localFile, error) {
	return r.root.OpenFile(name, flag, mode)
}

func (r osRoot) Stat(name string) (fs.FileInfo, error)  { return r.root.Stat(name) }
func (r osRoot) Lstat(name string) (fs.FileInfo, error) { return r.root.Lstat(name) }
func (r osRoot) MkdirAll(name string, mode fs.FileMode) error {
	return r.root.MkdirAll(name, mode)
}
func (r osRoot) Remove(name string) error             { return r.root.Remove(name) }
func (r osRoot) Rename(oldName, newName string) error { return r.root.Rename(oldName, newName) }
func (r osRoot) FS() fs.FS                            { return r.root.FS() }
func (r osRoot) Close() error                         { return r.root.Close() }
