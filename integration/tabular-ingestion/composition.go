// Package filesystemtabular is a non-releasable reference composition for
// bounded CSV ingestion from a filesystem.Reader.
package filesystemtabular

import (
	"context"
	"errors"
	"io"

	filesystem "github.com/faustbrian/go-filesystem"
	"github.com/faustbrian/go-tabular"
)

var (
	// ErrInvalidConfig reports that a required application-owned bound or
	// callback is absent.
	ErrInvalidConfig = errors.New("filesystem tabular ingestion: invalid configuration")
	// ErrObjectTooLarge reports that the source contains more bytes than the
	// configured total-object bound.
	ErrObjectTooLarge = errors.New("filesystem tabular ingestion: object byte limit exceeded")
	// ErrRowLimitExceeded reports that the source contains more data rows than
	// the configured row bound.
	ErrRowLimitExceeded = errors.New("filesystem tabular ingestion: row limit exceeded")
)

// Config makes every retained-input bound explicit. IngestCSV treats the
// first record as a required, nonempty, unique header and does not retain it.
type Config struct {
	// MaxObjectBytes bounds all bytes read from the opened object.
	MaxObjectBytes int64
	// MaxRecordBytes bounds one logical CSV record before parser allocation.
	MaxRecordBytes int
	// MaxFieldBytes bounds one parsed CSV field.
	MaxFieldBytes int
	// MaxRows bounds data rows delivered after the header.
	MaxRows int
}

// IngestCSV opens object through source, parses bounded CSV rows, and passes
// each data row to consume synchronously. Filesystem opening errors, tabular
// parsing errors, context cancellation, callback errors, and application-owned
// limit errors retain their original errors.Is classifications.
//
// The opened stream remains owned by IngestCSV until consume and parsing stop.
// It is then closed before IngestCSV returns. A close error is joined with any
// earlier failure so neither outcome is lost. The tabular reader borrows the
// stream and owns no separately closeable resource.
func IngestCSV(
	ctx context.Context,
	source filesystem.Reader,
	object filesystem.Path,
	config Config,
	consume func(tabular.Row) error,
) (returnErr error) {
	if ctx == nil || source == nil || consume == nil || config.MaxObjectBytes <= 0 ||
		config.MaxRecordBytes <= 0 || config.MaxFieldBytes <= 0 || config.MaxRows <= 0 {
		return ErrInvalidConfig
	}

	stream, err := source.Open(ctx, object)
	if err != nil {
		return err
	}
	if stream == nil {
		return ErrInvalidConfig
	}
	defer func() {
		returnErr = errors.Join(returnErr, stream.Close())
	}()

	parser, err := tabular.NewCSVReader(
		&boundedContextReader{
			ctx:       ctx,
			source:    stream,
			remaining: config.MaxObjectBytes,
		},
		tabular.DelimitedConfig{
			MaxRecordBytes: config.MaxRecordBytes,
			MaxFieldBytes:  config.MaxFieldBytes,
			Header: &tabular.HeaderConfig{
				TrimSpace:        true,
				Case:             tabular.HeaderCaseLower,
				RejectEmpty:      true,
				RejectDuplicates: true,
			},
		},
	)
	if err != nil {
		return err
	}

	rows := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := parser.Read()
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if rows == config.MaxRows {
			return ErrRowLimitExceeded
		}
		if err := consume(row); err != nil {
			return err
		}
		rows++
	}
}

type boundedContextReader struct {
	ctx       context.Context
	source    io.Reader
	remaining int64
}

func (reader *boundedContextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if reader.remaining == 0 {
		var probe [1]byte
		count, err := reader.source.Read(probe[:])
		if count > 0 {
			return 0, ErrObjectTooLarge
		}
		return 0, err
	}
	if int64(len(destination)) > reader.remaining {
		destination = destination[:reader.remaining]
	}
	count, err := reader.source.Read(destination)
	reader.remaining -= int64(count)
	return count, err
}
