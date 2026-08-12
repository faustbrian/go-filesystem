// Package sftp provides a capability-based SFTP adapter using pkg/sftp and
// golang.org/x/crypto/ssh.
package sftp

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
	"net"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/internal/streamwriter"
	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const posixRenameExtension = "posix-rename@openssh.com"

// Config contains SSH connection and remote-root settings.
type Config struct {
	// Address is the SSH server host and port.
	Address string
	// User is the SSH username.
	User string
	// Auth contains one or more SSH authentication methods.
	Auth []ssh.AuthMethod
	// HostKeyCallback verifies the server host key and is mandatory.
	HostKeyCallback ssh.HostKeyCallback
	// Root is the absolute remote directory containing all logical paths.
	Root string
	// Timeout bounds dialing and SSH setup; zero selects 30 seconds.
	Timeout time.Duration
	// MaxListEntries bounds one listing; zero selects 10,000.
	MaxListEntries int
}

type remoteFile interface {
	io.Reader
	io.Writer
	io.Closer
	io.Seeker
	Stat() (fs.FileInfo, error)
	Sync() error
}

type remoteSession interface {
	Open(string) (remoteFile, error)
	OpenFile(string, int) (remoteFile, error)
	Stat(string) (fs.FileInfo, error)
	Lstat(string) (fs.FileInfo, error)
	ReadDirContext(context.Context, string) ([]fs.FileInfo, error)
	Remove(string) error
	MkdirAll(string) error
	Rename(string, string) error
	PosixRename(string, string) error
	HasExtension(string) (string, bool)
	Close() error
}

type connector func(context.Context) (remoteSession, error)

// Adapter stores files beneath one remote SFTP directory.
type Adapter struct {
	connector    connector
	random       io.Reader
	root         string
	maxList      int
	atomicRename bool
	capabilities filesystem.CapabilitySet

	mu      sync.Mutex
	session remoteSession
	closed  bool
}

// New validates SSH security settings and opens the initial SFTP session.
func New(ctx context.Context, configuration Config) (*Adapter, error) {
	if strings.TrimSpace(configuration.Address) == "" {
		return nil, errors.New("sftp: address is required")
	}
	if strings.TrimSpace(configuration.User) == "" {
		return nil, errors.New("sftp: user is required")
	}
	if len(configuration.Auth) == 0 {
		return nil, errors.New("sftp: at least one authentication method is required")
	}
	if configuration.HostKeyCallback == nil {
		return nil, errors.New("sftp: host-key callback is required")
	}
	root, err := validateRoot(configuration.Root)
	if err != nil {
		return nil, err
	}
	if configuration.Timeout < 0 {
		return nil, errors.New("sftp: timeout must not be negative")
	}
	if configuration.MaxListEntries < 0 {
		return nil, errors.New("sftp: maximum list entries must be positive")
	}
	timeout := defaultTimeout(configuration.Timeout)
	maxList := defaultMaxList(configuration.MaxListEntries)

	sshConfiguration := &ssh.ClientConfig{
		User:            configuration.User,
		Auth:            append([]ssh.AuthMethod(nil), configuration.Auth...),
		HostKeyCallback: configuration.HostKeyCallback,
		Timeout:         timeout,
	}
	dial := func(ctx context.Context) (remoteSession, error) {
		connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", configuration.Address)
		if err != nil {
			return nil, fmt.Errorf("sftp: dial SSH server: %w", err)
		}
		clientConnection, channels, requests, err := ssh.NewClientConn(connection, configuration.Address, sshConfiguration)
		if err != nil {
			_ = connection.Close()
			return nil, fmt.Errorf("sftp: establish SSH connection: %w", err)
		}
		sshClient := ssh.NewClient(clientConnection, channels, requests)
		sftpClient, err := pkgsftp.NewClient(sshClient)
		if err != nil {
			_ = sshClient.Close()
			return nil, fmt.Errorf("sftp: start subsystem: %w", err)
		}
		return &realSession{sftp: sftpClient, ssh: sshClient}, nil
	}
	return newAdapter(ctx, dial, root, maxList)
}

