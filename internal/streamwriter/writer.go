// Package streamwriter adapts a reader-consuming upload into a closeable
// writer with unbuffered pipe backpressure. The consumer may still buffer the
// whole object. New starts one worker; producer Close supplies EOF and joins it.
package streamwriter

import (
	"io"
	"sync"
)

// Writer sends writes through a bounded pipe and reports the consumer's final
// publication result from Close.
type Writer struct {
	pipe io.WriteCloser
	done <-chan error
	once sync.Once
	err  error
}

// New starts consume with the read side of a pipe. Callers must close the
// returned writer so the consumer can publish or clean up its partial object.
// consume must be non-nil, return normally, and release its resources; panics
// are not contained. There is no context watcher to unblock an already waiting
// pipe Read. Keep callbacks bounded if Close must finish within a deadline.
func New(consume func(io.Reader) error) *Writer {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := consume(reader)
		_ = reader.CloseWithError(err)
		done <- err
		close(done)
	}()
	return &Writer{pipe: writer, done: done}
}

// Write streams one chunk to the consumer.
func (w *Writer) Write(content []byte) (int, error) {
	return w.pipe.Write(content)
}

// Close signals EOF, joins the consumer and repeats its final result idempotently.
// Canceling an upload context is not a substitute for Close; a stuck consumer
// can block Close, and already published backend data cannot be rolled back here.
func (w *Writer) Close() error {
	w.once.Do(func() {
		_ = w.pipe.Close()
		w.err = <-w.done
	})
	return w.err
}
