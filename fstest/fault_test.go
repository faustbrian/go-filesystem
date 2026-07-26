package fstest_test

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	filesystemtest "github.com/faustbrian/golib/pkg/filesystem/fstest"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

func TestConformanceSuiteCoversSupportedAndUnsupportedCapabilities(t *testing.T) {
	t.Run("supported", func(t *testing.T) {
		filesystemtest.TestFilesystem(t, func(*testing.T) filesystemtest.Filesystem {
			return memory.New()
		})
	})
	t.Run("unsupported", func(t *testing.T) {
		filesystemtest.TestFilesystem(t, func(*testing.T) filesystemtest.Filesystem {
			return &limitedFilesystem{Adapter: memory.New()}
		})
	})
}

type limitedFilesystem struct{ *memory.Adapter }

func (*limitedFilesystem) Capabilities() filesystem.CapabilitySet {
	return filesystem.NewCapabilitySet(
		filesystem.CapabilityRead,
		filesystem.CapabilityWrite,
		filesystem.CapabilityStreamingWrite,
		filesystem.CapabilityDelete,
		filesystem.CapabilityList,
		filesystem.CapabilityStat,
	)
}

func (*limitedFilesystem) OpenRange(context.Context, filesystem.Path, filesystem.ByteRange) (io.ReadCloser, error) {
	return nil, filesystem.Unsupported("limited", filesystem.CapabilityRangeRead, filesystem.OperationRangeRead)
}

func (*limitedFilesystem) Copy(context.Context, filesystem.Path, filesystem.Path, filesystem.CopyOptions) error {
	return filesystem.Unsupported("limited", filesystem.CapabilityCopy, filesystem.OperationCopy)
}

func (*limitedFilesystem) Move(context.Context, filesystem.Path, filesystem.Path, filesystem.MoveOptions) error {
	return filesystem.Unsupported("limited", filesystem.CapabilityMove, filesystem.OperationMove)
}

func (*limitedFilesystem) SetMetadata(context.Context, filesystem.Path, map[string]string) error {
	return filesystem.Unsupported("limited", filesystem.CapabilityMetadata, filesystem.OperationSetMetadata)
}

func (*limitedFilesystem) Checksum(context.Context, filesystem.Path, filesystem.ChecksumAlgorithm) (filesystem.Checksum, error) {
	return filesystem.Checksum{}, filesystem.Unsupported("limited", filesystem.CapabilityChecksum, filesystem.OperationChecksum)
}

func (*limitedFilesystem) Visibility(context.Context, filesystem.Path) (filesystem.Visibility, error) {
	return "", filesystem.Unsupported("limited", filesystem.CapabilityVisibility, filesystem.OperationVisibility)
}

func (*limitedFilesystem) SetVisibility(context.Context, filesystem.Path, filesystem.Visibility) error {
	return filesystem.Unsupported("limited", filesystem.CapabilityVisibility, filesystem.OperationSetVisibility)
}

func TestFaultReaderLimitsChunksAndInjectsFailure(t *testing.T) {
	t.Parallel()

	injected := errors.New("connection reset")
	reader := filesystemtest.NewFaultReader(
		strings.NewReader("0123456789"),
		filesystemtest.FaultReaderOptions{
			MaxChunk:  2,
			FailAfter: 5,
			Err:       injected,
		},
	)
	buffer := make([]byte, 8)
	var content strings.Builder
	for {
		count, err := reader.Read(buffer)
		if count > 2 {
			t.Fatalf("Read() count = %d, want at most 2", count)
		}
		content.Write(buffer[:count])
		if err != nil {
			if !errors.Is(err, injected) {
				t.Fatalf("Read() error = %v", err)
			}
			break
		}
	}
	if content.String() != "01234" {
		t.Fatalf("content = %q, want 01234", content.String())
	}
}

func TestFaultReaderCanModelShortReadsWithoutFailure(t *testing.T) {
	t.Parallel()

	reader := filesystemtest.NewFaultReader(
		strings.NewReader("content"),
		filesystemtest.FaultReaderOptions{MaxChunk: 1, FailAfter: -1},
	)
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "content" {
		t.Fatalf("content = %q", content)
	}
}

func TestFaultReaderDefaultsAndImmediateFailure(t *testing.T) {
	t.Parallel()

	reader := filesystemtest.NewFaultReader(
		strings.NewReader("content"),
		filesystemtest.FaultReaderOptions{FailAfter: 0},
	)
	if count, err := reader.Read(make([]byte, 1)); count != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Read() = %d, %v", count, err)
	}
	reader = filesystemtest.NewFaultReader(
		strings.NewReader("x"),
		filesystemtest.FaultReaderOptions{FailAfter: -1},
	)
	if count, err := reader.Read(make([]byte, 4)); count != 1 || err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Read(unbounded) = %d, %v", count, err)
	}
}

func TestFaultReaderInjectsLatencyAndCorruption(t *testing.T) {
	t.Parallel()

	reader := filesystemtest.NewFaultReader(
		strings.NewReader("abcdef"),
		filesystemtest.FaultReaderOptions{
			FailAfter:      -1,
			Latency:        5 * time.Millisecond,
			CorruptOffsets: []int64{1, 4},
		},
	)
	started := time.Now()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 5*time.Millisecond {
		t.Fatalf("ReadAll() elapsed = %s, want injected latency", elapsed)
	}
	if content[0] != 'a' || content[1] != ^byte('b') || content[4] != ^byte('e') || content[5] != 'f' {
		t.Fatalf("corrupted content = %v", content)
	}
}

