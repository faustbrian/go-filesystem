package filesystem_test

import (
	"context"
	"io"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

func TestPublicCapabilityContractsRemainSmallAndComposable(t *testing.T) {
	t.Parallel()

	var _ filesystem.Reader = readerStub{}
	var _ filesystem.RangeReader = rangeReaderStub{}
	var _ filesystem.Writer = writerStub{}
	var _ filesystem.WriteOpener = writeOpenerStub{}
	var _ filesystem.Deleter = deleterStub{}
	var _ filesystem.Lister = listerStub{}
	var _ filesystem.Statter = statterStub{}
	var _ filesystem.Copier = copierStub{}
	var _ filesystem.Mover = moverStub{}
	var _ filesystem.MetadataSetter = metadataSetterStub{}
	var _ filesystem.Checksummer = checksummerStub{}
	var _ filesystem.TemporaryURLer = temporaryURLStub{}
	var _ filesystem.VisibilityManager = visibilityStub{}
	var _ filesystem.CapabilityReporter = capabilityReporterStub{}
}

type readerStub struct{}

func (readerStub) Open(context.Context, filesystem.Path) (io.ReadCloser, error) {
	return nil, nil
}

type rangeReaderStub struct{}

func (rangeReaderStub) OpenRange(context.Context, filesystem.Path, filesystem.ByteRange) (io.ReadCloser, error) {
	return nil, nil
}

type writerStub struct{}

func (writerStub) Write(context.Context, filesystem.Path, io.Reader, filesystem.WriteOptions) (filesystem.Metadata, error) {
	return filesystem.Metadata{}, nil
}

type writeOpenerStub struct{}

func (writeOpenerStub) OpenWriter(context.Context, filesystem.Path, filesystem.WriteOptions) (io.WriteCloser, error) {
	return nil, nil
}

type deleterStub struct{}

func (deleterStub) Delete(context.Context, filesystem.Path) error { return nil }

type listerStub struct{}

func (listerStub) List(context.Context, filesystem.Path, filesystem.ListOptions) (filesystem.EntryIterator, error) {
	return nil, nil
}

type statterStub struct{}

func (statterStub) Stat(context.Context, filesystem.Path) (filesystem.Metadata, error) {
	return filesystem.Metadata{}, nil
}

type copierStub struct{}

func (copierStub) Copy(context.Context, filesystem.Path, filesystem.Path, filesystem.CopyOptions) error {
	return nil
}

type moverStub struct{}

func (moverStub) Move(context.Context, filesystem.Path, filesystem.Path, filesystem.MoveOptions) error {
	return nil
}

type metadataSetterStub struct{}

func (metadataSetterStub) SetMetadata(context.Context, filesystem.Path, map[string]string) error {
	return nil
}

type checksummerStub struct{}

func (checksummerStub) Checksum(context.Context, filesystem.Path, filesystem.ChecksumAlgorithm) (filesystem.Checksum, error) {
	return filesystem.Checksum{}, nil
}

type temporaryURLStub struct{}

func (temporaryURLStub) TemporaryURL(context.Context, filesystem.Path, time.Duration, filesystem.TemporaryURLOptions) (string, error) {
	return "", nil
}

type visibilityStub struct{}

func (visibilityStub) Visibility(context.Context, filesystem.Path) (filesystem.Visibility, error) {
	return "", nil
}

func (visibilityStub) SetVisibility(context.Context, filesystem.Path, filesystem.Visibility) error {
	return nil
}

type capabilityReporterStub struct{}

func (capabilityReporterStub) Capabilities() filesystem.CapabilitySet {
	return filesystem.CapabilitySet{}
}