func newAdapter(ctx context.Context, dial connector, root string, maxList int) (*Adapter, error) {
	if dial == nil {
		return nil, errors.New("sftp: connector is required")
	}
	root, err := validateRoot(root)
	if err != nil {
		return nil, err
	}
	if maxList <= 0 {
		return nil, errors.New("sftp: maximum list entries must be positive")
	}
	session, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	_, atomicRename := session.HasExtension(posixRenameExtension)
	capabilities := []filesystem.Capability{
		filesystem.CapabilityRead,
		filesystem.CapabilityWrite,
		filesystem.CapabilityStreamingWrite,
		filesystem.CapabilityDelete,
		filesystem.CapabilityList,
		filesystem.CapabilityStat,
		filesystem.CapabilityCopy,
		filesystem.CapabilityRangeRead,
		filesystem.CapabilityChecksum,
	}
	if atomicRename {
		capabilities = append(capabilities, filesystem.CapabilityMove)
	}
	return &Adapter{
		connector:    dial,
		random:       rand.Reader,
		root:         root,
		maxList:      maxList,
		atomicRename: atomicRename,
		capabilities: filesystem.NewCapabilitySet(capabilities...),
		session:      session,
	}, nil
}

// Close releases the current SFTP and SSH connections.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	if a.session == nil {
		return nil
	}
	err := a.session.Close()
	a.session = nil
	return err
}

// Capabilities reports protocol and server-extension support.
func (a *Adapter) Capabilities() filesystem.CapabilitySet {
	return a.capabilities
}

// Open opens a remote stream and reconnects once if opening fails because the
// current connection was already lost.
func (a *Adapter) Open(ctx context.Context, logicalPath filesystem.Path) (io.ReadCloser, error) {
	file, err := withSession(a, ctx, true, func(session remoteSession) (remoteFile, error) {
		if err := a.rejectSymlinks(session, logicalPath, true); err != nil {
			return nil, err
		}
		return session.Open(a.remotePath(logicalPath))
	})
	if err != nil {
		return nil, mapError(logicalPath, err)
	}
	return newContextReadCloser(ctx, file), nil
}

// OpenRange opens a bounded range without buffering the whole remote file.
func (a *Adapter) OpenRange(ctx context.Context, logicalPath filesystem.Path, byteRange filesystem.ByteRange) (io.ReadCloser, error) {
	if !validByteRange(byteRange) {
		return nil, fmt.Errorf("%w: offset=%d length=%d", filesystem.ErrInvalidRange, byteRange.Offset, byteRange.Length)
	}
	file, err := withSession(a, ctx, true, func(session remoteSession) (remoteFile, error) {
		if err := a.rejectSymlinks(session, logicalPath, true); err != nil {
			return nil, err
		}
		opened, err := session.Open(a.remotePath(logicalPath))
		if err != nil {
			return nil, err
		}
		if _, err := opened.Seek(byteRange.Offset, io.SeekStart); err != nil {
			_ = opened.Close()
			return nil, err
		}
		return opened, nil
	})
	if err != nil {
		return nil, mapError(logicalPath, err)
	}
	stream := newContextReadCloser(ctx, file)
	return &limitedReadCloser{Reader: io.LimitReader(stream, byteRange.Length), closer: stream}, nil
}

