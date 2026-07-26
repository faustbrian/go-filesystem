package filesystem

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"syscall"
	"time"
)

// IOFS adapts read, stat, and list capabilities to Go's read-only io/fs
// contracts. Logical directories may be synthesized from object prefixes.
type IOFS struct {
	reader  Reader
	statter Statter
	lister  Lister
}

// NewIOFS constructs a read-only io/fs bridge. All three capabilities are
// required because io/fs directories must support Stat and ReadDir.
func NewIOFS(reader Reader, statter Statter, lister Lister) *IOFS {
	if reader == nil || statter == nil || lister == nil {
		panic("filesystem: NewIOFS requires reader, statter, and lister")
	}
	return &IOFS{reader: reader, statter: statter, lister: lister}
}

// Open implements fs.FS.
func (f *IOFS) Open(name string) (fs.File, error) {
	logicalPath, err := ioFSPath(name)
	if err != nil {
		return nil, pathError("open", name, err)
	}
	metadata, err := f.metadata(context.Background(), logicalPath)
	if err != nil {
		return nil, pathError("open", name, err)
	}
	if metadata.Kind == EntryKindDirectory {
		entries, err := f.directoryEntries(context.Background(), logicalPath)
		if err != nil {
			return nil, pathError("open", name, err)
		}
		return &ioFSDirectory{
			name:    name,
			info:    ioFSInfo{metadata: metadata, name: ioFSBase(name)},
			entries: entries,
		}, nil
	}
	stream, err := f.reader.Open(context.Background(), logicalPath)
	if err != nil {
		return nil, pathError("open", name, err)
	}
	return &ioFSFile{
		stream: stream,
		info:   ioFSInfo{metadata: metadata, name: ioFSBase(name)},
	}, nil
}

// Stat implements fs.StatFS.
func (f *IOFS) Stat(name string) (fs.FileInfo, error) {
	logicalPath, err := ioFSPath(name)
	if err != nil {
		return nil, pathError("stat", name, err)
	}
	metadata, err := f.metadata(context.Background(), logicalPath)
	if err != nil {
		return nil, pathError("stat", name, err)
	}
	return ioFSInfo{metadata: metadata, name: ioFSBase(name)}, nil
}

// ReadDir implements fs.ReadDirFS.
func (f *IOFS) ReadDir(name string) ([]fs.DirEntry, error) {
	logicalPath, err := ioFSPath(name)
	if err != nil {
		return nil, pathError("readdir", name, err)
	}
	metadata, err := f.metadata(context.Background(), logicalPath)
	if err != nil {
		return nil, pathError("readdir", name, err)
	}
	if metadata.Kind != EntryKindDirectory {
		return nil, pathError("readdir", name, syscall.ENOTDIR)
	}
	entries, err := f.directoryEntries(context.Background(), logicalPath)
	if err != nil {
		return nil, pathError("readdir", name, err)
	}
	return entries, nil
}

func (f *IOFS) metadata(ctx context.Context, logicalPath Path) (Metadata, error) {
	if logicalPath.IsRoot() {
		return Metadata{Path: Root(), Kind: EntryKindDirectory}, nil
	}
	metadata, err := f.statter.Stat(ctx, logicalPath)
	if err == nil {
		return metadata, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Metadata{}, err
	}
	iterator, listErr := f.lister.List(ctx, logicalPath, ListOptions{Limit: 1})
	if listErr != nil {
		return Metadata{}, listErr
	}
	hasEntry := iterator.Next()
	iteratorErr := iterator.Err()
	closeErr := iterator.Close()
	if iteratorErr != nil || closeErr != nil {
		return Metadata{}, errors.Join(iteratorErr, closeErr)
	}
	if hasEntry {
		return Metadata{Path: logicalPath, Kind: EntryKindDirectory}, nil
	}
	return Metadata{}, err
}

