package streamwriter

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestWriterStreamsAndWaitsForConsumer(t *testing.T) {
	t.Parallel()

	var consumed strings.Builder
	writer := New(func(source io.Reader) error {
		_, err := io.Copy(&consumed, source)
		return err
	})
	if _, err := io.WriteString(writer, "first "); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, "second"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if consumed.String() != "first second" {
		t.Fatalf("consumed = %q", consumed.String())
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestWriterReturnsConsumerFailureFromClose(t *testing.T) {
	t.Parallel()

	injected := errors.New("upload failed")
	writer := New(func(source io.Reader) error {
		_, _ = io.Copy(io.Discard, source)
		return injected
	})
	if _, err := io.WriteString(writer, "content"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); !errors.Is(err, injected) {
		t.Fatalf("Close() = %v", err)
	}
}

func TestWriterConcurrentCloseIsStable(t *testing.T) {
	t.Parallel()

	writer := New(func(source io.Reader) error {
		_, err := io.Copy(io.Discard, source)
		return err
	})
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := writer.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		}()
	}
	wait.Wait()
}
