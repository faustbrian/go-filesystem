// Package ftp provides a capability-based FTP adapter. The external
// protocol dependency is isolated behind a small session interface so server
// dialect and reconnect behavior remain explicit.
package ftp

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/internal/streamwriter"
	protocol "github.com/gonzalop/ftp"
)

var errFileUnavailable = errors.New("ftp: remote path unavailable")

// TLSMode selects control and data channel security.
type TLSMode uint8

const (
	// TLSExplicit requests AUTH TLS and currently fails closed at construction.
	TLSExplicit TLSMode = iota
	// TLSImplicit requests immediate TLS and currently fails closed at construction.
	TLSImplicit
	// TLSPlaintext sends credentials and data without transport encryption.
	TLSPlaintext
)

// DataMode selects how FTP data connections are established.
type DataMode uint8

const (
	// Passive uses EPSV/PASV and is appropriate for most clients behind NAT.
	Passive DataMode = iota
	// Active uses EPRT/PORT and requires the server to connect to the client.
	Active
)

// Config contains FTP connection, security, and remote-root settings.
type Config struct {
	// Address is the FTP server host and port.
	Address string
	// Username is the FTP login name.
	Username string
	// Password is the FTP login secret and is never included in errors.
	Password string
	// Root is the absolute remote directory containing all logical paths.
	Root string
	// TLSMode selects transport security. Only explicitly allowed plaintext is
	// currently operational; TLS modes fail closed before dialing.
	TLSMode TLSMode
	// TLSConfig verifies the server and must not skip verification.
	TLSConfig *tls.Config
	// AllowPlaintext explicitly permits unencrypted credentials and data.
	AllowPlaintext bool
	// DataMode selects passive or active data connections.
	DataMode DataMode
	// DisableEPSV forces PASV fallback for incompatible IPv4 servers.
	DisableEPSV bool
	// Timeout bounds dialing and protocol setup; zero selects 30 seconds.
	Timeout time.Duration
	// MaxListEntries bounds one listing; zero selects 10,000.
	MaxListEntries int
}

// Profile records protocol choices and observed listing support.
type Profile struct {
	// TLSMode is the configured transport-security mode.
	TLSMode TLSMode
	// DataMode is the configured data-connection mode.
	DataMode DataMode
	// MachineListings reports negotiated MLSD/MLST support.
	MachineListings bool
}

type remoteEntry struct {
	Name      string
	Size      int64
	Modified  time.Time
	Directory bool
	Link      bool
}

type remoteSession interface {
	Retrieve(string, io.Writer) error
	Store(string, io.Reader) error
	Stat(string) (remoteEntry, error)
	List(string) ([]remoteEntry, error)
	MakeDir(string) error
	Delete(string) error
	Rename(string, string) error
	Abort() error
	Quit() error
	MachineListings() bool
}

type connector func(context.Context) (remoteSession, error)

// Adapter serializes operations over one FTP control session. FTP does not
// permit independent concurrent commands on a control connection.
type Adapter struct {
	connector    connector
	random       io.Reader
	root         string
	maxList      int
	capabilities filesystem.CapabilitySet

	opMu sync.Mutex
	mu   sync.Mutex

	session remoteSession
	profile Profile
	closed  bool
}

