package filesystemtabular

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem"
	"github.com/faustbrian/go-filesystem/memory"
	"github.com/faustbrian/go-tabular"
)

func TestIngestCSVStreamsRowsAndClosesAfterConsumption(t *testing.T) {
	events := make([]string, 0, 3)
	stream := &trackingStream{
		Reader: strings.NewReader("name,city\nAda,Helsinki\nLinus,Espoo\n"),
		events: &events,
	}
	source := &stubFilesystem{stream: stream}
	config := testConfig()

	var rows []tabular.Row
	err := IngestCSV(context.Background(), source, filesystem.MustParsePath("imports/people.csv"), config, func(row tabular.Row) error {
		events = append(events, "consume")
		rows = append(rows, append(tabular.Row(nil), row...))
		return nil
	})
	if err != nil {
		t.Fatalf("IngestCSV() error = %v", err)
	}
	if fmt.Sprint(rows) != "[[Ada Helsinki] [Linus Espoo]]" {
		t.Fatalf("rows = %v", rows)
	}
	if fmt.Sprint(events) != "[consume consume close]" {
		t.Fatalf("events = %v, want callbacks before close", events)
	}
	if source.openCalls != 1 || source.opened.String() != "imports/people.csv" {
		t.Fatalf("open calls = %d, path = %q", source.openCalls, source.opened.String())
	}
}

func TestIngestCSVPreservesParserAndCloseFailures(t *testing.T) {
	closeErr := errors.New("close source")
	stream := &trackingStream{
		Reader:   strings.NewReader("name,city\n\"Ada,Helsinki\n"),
		closeErr: closeErr,
	}

	err := IngestCSV(context.Background(), &stubFilesystem{stream: stream}, filesystem.MustParsePath("broken.csv"), testConfig(), func(tabular.Row) error {
		t.Fatal("consumer called for malformed row")
		return nil
	})
	if !errors.Is(err, tabular.ErrorMalformedRow) {
		t.Fatalf("IngestCSV() error = %v, want ErrorMalformedRow", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("IngestCSV() error = %v, want close error", err)
	}
	if !stream.closed {
		t.Fatal("source stream was not closed")
	}
}

func TestIngestCSVHonorsCancellationBeforeBufferedRows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &trackingStream{Reader: strings.NewReader("name,city\nAda,Helsinki\nLinus,Espoo\n")}
	consumed := 0

	err := IngestCSV(ctx, &stubFilesystem{stream: stream}, filesystem.MustParsePath("people.csv"), testConfig(), func(tabular.Row) error {
		consumed++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("IngestCSV() error = %v, want context.Canceled", err)
	}
	if consumed != 1 {
		t.Fatalf("consumed rows = %d, want 1", consumed)
	}
	if !stream.closed {
		t.Fatal("source stream was not closed")
	}
}

func TestIngestCSVDoesNotDeliverRowsAfterReadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &trackingStream{
		Reader: &cancelingReader{
			Reader: strings.NewReader("name,city\nAda,Helsinki\n"),
			cancel: cancel,
		},
	}
	consumed := 0

	err := IngestCSV(ctx, &stubFilesystem{stream: stream}, filesystem.MustParsePath("people.csv"), testConfig(), func(tabular.Row) error {
		consumed++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("IngestCSV() error = %v, want context.Canceled", err)
	}
	if consumed != 0 {
		t.Fatalf("consumed rows = %d, want 0", consumed)
	}
	if !stream.closed {
		t.Fatal("source stream was not closed")
	}
}

func TestIngestCSVEnforcesTotalInputAndRowBounds(t *testing.T) {
	t.Run("object bytes", func(t *testing.T) {
		config := testConfig()
		config.MaxObjectBytes = 8
		stream := &trackingStream{Reader: strings.NewReader("name,city\nAda,Helsinki\n")}

		err := IngestCSV(context.Background(), &stubFilesystem{stream: stream}, filesystem.MustParsePath("large.csv"), config, func(tabular.Row) error {
			t.Fatal("consumer called for oversized input")
			return nil
		})
		if !errors.Is(err, ErrObjectTooLarge) {
			t.Fatalf("IngestCSV() error = %v, want ErrObjectTooLarge", err)
		}
		if !stream.closed {
			t.Fatal("source stream was not closed")
		}
	})

	t.Run("record bytes", func(t *testing.T) {
		config := testConfig()
		config.MaxRecordBytes = 10
		stream := &trackingStream{Reader: strings.NewReader("name,city\nAda,Helsinki\n")}

		err := IngestCSV(context.Background(), &stubFilesystem{stream: stream}, filesystem.MustParsePath("record.csv"), config, func(tabular.Row) error {
			t.Fatal("consumer called for oversized record")
			return nil
		})
		if !errors.Is(err, tabular.ErrorLimitExceeded) {
			t.Fatalf("IngestCSV() error = %v, want ErrorLimitExceeded", err)
		}
		if !stream.closed {
			t.Fatal("source stream was not closed")
		}
	})

	t.Run("field bytes", func(t *testing.T) {
		config := testConfig()
		config.MaxFieldBytes = 4
		stream := &trackingStream{Reader: strings.NewReader("name,city\nAda,Helsinki\n")}

		err := IngestCSV(context.Background(), &stubFilesystem{stream: stream}, filesystem.MustParsePath("field.csv"), config, func(tabular.Row) error {
			t.Fatal("consumer called for oversized field")
			return nil
		})
		if !errors.Is(err, tabular.ErrorLimitExceeded) {
			t.Fatalf("IngestCSV() error = %v, want ErrorLimitExceeded", err)
		}
		if !stream.closed {
			t.Fatal("source stream was not closed")
		}
	})

	t.Run("rows", func(t *testing.T) {
		config := testConfig()
		config.MaxRows = 1
		stream := &trackingStream{Reader: strings.NewReader("name,city\nAda,Helsinki\nLinus,Espoo\n")}
		consumed := 0

		err := IngestCSV(context.Background(), &stubFilesystem{stream: stream}, filesystem.MustParsePath("rows.csv"), config, func(tabular.Row) error {
			consumed++
			return nil
		})
		if !errors.Is(err, ErrRowLimitExceeded) {
			t.Fatalf("IngestCSV() error = %v, want ErrRowLimitExceeded", err)
		}
		if consumed != 1 {
			t.Fatalf("consumed rows = %d, want 1", consumed)
		}
	})
}

func TestIngestCSVValidatesBeforeOpeningAndPreservesOpenErrors(t *testing.T) {
	source := &stubFilesystem{}
	config := testConfig()
	config.MaxFieldBytes = 0

	err := IngestCSV(context.Background(), source, filesystem.MustParsePath("people.csv"), config, func(tabular.Row) error { return nil })
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("IngestCSV() error = %v, want ErrInvalidConfig", err)
	}
	if source.openCalls != 0 {
		t.Fatalf("open calls = %d, want 0", source.openCalls)
	}

	openErr := errors.New("open source")
	source = &stubFilesystem{openErr: openErr}
	err = IngestCSV(context.Background(), source, filesystem.MustParsePath("people.csv"), testConfig(), func(tabular.Row) error { return nil })
	if !errors.Is(err, openErr) {
		t.Fatalf("IngestCSV() error = %v, want open error", err)
	}
}

