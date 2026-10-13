// Package streamwriter adapts a reader-consuming upload into a closeable
// writer with unbuffered pipe backpressure. The consumer may still buffer the
// whole object. New starts one worker; producer Close supplies EOF and joins it.
package streamwriter

import (
	"context"
	"io"
	"sync"
)

// Writer sends writes through a bounded pipe and reports the consumer's final
// publication result from Close.
type Writer struct {
	ctx  context.Context
	pipe io.WriteCloser
	done <-chan error
	once sync.Once
	err  error
}

// New starts consume with the read side of a pipe. Callers must close the
// returned writer so the consumer can publish or clean up its partial object.
// consume must be non-nil, return normally, and release its resources; panics
// are not contained. Cancellation closes the owned pipe, unblocking its reads
// and writes; borrowed callbacks/backend calls must still return cooperatively.
// Keep callbacks bounded if Close must finish within a deadline.
func New(ctx context.Context, consume func(io.Reader) error) *Writer {
	reader, writer := io.Pipe()
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = writer.CloseWithError(ctx.Err())
	})
	done := make(chan error, 1)
	go func() {
		defer stopCancellation()
		err := consume(reader)
		_ = reader.CloseWithError(err)
		done <- err
		close(done)
	}()
	return &Writer{ctx: ctx, pipe: writer, done: done}
}

// Write streams one chunk to the consumer.
func (w *Writer) Write(content []byte) (int, error) {
	n, err := w.pipe.Write(content)
	if err != nil && w.ctx.Err() != nil {
		return n, w.ctx.Err()
	}
	return n, err
}

// Close signals EOF, joins the consumer and repeats its final result idempotently.
// Canceling an upload context releases pipe waits, but is not a substitute for
// joining with Close; a stuck borrowed callback/backend operation
// can block Close, and already published backend data cannot be rolled back here.
func (w *Writer) Close() error {
	w.once.Do(func() {
		_ = w.pipe.Close()
		w.err = <-w.done
	})
	return w.err
}