// Write publishes through a temporary file. A consumed source is never
// replayed after connection loss.
func (a *Adapter) Write(ctx context.Context, logicalPath filesystem.Path, source io.Reader, options filesystem.WriteOptions) (filesystem.Metadata, error) {
	if logicalPath.IsRoot() {
		return filesystem.Metadata{}, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
	}
	if options.IfNoneMatch {
		return filesystem.Metadata{}, filesystem.Unsupported("sftp", filesystem.CapabilityWrite, filesystem.OperationWrite)
	}
	session, err := a.currentSession(ctx)
	if err != nil {
		return filesystem.Metadata{}, err
	}
	if err := a.rejectSymlinks(session, logicalPath, false); err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	remotePath := a.remotePath(logicalPath)
	if _, err := session.Stat(remotePath); err == nil && !a.atomicRename {
		return filesystem.Metadata{}, filesystem.Unsupported("sftp", filesystem.CapabilityWrite, filesystem.OperationWrite)
	} else if err != nil && !isNotFound(err) {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	if err := session.MkdirAll(path.Dir(remotePath)); err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	temporary, err := temporaryPath(path.Dir(remotePath), a.random)
	if err != nil {
		return filesystem.Metadata{}, err
	}
	file, err := session.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	cleanup := func() {
		_ = file.Close()
		_ = session.Remove(temporary)
	}
	if _, err := io.Copy(file, contextReader{ctx: ctx, reader: source}); err != nil {
		cleanup()
		return filesystem.Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return filesystem.Metadata{}, err
	}
	if err := file.Sync(); err != nil && !isOperationUnsupported(err) {
		cleanup()
		return filesystem.Metadata{}, err
	}
	if err := file.Close(); err != nil {
		_ = session.Remove(temporary)
		return filesystem.Metadata{}, err
	}
	if a.atomicRename {
		err = session.PosixRename(temporary, remotePath)
	} else {
		err = session.Rename(temporary, remotePath)
	}
	if err != nil {
		_ = session.Remove(temporary)
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	return a.Stat(ctx, logicalPath)
}

// OpenWriter returns a streaming writer that publishes its remote temporary
// file on Close without replaying an uncertain upload.
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

// Delete removes a file without replaying an ambiguous request.
func (a *Adapter) Delete(ctx context.Context, logicalPath filesystem.Path) error {
	session, err := a.currentSession(ctx)
	if err != nil {
		return err
	}
	if err := a.rejectSymlinks(session, logicalPath, true); err != nil {
		return mapError(logicalPath, err)
	}
	return mapError(logicalPath, session.Remove(a.remotePath(logicalPath)))
}

// Stat retrieves metadata and reconnects once after a lost connection.
func (a *Adapter) Stat(ctx context.Context, logicalPath filesystem.Path) (filesystem.Metadata, error) {
	info, err := withSession(a, ctx, true, func(session remoteSession) (fs.FileInfo, error) {
		if err := a.rejectSymlinks(session, logicalPath, true); err != nil {
			return nil, err
		}
		return session.Stat(a.remotePath(logicalPath))
	})
	if err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	kind := filesystem.EntryKindFile
	if info.IsDir() {
		kind = filesystem.EntryKindDirectory
	}
	return filesystem.Metadata{Path: logicalPath, Kind: kind, Size: info.Size(), Modified: info.ModTime()}, nil
}

// List returns a bounded deterministic snapshot and reconnects once if the
// connection is lost while walking.
func (a *Adapter) List(ctx context.Context, directory filesystem.Path, options filesystem.ListOptions) (filesystem.EntryIterator, error) {
	if options.Limit < 0 {
		return nil, errors.New("sftp: list limit must not be negative")
	}
	limit := effectiveListLimit(options.Limit, a.maxList)
	entries, err := withSession(a, ctx, true, func(session remoteSession) ([]filesystem.Entry, error) {
		return a.list(ctx, session, directory, options.Recursive, limit)
	})
	if err != nil {
		return nil, mapError(directory, err)
	}
	return &iterator{entries: entries}, nil
}

func (a *Adapter) list(ctx context.Context, session remoteSession, directory filesystem.Path, recursive bool, limit int) ([]filesystem.Entry, error) {
	if !directory.IsRoot() {
		if err := a.rejectSymlinks(session, directory, true); err != nil {
			return nil, err
		}
	}
	queue := []filesystem.Path{directory}
	entries := make([]filesystem.Entry, 0, min(limit, 128))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		infos, err := session.ReadDirContext(ctx, a.remotePath(current))
		if err != nil {
			return nil, err
		}
		slices.SortFunc(infos, func(left, right fs.FileInfo) int {
			return strings.Compare(left.Name(), right.Name())
		})
		for _, info := range infos {
			switch info.Mode().Type() {
			case fs.ModeSymlink:
				return nil, fmt.Errorf("sftp: symbolic link %q is denied", info.Name())
			}
			logicalPath, err := current.Join(info.Name())
			if err != nil {
				return nil, err
			}
			kind := filesystem.EntryKindFile
			if info.IsDir() {
				kind = filesystem.EntryKindDirectory
				if recursive {
					queue = append(queue, logicalPath)
				}
			}
			entries = append(entries, filesystem.Entry{Path: logicalPath, Kind: kind, Size: info.Size(), Modified: info.ModTime()})
			if len(entries) >= limit {
				sortEntries(entries)
				return entries, nil
			}
		}
	}
	sortEntries(entries)
	return entries, nil
}

func sortEntries(entries []filesystem.Entry) {
	slices.SortFunc(entries, func(left, right filesystem.Entry) int {
		return strings.Compare(left.Path.String(), right.Path.String())
	})
}

// Copy streams into a temporary destination and requires explicit overwrite
// because SFTP has no portable conditional destination operation.
func (a *Adapter) Copy(ctx context.Context, source, destination filesystem.Path, options filesystem.CopyOptions) error {
	if !options.Overwrite {
		return filesystem.Unsupported("sftp", filesystem.CapabilityCopy, filesystem.OperationCopy)
	}
	stream, err := a.Open(ctx, source)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	_, err = a.Write(ctx, destination, stream, filesystem.WriteOptions{})
	return err
}

// Move uses the POSIX rename extension and never emulates rename with
// copy-and-delete.
func (a *Adapter) Move(ctx context.Context, source, destination filesystem.Path, options filesystem.MoveOptions) error {
	if !a.atomicRename || !options.Overwrite {
		return filesystem.Unsupported("sftp", filesystem.CapabilityMove, filesystem.OperationMove)
	}
	session, err := a.currentSession(ctx)
	if err != nil {
		return err
	}
	if err := a.rejectSymlinks(session, source, true); err != nil {
		return mapError(source, err)
	}
	if err := a.rejectSymlinks(session, destination, false); err != nil {
		return mapError(destination, err)
	}
	if err := session.MkdirAll(path.Dir(a.remotePath(destination))); err != nil {
		return err
	}
	return mapError(source, session.PosixRename(a.remotePath(source), a.remotePath(destination)))
}

// SetMetadata returns a typed unsupported error.
func (a *Adapter) SetMetadata(context.Context, filesystem.Path, map[string]string) error {
	return filesystem.Unsupported("sftp", filesystem.CapabilityMetadata, filesystem.OperationSetMetadata)
}

// Checksum streams a remote file through the requested local digest.
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
		return filesystem.Checksum{}, filesystem.Unsupported("sftp", filesystem.CapabilityChecksum, filesystem.OperationChecksum)
	}
	return filesystem.Checksum{Algorithm: algorithm, Value: value}, nil
}