// New validates security settings and establishes the initial FTP session.
func New(ctx context.Context, configuration Config) (*Adapter, error) {
	if strings.TrimSpace(configuration.Address) == "" {
		return nil, errors.New("ftp: address is required")
	}
	if strings.TrimSpace(configuration.Username) == "" {
		return nil, errors.New("ftp: username is required")
	}
	if configuration.Password == "" {
		return nil, errors.New("ftp: password is required")
	}
	if configuration.TLSMode > TLSPlaintext {
		return nil, fmt.Errorf("ftp: invalid TLS mode %d", configuration.TLSMode)
	}
	if configuration.DataMode > Active {
		return nil, fmt.Errorf("ftp: invalid data mode %d", configuration.DataMode)
	}
	if configuration.TLSMode == TLSPlaintext && !configuration.AllowPlaintext {
		return nil, errors.New("ftp: plaintext credentials require explicit opt-in")
	}
	if configuration.TLSMode != TLSPlaintext {
		if configuration.TLSConfig == nil {
			return nil, errors.New("ftp: TLS configuration is required")
		}
		if configuration.TLSConfig.InsecureSkipVerify {
			return nil, errors.New("ftp: TLS certificate verification must be enabled")
		}
		if configuration.TLSConfig.MinVersion != 0 && configuration.TLSConfig.MinVersion < tls.VersionTLS12 {
			return nil, errors.New("ftp: minimum TLS version must be TLS 1.2 or newer")
		}
		return nil, errors.New("ftp: TLS data transfers are not supported by the pinned protocol client")
	}
	root, err := validateRoot(configuration.Root)
	if err != nil {
		return nil, err
	}
	timeout := configuration.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if timeout < 0 {
		return nil, errors.New("ftp: timeout must not be negative")
	}
	maxList := configuration.MaxListEntries
	if maxList == 0 {
		maxList = 10_000
	}
	if maxList < 0 {
		return nil, errors.New("ftp: maximum list entries must be positive")
	}

	options := []protocol.Option{protocol.WithTimeout(timeout)}
	if configuration.DataMode == Active {
		options = append(options, protocol.WithActiveMode())
	}
	if configuration.DisableEPSV {
		options = append(options, protocol.WithDisableEPSV())
	}
	dial := func(ctx context.Context) (remoteSession, error) {
		return connect(ctx, configuration.Address, configuration.Username, configuration.Password, options)
	}
	return newAdapter(ctx, dial, root, maxList, Profile{
		TLSMode:  configuration.TLSMode,
		DataMode: configuration.DataMode,
	})
}

func newAdapter(ctx context.Context, dial connector, root string, maxList int, profile Profile) (*Adapter, error) {
	if dial == nil {
		return nil, errors.New("ftp: connector is required")
	}
	root, err := validateRoot(root)
	if err != nil {
		return nil, err
	}
	if maxList <= 0 {
		return nil, errors.New("ftp: maximum list entries must be positive")
	}
	session, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	profile.MachineListings = profile.MachineListings || session.MachineListings()
	return &Adapter{
		connector: dial,
		random:    rand.Reader,
		root:      root,
		maxList:   maxList,
		profile:   profile,
		capabilities: filesystem.NewCapabilitySet(
			filesystem.CapabilityRead,
			filesystem.CapabilityWrite,
			filesystem.CapabilityStreamingWrite,
			filesystem.CapabilityDelete,
			filesystem.CapabilityList,
			filesystem.CapabilityStat,
		),
		session: session,
	}, nil
}

// Profile returns the configured TLS/data mode and observed MLSD support.
func (a *Adapter) Profile() Profile {
	return a.profile
}

// Capabilities reports only features with portable FTP guarantees.
func (a *Adapter) Capabilities() filesystem.CapabilitySet {
	return a.capabilities
}

// Close aborts an active transfer, waits for the serialized operation, and
// closes the control session.
func (a *Adapter) Close() error {
	a.abortTransfer()
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	if a.session == nil {
		return nil
	}
	err := a.session.Quit()
	a.session = nil
	return err
}

// Open starts a streaming RETR operation. Closing or canceling the stream
// aborts the data transfer and releases the serialized control session.
func (a *Adapter) Open(ctx context.Context, logicalPath filesystem.Path) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.opMu.Lock()
	session, err := a.currentSession(ctx)
	if err != nil {
		a.opMu.Unlock()
		return nil, err
	}
	session, entry, err := a.prepareOpen(ctx, session, logicalPath)
	if err != nil {
		a.opMu.Unlock()
		return nil, mapError(logicalPath, err)
	}
	if entry.Directory {
		a.opMu.Unlock()
		return nil, fmt.Errorf("ftp: cannot open directory %q", logicalPath)
	}
	reader, writer := io.Pipe()
	stream := &readStream{
		adapter: a,
		ctx:     ctx,
		reader:  reader,
		done:    make(chan struct{}),
	}
	go stream.transfer(session, writer, logicalPath)
	go stream.watchCancellation()
	return stream, nil
}

func (a *Adapter) prepareOpen(ctx context.Context, session remoteSession, logicalPath filesystem.Path) (remoteSession, remoteEntry, error) {
	prepare := func(candidate remoteSession) (remoteEntry, error) {
		if err := a.rejectLink(candidate, logicalPath, true); err != nil {
			return remoteEntry{}, err
		}
		return candidate.Stat(a.remotePath(logicalPath))
	}
	entry, err := prepare(session)
	if err == nil || !isConnectionError(err) {
		return session, entry, err
	}
	a.invalidate(session)
	reconnected, reconnectErr := a.currentSession(ctx)
	if reconnectErr != nil {
		return nil, remoteEntry{}, errors.Join(err, reconnectErr)
	}
	entry, err = prepare(reconnected)
	return reconnected, entry, err
}

