package local_test

import (
	"context"
	"io"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/local"
)

func BenchmarkLargeObjectStreaming(b *testing.B) {
	const size = int64(64 * 1024 * 1024)
	adapter, err := local.New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = adapter.Close() })
	path := filesystem.MustParsePath("large-object.bin")
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := adapter.Write(
			context.Background(),
			path,
			io.LimitReader(benchmarkZeroReader{}, size),
			filesystem.WriteOptions{},
		); err != nil {
			b.Fatal(err)
		}
		stream, err := adapter.Open(context.Background(), path)
		if err != nil {
			b.Fatal(err)
		}
		written, copyErr := io.Copy(io.Discard, stream)
		closeErr := stream.Close()
		if written != size || copyErr != nil || closeErr != nil {
			b.Fatalf("read = %d, copy %v, close %v", written, copyErr, closeErr)
		}
	}
}

type benchmarkZeroReader struct{}

func (benchmarkZeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}
