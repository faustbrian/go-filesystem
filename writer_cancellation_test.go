package filesystem_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"github.com/faustbrian/go-filesystem/v2/local"
	"github.com/faustbrian/go-filesystem/v2/memory"
)

func TestOpenWriterCancellationReleasesOwnedPipe(t *testing.T) {
	directory := t.TempDir()
	localAdapter, err := local.Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = localAdapter.Close() })
	for name, adapter := range map[string]filesystem.WriteOpener{"local": localAdapter, "memory": memory.New()} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer, err := adapter.OpenWriter(ctx, filesystem.MustParsePath("object"), filesystem.WriteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			// Completion of this write proves the consumer has begun reading the pipe.
			if _, err := io.WriteString(writer, "partial"); err != nil {
				t.Fatal(err)
			}
			cancel()
			// Cancellation closes the pipe asynchronously. A consumer already in
			// Read may accept this whole chunk before closure; preserve its count.
			type writeResult struct {
				count int
				err   error
			}
			done := make(chan writeResult, 1)
			go func() { n, err := writer.Write([]byte("more")); done <- writeResult{n, err} }()
			select {
			case result := <-done:
				if result.count < 0 || result.count > len("more") {
					t.Errorf("invalid Write count=%d", result.count)
				}
				if result.err == nil {
					if result.count != len("more") {
						t.Errorf("successful Write count=%d", result.count)
					}
				} else if !errors.Is(result.err, context.Canceled) {
					t.Errorf("Write=%v", result.err)
				}
			case <-time.After(time.Second):
				t.Error("cancellation did not unblock owned pipe")
			}
			if err := writer.Close(); !errors.Is(err, context.Canceled) {
				t.Errorf("Close=%v", err)
			}
			// Joined cleanup establishes closure even when the racing chunk succeeded.
			if n, err := writer.Write([]byte("after cleanup")); n != 0 || !errors.Is(err, context.Canceled) {
				t.Errorf("Write after cleanup=%d, %v", n, err)
			}
			if statter, ok := adapter.(filesystem.Statter); ok {
				if _, err := statter.Stat(context.Background(), filesystem.MustParsePath("object")); !errors.Is(err, filesystem.ErrNotFound) {
					t.Errorf("canceled object exists: %v", err)
				}
			}
			if name == "local" {
				entries, err := os.ReadDir(directory)
				if err != nil || len(entries) != 0 {
					t.Errorf("temporary cleanup=%v, %v", entries, err)
				}
			}
			select {
			case <-done:
			default:
			}
		})
	}
}

type borrowedBlockedReader struct {
	entered chan struct{}
	release chan struct{}
	closed  bool
}

func (r *borrowedBlockedReader) Read([]byte) (int, error) {
	close(r.entered)
	<-r.release
	return 0, io.EOF
}
func (r *borrowedBlockedReader) Close() error { r.closed = true; return nil }

func TestWriteCancellationDoesNotOwnBorrowedBlockedReader(t *testing.T) {
	localAdapter, err := local.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = localAdapter.Close() }()
	for name, adapter := range map[string]filesystem.Writer{"local": localAdapter, "memory": memory.New()} {
		t.Run(name, func(t *testing.T) {
			reader := &borrowedBlockedReader{entered: make(chan struct{}), release: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := adapter.Write(ctx, filesystem.MustParsePath("object"), reader, filesystem.WriteOptions{})
				done <- err
			}()
			<-reader.entered
			cancel()
			select {
			case err := <-done:
				t.Errorf("borrowed Read was interrupted: %v", err)
			default:
			}
			close(reader.release)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("Write=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cooperative Read did not finish")
			}
			if reader.closed {
				t.Error("adapter closed borrowed source")
			}
		})
	}
}