// OpenRange is typed unsupported because REST offsets do not provide a
// portable bounded-range guarantee across FTP servers.
func (a *Adapter) OpenRange(context.Context, filesystem.Path, filesystem.ByteRange) (io.ReadCloser, error) {
	return nil, filesystem.Unsupported("ftp", filesystem.CapabilityRangeRead, filesystem.OperationRangeRead)
}

// Write uploads to a temporary name and renames only when the destination is
// absent. FTP cannot safely replace an existing destination across servers.
func (a *Adapter) Write(ctx context.Context, logicalPath filesystem.Path, source io.Reader, options filesystem.WriteOptions) (filesystem.Metadata, error) {
	if logicalPath.IsRoot() {
		return filesystem.Metadata{}, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
	}
	if options.IfNoneMatch {
		return filesystem.Metadata{}, filesystem.Unsupported("ftp", filesystem.CapabilityWrite, filesystem.OperationWrite)
	}
	a.opMu.Lock()
	session, err := a.currentSession(ctx)
	if err != nil {
		a.opMu.Unlock()
		return filesystem.Metadata{}, err
	}
	err = a.write(ctx, session, logicalPath, source)
	a.opMu.Unlock()
	if err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	return a.Stat(ctx, logicalPath)
}

// OpenWriter returns a streaming writer that completes its temporary upload
// and rename on Close without replaying an uncertain transfer.
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

func (a *Adapter) write(ctx context.Context, session remoteSession, logicalPath filesystem.Path, source io.Reader) error {
	if err := a.rejectLink(session, logicalPath, false); err != nil {
		return err
	}
	destination := a.remotePath(logicalPath)
	if _, err := session.Stat(destination); err == nil {
		return filesystem.Unsupported("ftp", filesystem.CapabilityWrite, filesystem.OperationWrite)
	} else if !isNotFound(err) {
		return err
	}
	if err := a.makeDirectories(session, path.Dir(destination)); err != nil {
		return err
	}
	temporary, err := temporaryPath(path.Dir(destination), a.random)
	if err != nil {
		return err
	}
	if err := session.Store(temporary, contextReader{ctx: ctx, reader: source}); err != nil {
		_ = session.Delete(temporary)
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = session.Delete(temporary)
		return err
	}
	if err := session.Rename(temporary, destination); err != nil {
		_ = session.Delete(temporary)
		return err
	}
	return nil
}

// Delete sends one non-replayed DELE request because a lost response leaves
// the outcome ambiguous.
func (a *Adapter) Delete(ctx context.Context, logicalPath filesystem.Path) error {
	return a.withLocked(ctx, false, func(session remoteSession) error {
		if err := a.rejectLink(session, logicalPath, true); err != nil {
			return err
		}
		return session.Delete(a.remotePath(logicalPath))
	}, logicalPath)
}

// Stat reconnects once after a transport failure.
func (a *Adapter) Stat(ctx context.Context, logicalPath filesystem.Path) (filesystem.Metadata, error) {
	entry, err := withLockedResult(a, ctx, true, func(session remoteSession) (remoteEntry, error) {
		if err := a.rejectLink(session, logicalPath, true); err != nil {
			return remoteEntry{}, err
		}
		return session.Stat(a.remotePath(logicalPath))
	})
	if err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	kind := filesystem.EntryKindFile
	if entry.Directory {
		kind = filesystem.EntryKindDirectory
	}
	return filesystem.Metadata{Path: logicalPath, Kind: kind, Size: entry.Size, Modified: entry.Modified}, nil
}

// List produces a bounded deterministic snapshot and reconnects once after a
// control or data connection loss.
func (a *Adapter) List(ctx context.Context, directory filesystem.Path, options filesystem.ListOptions) (filesystem.EntryIterator, error) {
	if options.Limit < 0 {
		return nil, errors.New("ftp: list limit must not be negative")
	}
	limit := options.Limit
	if limit == 0 || limit > a.maxList {
		limit = a.maxList
	}
	entries, err := withLockedResult(a, ctx, true, func(session remoteSession) ([]filesystem.Entry, error) {
		return a.list(ctx, session, directory, options.Recursive, limit)
	})
	if err != nil {
		return nil, mapError(directory, err)
	}
	return &iterator{entries: entries, index: -1}, nil
}

