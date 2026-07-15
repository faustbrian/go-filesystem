package fstest_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem"
	filesystemtest "github.com/faustbrian/go-filesystem/fstest"
	"github.com/faustbrian/go-filesystem/memory"
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
