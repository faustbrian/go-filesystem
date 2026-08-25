package local_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem"
	"github.com/faustbrian/go-filesystem/local"
)

func TestConcurrentCreateOnlyWritesPublishExactlyOnce(t *testing.T) {
	t.Parallel()

	adapter, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	const writers = 16
	path := filesystem.MustParsePath("immutable/report.bin")
	release := make(chan struct{})
	ready := make(chan struct{}, writers)
	var succeeded atomic.Int64
	var conflicted atomic.Int64
	var failures atomic.Int64
	var group sync.WaitGroup
	for index := range writers {
		group.Add(1)
		go func(value byte) {
			defer group.Done()
			_, writeErr := adapter.Write(
				context.Background(),
				path,
				&barrierReader{ready: ready, release: release, payload: []byte{value}},
				filesystem.WriteOptions{IfNoneMatch: true},
			)
			switch {
			case writeErr == nil:
				succeeded.Add(1)
			case errors.Is(writeErr, filesystem.ErrPreconditionFailed):
				conflicted.Add(1)
			default:
				failures.Add(1)
			}
		}(byte(index))
	}
	for range writers {
		<-ready
	}
	close(release)
	group.Wait()
	if succeeded.Load() != 1 || conflicted.Load() != writers-1 || failures.Load() != 0 {
		t.Fatalf(
			"create-only results: succeeded=%d conflicted=%d failed=%d",
			succeeded.Load(), conflicted.Load(), failures.Load(),
		)
	}
}

func TestConcurrentReadersObserveCompleteAtomicWrites(t *testing.T) {
	t.Parallel()

	adapter, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	path := filesystem.MustParsePath("shared/object.bin")
	payloads := make([][]byte, 4)
	valid := make(map[string]struct{}, len(payloads))
	for index := range payloads {
		payloads[index] = bytes.Repeat([]byte{byte('A' + index)}, 32*1024)
		valid[string(payloads[index])] = struct{}{}
	}
	if _, err := adapter.Write(
		context.Background(),
		path,
		bytes.NewReader(payloads[0]),
		filesystem.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	stop := make(chan struct{})
	errors := make(chan error, 1)
	report := func(err error) {
		select {
		case errors <- err:
		default:
		}
	}

	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
				}
				stream, err := adapter.Open(context.Background(), path)
				if err != nil {
					report(fmt.Errorf("open concurrent object: %w", err))
					return
				}
				content, readErr := io.ReadAll(stream)
				closeErr := stream.Close()
				if readErr != nil || closeErr != nil {
					report(fmt.Errorf("read concurrent object: read %v, close %v", readErr, closeErr))
					return
				}
				if _, ok := valid[string(content)]; !ok {
					report(fmt.Errorf("observed partial or mixed write of %d bytes", len(content)))
					return
				}
			}
		}()
	}

	var writers sync.WaitGroup
	for index := range payloads {
		writers.Add(1)
		go func(payload []byte) {
			defer writers.Done()
			<-start
			for range 50 {
				if _, err := adapter.Write(
					context.Background(),
					path,
					bytes.NewReader(payload),
					filesystem.WriteOptions{},
				); err != nil {
					report(fmt.Errorf("write concurrent object: %w", err))
					return
				}
			}
		}(payloads[index])
	}

	close(start)
	writers.Wait()
	close(stop)
	readers.Wait()
	select {
	case err := <-errors:
		t.Fatal(err)
	default:
	}
}

type barrierReader struct {
	ready   chan<- struct{}
	release <-chan struct{}
	payload []byte
	once    sync.Once
}

func (reader *barrierReader) Read(buffer []byte) (int, error) {
	reader.once.Do(func() {
		reader.ready <- struct{}{}
		<-reader.release
	})
	if len(reader.payload) == 0 {
		return 0, io.EOF
	}
	count := copy(buffer, reader.payload)
	reader.payload = reader.payload[count:]

	return count, nil
}