func (a *Adapter) list(ctx context.Context, session remoteSession, directory filesystem.Path, recursive bool, limit int) ([]filesystem.Entry, error) {
	if !directory.IsRoot() {
		if err := a.rejectLink(session, directory, true); err != nil {
			return nil, err
		}
	}
	queue := []filesystem.Path{directory}
	entries := make([]filesystem.Entry, 0, min(limit, 128))
	for len(queue) > 0 && len(entries) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := queue[0]
		queue = queue[1:]
		children, err := session.List(a.remotePath(current))
		if err != nil {
			return nil, err
		}
		sort.Slice(children, func(left, right int) bool { return children[left].Name < children[right].Name })
		for _, child := range children {
			if child.Link {
				return nil, fmt.Errorf("ftp: symbolic link %q is denied", child.Name)
			}
			logicalPath, err := current.Join(child.Name)
			if err != nil {
				return nil, err
			}
			kind := filesystem.EntryKindFile
			if child.Directory {
				kind = filesystem.EntryKindDirectory
				if recursive {
					queue = append(queue, logicalPath)
				}
			}
			entries = append(entries, filesystem.Entry{Path: logicalPath, Kind: kind, Size: child.Size, Modified: child.Modified})
			if len(entries) == limit {
				break
			}
		}
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Path.String() < entries[right].Path.String() })
	return entries, nil
}

// Copy is unsupported because FTP has no portable server-side copy primitive.
func (a *Adapter) Copy(context.Context, filesystem.Path, filesystem.Path, filesystem.CopyOptions) error {
	return filesystem.Unsupported("ftp", filesystem.CapabilityCopy, filesystem.OperationCopy)
}

// Move is unsupported because RNFR/RNTO overwrite and atomicity differ across
// servers.
func (a *Adapter) Move(context.Context, filesystem.Path, filesystem.Path, filesystem.MoveOptions) error {
	return filesystem.Unsupported("ftp", filesystem.CapabilityMove, filesystem.OperationMove)
}

// SetMetadata returns a typed unsupported error.
func (a *Adapter) SetMetadata(context.Context, filesystem.Path, map[string]string) error {
	return filesystem.Unsupported("ftp", filesystem.CapabilityMetadata, filesystem.OperationSetMetadata)
}

// Checksum returns typed unsupported because HASH algorithms and response
// formats are server-specific.
func (a *Adapter) Checksum(context.Context, filesystem.Path, filesystem.ChecksumAlgorithm) (filesystem.Checksum, error) {
	return filesystem.Checksum{}, filesystem.Unsupported("ftp", filesystem.CapabilityChecksum, filesystem.OperationChecksum)
}

// TemporaryURL returns a typed unsupported error.
func (a *Adapter) TemporaryURL(context.Context, filesystem.Path, time.Duration, filesystem.TemporaryURLOptions) (string, error) {
	return "", filesystem.Unsupported("ftp", filesystem.CapabilityTemporaryURL, filesystem.OperationTemporaryURL)
}

// Visibility returns a typed unsupported error.
func (a *Adapter) Visibility(context.Context, filesystem.Path) (filesystem.Visibility, error) {
	return "", filesystem.Unsupported("ftp", filesystem.CapabilityVisibility, filesystem.OperationVisibility)
}

// SetVisibility returns a typed unsupported error.
func (a *Adapter) SetVisibility(context.Context, filesystem.Path, filesystem.Visibility) error {
	return filesystem.Unsupported("ftp", filesystem.CapabilityVisibility, filesystem.OperationSetVisibility)
}

func (a *Adapter) withLocked(ctx context.Context, retry bool, operation func(remoteSession) error, logicalPath filesystem.Path) error {
	_, err := withLockedResult(a, ctx, retry, func(session remoteSession) (struct{}, error) {
		return struct{}{}, operation(session)
	})
	return mapError(logicalPath, err)
}

