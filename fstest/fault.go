package fstest

import (
	"errors"
	"io"
	"sync"

	filesystem "github.com/faustbrian/go-filesystem"
)

// FaultReaderOptions controls deterministic stream failure injection.
type FaultReaderOptions struct {
	// MaxChunk limits bytes returned by one Read. Zero disables the limit.
	MaxChunk int
	// FailAfter injects Err after this many bytes. A negative value disables
	// failure injection.
	FailAfter int64
	// Err is the injected failure. It defaults to io.ErrUnexpectedEOF.
	Err error
}

// FaultReader models short reads and deterministic mid-stream failures.
type FaultReader struct {
	reader    io.Reader
	maxChunk  int
	failAfter int64
	err       error
	read      int64
}

// NewFaultReader wraps reader with deterministic read behavior.
func NewFaultReader(reader io.Reader, options FaultReaderOptions) *FaultReader {
	injected := options.Err
	if injected == nil {
		injected = io.ErrUnexpectedEOF
	}
	return &FaultReader{
		reader:    reader,
		maxChunk:  options.MaxChunk,
		failAfter: options.FailAfter,
		err:       injected,
	}
}

// Read implements io.Reader.
func (r *FaultReader) Read(buffer []byte) (int, error) {
	if r.failAfter >= 0 && r.read >= r.failAfter {
		return 0, r.err
	}
	limit := len(buffer)
	if r.maxChunk > 0 && limit > r.maxChunk {
		limit = r.maxChunk
	}
	if r.failAfter >= 0 {
		remaining := r.failAfter - r.read
		if int64(limit) > remaining {
			limit = int(remaining)
		}
	}
	count, err := r.reader.Read(buffer[:limit])
	r.read += int64(count)
	if r.failAfter >= 0 && r.read >= r.failAfter {
		return count, r.err
	}
	return count, err
}

// FaultIterator is a deterministic listing iterator that can fail after a
// configured number of entries.
type FaultIterator struct {
	mu        sync.Mutex
	entries   []filesystem.Entry
	failAfter int
	fault     error
	index     int
	current   filesystem.Entry
	err       error
	closed    bool
}

// NewFaultIterator constructs an iterator that reports fault after failAfter
// entries. A negative failAfter disables failure injection.
func NewFaultIterator(entries []filesystem.Entry, failAfter int, fault error) *FaultIterator {
	if fault == nil {
		fault = errors.New("fstest: injected listing failure")
	}
	return &FaultIterator{
		entries:   append([]filesystem.Entry(nil), entries...),
		failAfter: failAfter,
		fault:     fault,
	}
}

// Next advances the iterator unless it is closed, exhausted, or faulted.
func (i *FaultIterator) Next() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.err != nil {
		return false
	}
	if i.failAfter >= 0 && i.index >= i.failAfter {
		i.err = i.fault
		return false
	}
	if i.index >= len(i.entries) {
		return false
	}
	i.current = i.entries[i.index]
	i.index++
	return true
}

// Entry returns the current entry.
func (i *FaultIterator) Entry() filesystem.Entry {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.current
}

// Err reports the injected error, if any.
func (i *FaultIterator) Err() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.err
}

// Close marks the iterator closed.
func (i *FaultIterator) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closed = true
	return nil
}

// Closed reports whether Close has been called.
func (i *FaultIterator) Closed() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.closed
}

var (
	_ io.Reader                = (*FaultReader)(nil)
	_ filesystem.EntryIterator = (*FaultIterator)(nil)
)
