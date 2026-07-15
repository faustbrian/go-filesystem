// Package streamwriter adapts a reader-consuming upload into a closeable
// writer without buffering the whole object.
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

// Close signals end of input, waits for publication, and is idempotent.
func (w *Writer) Close() error {
	w.once.Do(func() {
		_ = w.pipe.Close()
		w.err = <-w.done
	})
	return w.err
}