func TestFaultWriterLimitsChunksAndInjectsFailure(t *testing.T) {
	t.Parallel()

	injected := errors.New("connection reset")
	var destination strings.Builder
	writer := filesystemtest.NewFaultWriter(
		&destination,
		filesystemtest.FaultWriterOptions{
			MaxChunk:  3,
			FailAfter: 5,
			Err:       injected,
		},
	)
	count, err := writer.Write([]byte("abcdefgh"))
	if count != 3 || err != nil {
		t.Fatalf("first Write() = %d, %v", count, err)
	}
	count, err = writer.Write([]byte("defgh"))
	if count != 2 || !errors.Is(err, injected) {
		t.Fatalf("second Write() = %d, %v", count, err)
	}
	if destination.String() != "abcde" || writer.Written() != 5 {
		t.Fatalf("written content = %q, count = %d", destination.String(), writer.Written())
	}
}

func TestFaultWriterModelsShortWritesAndLatency(t *testing.T) {
	t.Parallel()

	var destination strings.Builder
	writer := filesystemtest.NewFaultWriter(
		&destination,
		filesystemtest.FaultWriterOptions{
			MaxChunk:  1,
			FailAfter: -1,
			Latency:   5 * time.Millisecond,
		},
	)
	started := time.Now()
	count, err := writer.Write([]byte("ab"))
	if elapsed := time.Since(started); elapsed < 5*time.Millisecond {
		t.Fatalf("Write() elapsed = %s, want injected latency", elapsed)
	}
	if count != 1 || err != nil || destination.String() != "a" {
		t.Fatalf("Write() = %d, %v, content %q", count, err, destination.String())
	}
}

func TestTCPFaultProxyInjectsBidirectionalNetworkFaults(t *testing.T) {
	t.Parallel()

	upstream := startEchoServer(t)
	proxy, err := filesystemtest.NewTCPFaultProxy(upstream, filesystemtest.TCPFaultProxyOptions{
		ClientToServer: filesystemtest.TCPFaultDirection{
			DisconnectAfter: 3,
			Latency:         5 * time.Millisecond,
			CorruptOffsets:  []int64{1},
		},
		ServerToClient: filesystemtest.TCPFaultDirection{CorruptOffsets: []int64{2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	connection, err := net.Dial("tcp", proxy.Address())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := connection.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(connection)
	if elapsed := time.Since(started); elapsed < 5*time.Millisecond {
		t.Fatalf("transfer elapsed = %s, want injected latency", elapsed)
	}
	want := []byte{'a', ^byte('b'), ^byte('c')}
	if string(content) != string(want) {
		t.Fatalf("proxied content = %v, want %v", content, want)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Close(); err != nil {
		t.Fatalf("Close(second) = %v", err)
	}
}

func TestTCPFaultProxyRejectsInvalidUpstream(t *testing.T) {
	t.Parallel()

	if proxy, err := filesystemtest.NewTCPFaultProxy("missing-port", filesystemtest.TCPFaultProxyOptions{}); err == nil {
		_ = proxy.Close()
		t.Fatal("NewTCPFaultProxy() error = nil")
	}
}

func startEchoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = connection.Close() }()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return listener.Addr().String()
}

func TestFaultIteratorFailsAtBoundaryAndTracksClose(t *testing.T) {
	t.Parallel()

	injected := errors.New("malformed listing page")
	iterator := filesystemtest.NewFaultIterator(
		[]filesystem.Entry{
			{Path: filesystem.MustParsePath("first.txt")},
			{Path: filesystem.MustParsePath("second.txt")},
		},
		1,
		injected,
	)
	if !iterator.Next() || iterator.Entry().Path.String() != "first.txt" {
		t.Fatal("first iterator entry missing")
	}
	if iterator.Next() {
		t.Fatal("iterator advanced beyond fault boundary")
	}
	if !errors.Is(iterator.Err(), injected) {
		t.Fatalf("Err() = %v", iterator.Err())
	}
	if iterator.Closed() {
		t.Fatal("iterator reported closed before Close")
	}
	if err := iterator.Close(); err != nil {
		t.Fatal(err)
	}
	if !iterator.Closed() || iterator.Next() {
		t.Fatal("closed iterator remained active")
	}
}

func TestFaultIteratorDefaultsAndExhaustion(t *testing.T) {
	t.Parallel()

	iterator := filesystemtest.NewFaultIterator(
		[]filesystem.Entry{{Path: filesystem.MustParsePath("entry")}},
		-1,
		nil,
	)
	if !iterator.Next() || iterator.Next() || iterator.Err() != nil {
		t.Fatalf("iterator exhaustion = next %v error %v", iterator.Next(), iterator.Err())
	}
	faulted := filesystemtest.NewFaultIterator(nil, 0, nil)
	if faulted.Next() || faulted.Err() == nil {
		t.Fatalf("default fault = next %v error %v", faulted.Next(), faulted.Err())
	}
}