func ExampleIngestCSV() {
	ctx := context.Background()
	store := memory.New()
	object := filesystem.MustParsePath("imports/people.csv")
	_, err := store.Write(ctx, object, strings.NewReader("name,city\nAda,Helsinki\n"), filesystem.WriteOptions{
		ContentType: "text/csv",
	})
	if err != nil {
		panic(err)
	}

	err = IngestCSV(ctx, store, object, Config{
		MaxObjectBytes: 64 << 10,
		MaxRecordBytes: 8 << 10,
		MaxFieldBytes:  2 << 10,
		MaxRows:        100,
	}, func(row tabular.Row) error {
		fmt.Println(row)
		return nil
	})
	if err != nil {
		panic(err)
	}
	// Output: [Ada Helsinki]
}

func testConfig() Config {
	return Config{
		MaxObjectBytes: 1024,
		MaxRecordBytes: 256,
		MaxFieldBytes:  128,
		MaxRows:        10,
	}
}

type stubFilesystem struct {
	stream    io.ReadCloser
	openErr   error
	openCalls int
	opened    filesystem.Path
}

func (source *stubFilesystem) Open(_ context.Context, path filesystem.Path) (io.ReadCloser, error) {
	source.openCalls++
	source.opened = path
	return source.stream, source.openErr
}

type trackingStream struct {
	io.Reader
	closeErr error
	closed   bool
	events   *[]string
}

type cancelingReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (reader *cancelingReader) Read(destination []byte) (int, error) {
	count, err := reader.Reader.Read(destination)
	reader.cancel()
	return count, err
}

func (stream *trackingStream) Close() error {
	stream.closed = true
	if stream.events != nil {
		*stream.events = append(*stream.events, "close")
	}
	return stream.closeErr
}