func withLockedResult[T any](a *Adapter, ctx context.Context, retry bool, operation func(remoteSession) (T, error)) (T, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	var zero T
	session, err := a.currentSession(ctx)
	if err != nil {
		return zero, err
	}
	result, err := operation(session)
	if err == nil || !retry || !isConnectionError(err) {
		return result, err
	}
	a.invalidate(session)
	session, reconnectErr := a.currentSession(ctx)
	if reconnectErr != nil {
		return zero, errors.Join(err, reconnectErr)
	}
	return operation(session)
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

func (a *Adapter) invalidate(failed remoteSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == failed {
		_ = a.session.Quit()
		a.session = nil
	}
}

func (a *Adapter) abortTransfer() {
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	if session != nil {
		_ = session.Abort()
	}
}

func (a *Adapter) rejectLink(session remoteSession, logicalPath filesystem.Path, includeFinal bool) error {
	segments := strings.Split(logicalPath.String(), "/")
	if !includeFinal && len(segments) > 0 {
		segments = segments[:len(segments)-1]
	}
	for index := range segments {
		candidate := path.Join(a.root, path.Join(segments[:index+1]...))
		entry, err := session.Stat(candidate)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Link {
			return fmt.Errorf("ftp: symbolic link %q is denied", candidate)
		}
	}
	return nil
}

func (a *Adapter) makeDirectories(session remoteSession, directory string) error {
	if directory == "/" || directory == "." {
		return nil
	}
	segments := strings.Split(strings.TrimPrefix(directory, "/"), "/")
	current := "/"
	for _, segment := range segments {
		current = path.Join(current, segment)
		if err := session.MakeDir(current); err != nil {
			entry, statErr := session.Stat(current)
			if statErr != nil || !entry.Directory {
				return err
			}
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

type readStream struct {
	adapter *Adapter
	ctx     context.Context
	reader  *io.PipeReader
	done    chan struct{}

	closeOnce sync.Once
}

func (s *readStream) transfer(session remoteSession, writer *io.PipeWriter, logicalPath filesystem.Path) {
	counting := &countingWriter{writer: writer}
	err := session.Retrieve(s.adapter.remotePath(logicalPath), counting)
	if isConnectionError(err) && counting.count == 0 && s.ctx.Err() == nil {
		s.adapter.invalidate(session)
		if reconnected, reconnectErr := s.adapter.currentSession(s.ctx); reconnectErr == nil {
			err = reconnected.Retrieve(s.adapter.remotePath(logicalPath), counting)
		} else {
			err = errors.Join(err, reconnectErr)
		}
	}
	if contextErr := s.ctx.Err(); contextErr != nil {
		err = contextErr
	}
	_ = writer.CloseWithError(mapError(logicalPath, err))
	close(s.done)
	s.adapter.opMu.Unlock()
}

func (s *readStream) watchCancellation() {
	select {
	case <-s.ctx.Done():
		_ = s.reader.CloseWithError(s.ctx.Err())
		s.adapter.abortTransfer()
	case <-s.done:
	}
}

func (s *readStream) Read(buffer []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	return s.reader.Read(buffer)
}

func (s *readStream) Close() error {
	s.closeOnce.Do(func() {
		_ = s.reader.Close()
		s.adapter.abortTransfer()
	})
	<-s.done
	return s.ctx.Err()
}

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (w *countingWriter) Write(buffer []byte) (int, error) {
	written, err := w.writer.Write(buffer)
	w.count += int64(written)
	return written, err
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

type realSession struct {
	client          *protocol.Client
	machineListings bool
}

func (s *realSession) Retrieve(name string, destination io.Writer) error {
	return s.client.Retrieve(name, destination)
}
func (s *realSession) Store(name string, source io.Reader) error {
	return s.client.Store(name, source)
}
func (s *realSession) Stat(name string) (remoteEntry, error) {
	if s.machineListings {
		entry, err := s.client.MLStat(name)
		if err == nil {
			return remoteEntry{
				Name:      entry.Name,
				Size:      entry.Size,
				Modified:  entry.ModTime,
				Directory: entry.Type == "dir" || entry.Type == "cdir" || entry.Type == "pdir",
				Link:      entry.Type == "link",
			}, nil
		}
	}
	parent := path.Dir(name)
	entries, err := s.client.List(parent)
	if err != nil {
		return remoteEntry{}, err
	}
	base := path.Base(name)
	for _, entry := range entries {
		if entry.Name == base {
			modified, _ := s.client.ModTime(name)
			return remoteEntry{
				Name:      entry.Name,
				Size:      entry.Size,
				Modified:  modified,
				Directory: entry.Type == "dir",
				Link:      entry.Type == "link",
			}, nil
		}
	}
	return remoteEntry{}, errFileUnavailable
}
func (s *realSession) List(name string) ([]remoteEntry, error) {
	if s.machineListings {
		entries, err := s.client.MLList(name)
		if err == nil {
			return machineListEntries(entries), nil
		}
	}
	entries, err := s.client.List(name)
	if err != nil {
		return nil, err
	}
	result := make([]remoteEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, remoteEntry{
			Name:      entry.Name,
			Size:      entry.Size,
			Directory: entry.Type == "dir",
			Link:      entry.Type == "link",
		})
	}
	return result, nil
}

func machineListEntries(entries []*protocol.MLEntry) []remoteEntry {
	result := make([]remoteEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type == "cdir" || entry.Type == "pdir" {
			continue
		}
		result = append(result, remoteEntry{
			Name:      entry.Name,
			Size:      entry.Size,
			Modified:  entry.ModTime,
			Directory: entry.Type == "dir",
			Link:      entry.Type == "link",
		})
	}
	return result
}
func (s *realSession) MakeDir(name string) error { return s.client.MakeDir(name) }
func (s *realSession) Delete(name string) error  { return s.client.Delete(name) }
func (s *realSession) Rename(source, destination string) error {
	return s.client.Rename(source, destination)
}
func (s *realSession) Abort() error          { return s.client.Abort() }
func (s *realSession) Quit() error           { return s.client.Quit() }
func (s *realSession) MachineListings() bool { return s.machineListings }

func connect(ctx context.Context, address, username, password string, options []protocol.Option) (remoteSession, error) {
	type result struct {
		session remoteSession
		err     error
	}
	results := make(chan result, 1)
	go func() {
		client, err := protocol.Dial(address, options...)
		if err != nil {
			results <- result{err: fmt.Errorf("ftp: connect: %w", err)}
			return
		}
		if err := client.Login(username, password); err != nil {
			_ = client.Quit()
			results <- result{err: fmt.Errorf("ftp: authenticate: %w", err)}
			return
		}
		features, featureErr := client.Features()
		machineListings := supportsMachineListings(features)
		if featureErr != nil {
			machineListings = false
		}
		results <- result{session: &realSession{client: client, machineListings: machineListings}}
	}()
	select {
	case result := <-results:
		return result.session, result.err
	case <-ctx.Done():
		go func() {
			result := <-results
			if result.session != nil {
				_ = result.session.Quit()
			}
		}()
		return nil, ctx.Err()
	}
}

func supportsMachineListings(features map[string]string) bool {
	_, hasMLST := features["MLST"]
	_, hasMLSD := features["MLSD"]
	return hasMLST || hasMLSD
}

func validateRoot(root string) (string, error) {
	if root == "" {
		return "/", nil
	}
	if !strings.HasPrefix(root, "/") || strings.Contains(root, `\`) {
		return "", errors.New("ftp: root must be an absolute POSIX path")
	}
	for _, segment := range strings.Split(root, "/") {
		if segment == ".." {
			return "", errors.New("ftp: root must not contain parent segments")
		}
		for _, character := range segment {
			if character == 0 || unicode.IsControl(character) {
				return "", errors.New("ftp: root contains a control character")
			}
		}
	}
	return path.Clean(root), nil
}

func temporaryPath(directory string, randomSource io.Reader) (string, error) {
	var random [16]byte
	if _, err := io.ReadFull(randomSource, random[:]); err != nil {
		return "", fmt.Errorf("ftp: generate temporary path: %w", err)
	}
	return path.Join(directory, ".filesystem-"+hex.EncodeToString(random[:])+".tmp"), nil
}

func isConnectionError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var networkError *net.OpError
	if errors.As(err, &networkError) {
		return true
	}
	var protocolError *protocol.ProtocolError
	return errors.As(err, &protocolError) && (protocolError.Code == 421 || protocolError.Code == 425 || protocolError.Code == 426)
}

func isNotFound(err error) bool {
	if errors.Is(err, errFileUnavailable) {
		return true
	}
	var protocolError *protocol.ProtocolError
	return errors.As(err, &protocolError) && protocolError.Code == 550
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
