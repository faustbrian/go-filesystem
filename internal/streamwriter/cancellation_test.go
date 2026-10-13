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