// TemporaryURL returns a typed unsupported error.
func (a *Adapter) TemporaryURL(context.Context, filesystem.Path, time.Duration, filesystem.TemporaryURLOptions) (string, error) {
	return "", filesystem.Unsupported("sftp", filesystem.CapabilityTemporaryURL, filesystem.OperationTemporaryURL)
}

// Visibility returns a typed unsupported error.
func (a *Adapter) Visibility(context.Context, filesystem.Path) (filesystem.Visibility, error) {
	return "", filesystem.Unsupported("sftp", filesystem.CapabilityVisibility, filesystem.OperationVisibility)
}

// SetVisibility returns a typed unsupported error.
func (a *Adapter) SetVisibility(context.Context, filesystem.Path, filesystem.Visibility) error {
	return filesystem.Unsupported("sftp", filesystem.CapabilityVisibility, filesystem.OperationSetVisibility)
}

func (a *Adapter) currentSession(ctx context.Context) (remoteSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, net.ErrClosed
	}
	if a.session != nil {
		return a.session, nil
	}
	session, err := a.connector(ctx)
	if err != nil {
		return nil, err
	}
	a.session = session
	return session, nil
}

func withSession[T any](a *Adapter, ctx context.Context, retry bool, operation func(remoteSession) (T, error)) (T, error) {
	var zero T
	session, err := a.currentSession(ctx)
	if err != nil {
		return zero, err
	}
	result, err := operation(session)
	if err == nil {
		return result, nil
	}
	if !retry || !isConnectionError(err) {
		return result, err
	}
	a.invalidate(session)
	session, reconnectErr := a.currentSession(ctx)
	if reconnectErr != nil {
		return zero, errors.Join(err, reconnectErr)
	}
	return operation(session)
}

func (a *Adapter) invalidate(failed remoteSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == failed {
		_ = a.session.Close()
		a.session = nil
	}
}

func (a *Adapter) rejectSymlinks(session remoteSession, logicalPath filesystem.Path, includeFinal bool) error {
	segments := strings.Split(logicalPath.String(), "/")
	segmentCount := len(segments)
	if !includeFinal {
		segmentCount--
	}
	for index := range segments[:segmentCount] {
		candidate := path.Join(a.root, path.Join(segments[:index+1]...))
		info, err := session.Lstat(candidate)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		switch info.Mode().Type() {
		case fs.ModeSymlink:
			return fmt.Errorf("sftp: symbolic link %q is denied", candidate)
		}
	}
	return nil
}

func (a *Adapter) remotePath(logicalPath filesystem.Path) string {
	if logicalPath.IsRoot() {
		return a.root
	}
	return path.Join(a.root, logicalPath.String())
}

type realSession struct {
	sftp *pkgsftp.Client
	ssh  *ssh.Client
}

