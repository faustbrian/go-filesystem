package fstest

import (
	"errors"
	"io"
	"sync"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
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
	// Latency delays every Read to model a slow transport.
	Latency time.Duration
	// CorruptOffsets identifies absolute byte offsets to invert after reading.
	CorruptOffsets []int64
}

// FaultReader models short reads and deterministic mid-stream failures.
type FaultReader struct {
	reader    io.Reader
	maxChunk  int
	failAfter int64
	err       error
	read      int64
	latency   time.Duration
	corrupt   map[int64]struct{}
}

// NewFaultReader wraps reader with deterministic read behavior.
func NewFaultReader(reader io.Reader, options FaultReaderOptions) *FaultReader {
	injected := options.Err
	if injected == nil {
		injected = io.ErrUnexpectedEOF
	}
	corrupt := make(map[int64]struct{}, len(options.CorruptOffsets))
	for _, offset := range options.CorruptOffsets {
		if offset >= 0 {
			corrupt[offset] = struct{}{}
		}
	}
	return &FaultReader{
		reader:    reader,
		maxChunk:  options.MaxChunk,
		failAfter: options.FailAfter,
		err:       injected,
		latency:   options.Latency,
		corrupt:   corrupt,
	}
}

// Read implements io.Reader.
func (r *FaultReader) Read(buffer []byte) (int, error) {
	if r.latency > 0 {
		time.Sleep(r.latency)
	}
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
	start := r.read
	count, err := r.reader.Read(buffer[:limit])
	for index := range count {
		if _, ok := r.corrupt[start+int64(index)]; ok {
			buffer[index] = ^buffer[index]
		}
	}
	r.read += int64(count)
	if r.failAfter >= 0 && r.read >= r.failAfter {
		return count, r.err
	}
	return count, err
}

// FaultWriterOptions controls deterministic stream write failure injection.
type FaultWriterOptions struct {
	// MaxChunk limits bytes accepted by one Write. Zero disables the limit.
	MaxChunk int
	// FailAfter injects Err after this many bytes. A negative value disables
	// failure injection.
	FailAfter int64
	// Err is the injected failure. It defaults to io.ErrUnexpectedEOF.
	Err error
	// Latency delays every Write to model a slow transport.
	Latency time.Duration
}

// FaultWriter models short writes, latency, and deterministic partial failure.
type FaultWriter struct {
	mu        sync.Mutex
	writer    io.Writer
	maxChunk  int
	failAfter int64
	err       error
	latency   time.Duration
	written   int64
}

// NewFaultWriter wraps writer with deterministic write behavior.
func NewFaultWriter(writer io.Writer, options FaultWriterOptions) *FaultWriter {
	injected := options.Err
	if injected == nil {
		injected = io.ErrUnexpectedEOF
	}
	return &FaultWriter{
		writer:    writer,
		maxChunk:  options.MaxChunk,
		failAfter: options.FailAfter,
		err:       injected,
		latency:   options.Latency,
	}
}

// Write implements io.Writer.
func (w *FaultWriter) Write(buffer []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.latency > 0 {
		time.Sleep(w.latency)
	}
	if w.failAfter >= 0 && w.written >= w.failAfter {
		return 0, w.err
	}
	limit := len(buffer)
	if w.maxChunk > 0 && limit > w.maxChunk {
		limit = w.maxChunk
	}
	if w.failAfter >= 0 {
		remaining := w.failAfter - w.written
		if int64(limit) > remaining {
			limit = int(remaining)
		}
	}
	count, err := w.writer.Write(buffer[:limit])
	w.written += int64(count)
	if w.failAfter >= 0 && w.written >= w.failAfter {
		return count, w.err
	}
	return count, err
}

// Written reports the number of bytes accepted by the wrapped writer.
func (w *FaultWriter) Written() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written
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
	_ io.Writer                = (*FaultWriter)(nil)
	_ filesystem.EntryIterator = (*FaultIterator)(nil)
)
