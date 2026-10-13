package streamwriter

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestCancellationUnblocksOwnedPipeConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	completed := make(chan error, 1)
	writer := New(ctx, func(source io.Reader) error {
		close(entered)
		_, err := io.Copy(io.Discard, source)
		completed <- err
		return err
	})
	<-entered
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("consume=%v", err)
		}
	case <-time.After(time.Second):
		t.Error("cancellation retained owned consumer")
	}
	// Close releases the pre-fix probe too; never retain a blocked test worker.
	err := writer.Close()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Close=%v (context %v)", err, ctx.Err())
	}
}

func TestCancellationAfterCompletionPreservesPublicationResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := New(ctx, func(source io.Reader) error { _, err := io.Copy(io.Discard, source); return err })
	if _, err := writer.Write([]byte("content")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := writer.Close(); err != nil {
		t.Fatalf("completed result changed after cancellation: %v", err)
	}
}

// A borrowed consumer callback is held cooperatively so no Read can accept
// the chunk. Cancellation must release the owned Write without that callback.
func TestCancellationReleasesOwnedBlockedWriteBeforeConsumerCooperates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	writer := New(ctx, func(io.Reader) error { close(entered); <-release; return ctx.Err() })
	<-entered
	type result struct {
		count int
		err   error
	}
	attempted := make(chan struct{})
	writeResult := make(chan result, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		close(attempted)
		n, err := writer.Write([]byte("chunk"))
		writeResult <- result{n, err}
	}()
	defer func() {
		close(release)
		_ = writer.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("producer did not join after cleanup")
		}
	}()
	<-attempted
	cancel()
	select {
	case got := <-writeResult:
		if got.count != 0 || !errors.Is(got.err, context.Canceled) {
			t.Errorf("unconsumed owned Write=%d, %v", got.count, got.err)
		}
	case <-time.After(time.Second):
		t.Error("cancellation did not release owned Write before borrowed callback cooperation")
	}
}