func (s *realSession) Open(name string) (remoteFile, error) { return s.sftp.Open(name) }
func (s *realSession) OpenFile(name string, flags int) (remoteFile, error) {
	return s.sftp.OpenFile(name, flags)
}
func (s *realSession) Stat(name string) (fs.FileInfo, error)  { return s.sftp.Stat(name) }
func (s *realSession) Lstat(name string) (fs.FileInfo, error) { return s.sftp.Lstat(name) }
func (s *realSession) ReadDirContext(ctx context.Context, name string) ([]fs.FileInfo, error) {
	return s.sftp.ReadDirContext(ctx, name)
}
func (s *realSession) Remove(name string) error             { return s.sftp.Remove(name) }
func (s *realSession) MkdirAll(name string) error           { return s.sftp.MkdirAll(name) }
func (s *realSession) Rename(oldName, newName string) error { return s.sftp.Rename(oldName, newName) }
func (s *realSession) PosixRename(oldName, newName string) error {
	return s.sftp.PosixRename(oldName, newName)
}
func (s *realSession) HasExtension(name string) (string, bool) { return s.sftp.HasExtension(name) }
func (s *realSession) Close() error {
	sftpErr := s.sftp.Close()
	sshErr := s.ssh.Close()
	if errors.Is(sshErr, net.ErrClosed) {
		sshErr = nil
	}
	return errors.Join(sftpErr, sshErr)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}

type contextReadCloser struct {
	ctx       context.Context
	file      remoteFile
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func newContextReadCloser(ctx context.Context, file remoteFile) *contextReadCloser {
	stream := &contextReadCloser{ctx: ctx, file: file, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-stream.done:
		}
	}()
	return stream
}

func (r *contextReadCloser) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.file.Read(buffer)
	}
}

func (r *contextReadCloser) Close() error {
	r.closeOnce.Do(func() {
		close(r.done)
		r.closeErr = r.file.Close()
	})
	return r.closeErr
}

type limitedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *limitedReadCloser) Close() error { return r.closer.Close() }

type iterator struct {
	entries    []filesystem.Entry
	current    filesystem.Entry
	hasCurrent bool
	closed     bool
}

func (i *iterator) Next() bool {
	if i.closed || len(i.entries) == 0 {
		return false
	}
	i.current = i.entries[0]
	i.entries = i.entries[1:]
	i.hasCurrent = true
	return true
}

func (i *iterator) Entry() filesystem.Entry {
	if !i.hasCurrent {
		return filesystem.Entry{}
	}
	return i.current
}

func (i *iterator) Err() error { return nil }

func (i *iterator) Close() error {
	i.closed = true
	return nil
}

func validateRoot(root string) (string, error) {
	if root == "" {
		return "/", nil
	}
	if !strings.HasPrefix(root, "/") {
		return "", errors.New("sftp: root must be an absolute POSIX path")
	}
	if strings.Contains(root, `\`) {
		return "", errors.New("sftp: root must be an absolute POSIX path")
	}
	for _, segment := range strings.Split(root, "/") {
		if segment == ".." {
			return "", errors.New("sftp: root must not contain parent segments")
		}
		for _, character := range segment {
			if character == 0 || unicode.IsControl(character) {
				return "", errors.New("sftp: root contains a control character")
			}
		}
	}
	return path.Clean(root), nil
}

func defaultTimeout(timeout time.Duration) time.Duration {
	if timeout == 0 {
		return 30 * time.Second
	}
	return timeout
}

func defaultMaxList(limit int) int {
	if limit == 0 {
		return 10_000
	}
	return limit
}

func effectiveListLimit(requested, maximum int) int {
	if requested == 0 {
		return maximum
	}
	return min(requested, maximum)
}

func validByteRange(byteRange filesystem.ByteRange) bool {
	if byteRange.Offset < 0 || byteRange.Length <= 0 {
		return false
	}
	maximum := int64(^uint64(0) >> 1)
	return byteRange.Length-1 <= maximum-byteRange.Offset
}

func temporaryPath(directory string, randomSource io.Reader) (string, error) {
	var random [16]byte
	if _, err := io.ReadFull(randomSource, random[:]); err != nil {
		return "", fmt.Errorf("sftp: generate temporary path: %w", err)
	}
	return path.Join(directory, ".filesystem-"+hex.EncodeToString(random[:])+".tmp"), nil
}

func isConnectionError(err error) bool {
	if errors.Is(err, pkgsftp.ErrSSHFxConnectionLost) || errors.Is(err, pkgsftp.ErrSSHFxNoConnection) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var networkError *net.OpError
	return errors.As(err, &networkError)
}

func isNotFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, pkgsftp.ErrSSHFxNoSuchFile)
}

func isOperationUnsupported(err error) bool {
	if errors.Is(err, pkgsftp.ErrSSHFxOpUnsupported) {
		return true
	}
	var status *pkgsftp.StatusError
	return errors.As(err, &status) && status.FxCode() == pkgsftp.ErrSSHFxOpUnsupported
}

func mapError(logicalPath filesystem.Path, err error) error {
	if err == nil {
		return nil
	}
	if isNotFound(err) {
		return fmt.Errorf("%w: %s", filesystem.ErrNotFound, logicalPath)
	}
	return err
}
