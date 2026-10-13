package streamwriter

import (
	"context"
	"errors"
	"testing"
)

type writeResult struct {
	count int
	err   error
}

func (result writeResult) Write([]byte) (int, error) { return result.count, result.err }
func (writeResult) Close() error                     { return nil }

// The pipe's count is always retained. Cancellation replaces a failed write's
// error, not a successful write, and cannot erase an unrelated backend failure.
func TestWriteRetainsCountAndErrorPrecedence(t *testing.T) {
	backend := errors.New("backend write failed")
	for _, test := range []struct {
		name     string
		canceled bool
		count    int
		writeErr error
		wantErr  error
	}{
		{"partial backend failure", false, 2, backend, backend},
		{"partial canceled failure", true, 2, backend, context.Canceled},
		{"successful write after cancellation", true, 4, nil, nil},
		{"successful ordinary write", false, 4, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.canceled {
				cancel()
			}
			writer := &Writer{ctx: ctx, pipe: writeResult{count: test.count, err: test.writeErr}}
			count, err := writer.Write([]byte("data"))
			if count != test.count || !errors.Is(err, test.wantErr) {
				t.Fatalf("Write = %d, %v; want %d, %v", count, err, test.count, test.wantErr)
			}
		})
	}
}