func (f *IOFS) directoryEntries(ctx context.Context, logicalPath Path) ([]fs.DirEntry, error) {
	iterator, err := f.lister.List(ctx, logicalPath, ListOptions{})
	if err != nil {
		return nil, err
	}
	var entries []fs.DirEntry
	for iterator.Next() {
		entry := iterator.Entry()
		metadata := Metadata{
			Path:     entry.Path,
			Kind:     entry.Kind,
			Size:     entry.Size,
			Modified: entry.Modified,
		}
		entries = append(entries, ioFSDirEntry{
			info: ioFSInfo{metadata: metadata, name: entry.Path.Base()},
		})
	}
	iteratorErr := iterator.Err()
	closeErr := iterator.Close()
	if iteratorErr != nil || closeErr != nil {
		return nil, errors.Join(iteratorErr, closeErr)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	return entries, nil
}

type ioFSFile struct {
	stream io.ReadCloser
	info   ioFSInfo
}

func (f *ioFSFile) Read(buffer []byte) (int, error) { return f.stream.Read(buffer) }
func (f *ioFSFile) Close() error                    { return f.stream.Close() }
func (f *ioFSFile) Stat() (fs.FileInfo, error)      { return f.info, nil }

type ioFSDirectory struct {
	name    string
	info    ioFSInfo
	entries []fs.DirEntry
	offset  int
	closed  bool
}

func (d *ioFSDirectory) Read([]byte) (int, error) {
	return 0, pathError("read", d.name, syscall.EISDIR)
}

func (d *ioFSDirectory) Close() error {
	d.closed = true
	return nil
}

func (d *ioFSDirectory) Stat() (fs.FileInfo, error) {
	if d.closed {
		return nil, pathError("stat", d.name, fs.ErrClosed)
	}
	return d.info, nil
}

func (d *ioFSDirectory) ReadDir(count int) ([]fs.DirEntry, error) {
	if d.closed {
		return nil, pathError("readdir", d.name, fs.ErrClosed)
	}
	if count <= 0 {
		result := append([]fs.DirEntry(nil), d.entries[d.offset:]...)
		d.offset = len(d.entries)
		return result, nil
	}
	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := min(d.offset+count, len(d.entries))
	result := append([]fs.DirEntry(nil), d.entries[d.offset:end]...)
	d.offset = end
	return result, nil
}

type ioFSInfo struct {
	metadata Metadata
	name     string
}

func (i ioFSInfo) Name() string       { return i.name }
func (i ioFSInfo) Size() int64        { return i.metadata.Size }
func (i ioFSInfo) ModTime() time.Time { return i.metadata.Modified }
func (i ioFSInfo) IsDir() bool        { return i.metadata.Kind == EntryKindDirectory }
func (i ioFSInfo) Sys() any           { return nil }
func (i ioFSInfo) Mode() fs.FileMode {
	if i.IsDir() {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

type ioFSDirEntry struct {
	info ioFSInfo
}

func (e ioFSDirEntry) Name() string               { return e.info.Name() }
func (e ioFSDirEntry) IsDir() bool                { return e.info.IsDir() }
func (e ioFSDirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e ioFSDirEntry) Info() (fs.FileInfo, error) { return e.info, nil }

func ioFSPath(name string) (Path, error) {
	if !fs.ValidPath(name) || strings.Contains(name, `\`) {
		return Path{}, fs.ErrInvalid
	}
	if name == "." {
		return Root(), nil
	}
	logicalPath, err := ParsePath(name)
	if err != nil {
		return Path{}, fs.ErrInvalid
	}
	return logicalPath, nil
}

func ioFSBase(name string) string {
	if name == "." {
		return "."
	}
	return MustParsePath(name).Base()
}

func pathError(operation, name string, err error) error {
	if errors.Is(err, ErrNotFound) {
		err = fs.ErrNotExist
	} else if errors.Is(err, ErrInvalidPath) {
		err = fs.ErrInvalid
	}
	return &fs.PathError{Op: operation, Path: name, Err: err}
}

var (
	_ fs.FS          = (*IOFS)(nil)
	_ fs.StatFS      = (*IOFS)(nil)
	_ fs.ReadDirFS   = (*IOFS)(nil)
	_ fs.ReadDirFile = (*ioFSDirectory)(nil)
)
