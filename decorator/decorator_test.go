package decorator

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/fstest"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

func TestPrefixConformanceAndIsolation(t *testing.T) {
	underlying := memory.New()
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		adapter, err := New(underlying, WithPrefix("tenants/acme"))
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	})

	path := filesystem.MustParsePath("visible.txt")
	adapter, err := New(underlying, WithPrefix("isolated"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Stat(context.Background(), path); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Stat(outside prefix) = %v", err)
	}
	if _, err := adapter.Write(
		context.Background(),
		filesystem.Root(),
		strings.NewReader("invalid"),
		filesystem.WriteOptions{},
	); !errors.Is(err, filesystem.ErrInvalidPath) {
		t.Fatalf("Write(root) = %v", err)
	}
}

func TestInstrumentationRecordsLogicalOperations(t *testing.T) {
	underlying := memory.New()
	path := filesystem.MustParsePath("object.txt")
	if _, err := underlying.Write(
		context.Background(),
		filesystem.MustParsePath("private/object.txt"),
		strings.NewReader("content"),
		filesystem.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []Event
	adapter, err := New(
		underlying,
		WithPrefix("private"),
		WithObserver(func(event Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Stat(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	missing := filesystem.MustParsePath("missing.txt")
	if err := adapter.Delete(context.Background(), missing); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Delete() = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Operation != filesystem.OperationStat || events[0].Path != path || events[0].Error != nil {
		t.Fatalf("stat event = %+v", events[0])
	}
	if events[1].Operation != filesystem.OperationDelete || events[1].Path != missing || !errors.Is(events[1].Error, filesystem.ErrNotFound) {
		t.Fatalf("delete event = %+v", events[1])
	}
	for _, event := range events {
		if event.Started.IsZero() || event.Duration < 0 || event.Started.After(time.Now()) {
			t.Errorf("invalid timing in %+v", event)
		}
	}
}

func TestInstrumentationCoversConformanceSurface(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[filesystem.Operation]bool)
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		adapter, err := New(memory.New(), WithObserver(func(event Event) {
			mu.Lock()
			defer mu.Unlock()
			seen[event.Operation] = true
		}))
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	})
	mu.Lock()
	defer mu.Unlock()
	for _, operation := range []filesystem.Operation{
		filesystem.OperationRead,
		filesystem.OperationRangeRead,
		filesystem.OperationWrite,
		filesystem.OperationDelete,
		filesystem.OperationList,
		filesystem.OperationStat,
		filesystem.OperationCopy,
		filesystem.OperationMove,
		filesystem.OperationSetMetadata,
		filesystem.OperationChecksum,
		filesystem.OperationVisibility,
		filesystem.OperationSetVisibility,
	} {
		if !seen[operation] {
			t.Errorf("operation %q was not observed", operation)
		}
	}
}

func TestConfigurationRejectsInvalidPolicies(t *testing.T) {
	injected := errors.New("invalid option")
	tests := []struct {
		name    string
		backend Backend
		options []Option
	}{
		{name: "missing backend"},
		{name: "typed nil backend", backend: (*memory.Adapter)(nil)},
		{name: "invalid prefix", backend: memory.New(), options: []Option{WithPrefix("../escape")}},
		{name: "zero attempts", backend: memory.New(), options: []Option{WithRetry(RetryPolicy{Retryable: func(error) bool { return true }})}},
		{name: "missing classifier", backend: memory.New(), options: []Option{WithRetry(RetryPolicy{Attempts: 2})}},
		{name: "missing observer", backend: memory.New(), options: []Option{WithObserver(nil)}},
		{name: "option failure", backend: memory.New(), options: []Option{func(*configuration) error { return injected }}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.backend, test.options...); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func TestTemporaryURLDelegatesPrefixAndRetriesSetup(t *testing.T) {
	injected := errors.New("signer unavailable")
	underlying := &urlBackend{Adapter: memory.New(), failures: 1, err: injected}
	adapter, err := New(underlying, WithPrefix("private"), WithRetry(RetryPolicy{
		Attempts: 2,
		Retryable: func(err error) bool {
			return errors.Is(err, injected)
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	path := filesystem.MustParsePath("object.txt")
	url, err := adapter.TemporaryURL(
		context.Background(),
		path,
		time.Minute,
		filesystem.TemporaryURLOptions{DownloadName: "download.txt"},
	)
	if err != nil || url != "https://example.test/signed" {
		t.Fatalf("TemporaryURL() = %q, %v", url, err)
	}
	if underlying.path != filesystem.MustParsePath("private/object.txt") || underlying.calls != 2 {
		t.Fatalf("signer path = %q, calls = %d", underlying.path, underlying.calls)
	}
	plain, err := New(memory.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.TemporaryURL(context.Background(), path, time.Minute, filesystem.TemporaryURLOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("TemporaryURL(unsupported) = %v", err)
	}
}

func TestChecksumPropagatesOpenReadAndCloseFailures(t *testing.T) {
	injected := errors.New("stream failed")
	path := filesystem.MustParsePath("object.txt")
	for name, backend := range map[string]Backend{
		"open":  &streamFaultBackend{Adapter: memory.New(), openErr: injected},
		"read":  &streamFaultBackend{Adapter: memory.New(), stream: &faultReadCloser{readErr: injected}},
		"close": &streamFaultBackend{Adapter: memory.New(), stream: &faultReadCloser{reader: strings.NewReader("content"), closeErr: injected}},
	} {
		t.Run(name, func(t *testing.T) {
			adapter, err := New(backend, WithChecksums())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Checksum(context.Background(), path, filesystem.ChecksumSHA256); !errors.Is(err, injected) {
				t.Fatalf("Checksum() = %v", err)
			}
		})
	}
}

func TestListingRejectsEscapedEntriesAndReportsInnerFaults(t *testing.T) {
	injected := errors.New("listing failed")
	tests := []struct {
		name    string
		entries []filesystem.Entry
		fault   error
		want    error
	}{
		{
			name:    "escaped",
			entries: []filesystem.Entry{{Path: filesystem.MustParsePath("outside/object.txt")}},
			want:    filesystem.ErrInvalidPath,
		},
		{
			name:    "prefix root entry",
			entries: []filesystem.Entry{{Path: filesystem.MustParsePath("private")}},
		},
		{name: "inner fault", fault: injected, want: injected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &listingBackend{Adapter: memory.New(), entries: test.entries, fault: test.fault}
			adapter, err := New(backend, WithPrefix("private"))
			if err != nil {
				t.Fatal(err)
			}
			iterator, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			advanced := iterator.Next()
			if test.name == "prefix root entry" {
				if !advanced || !iterator.Entry().Path.IsRoot() {
					t.Fatalf("entry = %+v, advanced = %v", iterator.Entry(), advanced)
				}
			} else if advanced || !errors.Is(iterator.Err(), test.want) {
				t.Fatalf("Next() = %v, Err() = %v", advanced, iterator.Err())
			}
			if test.name == "escaped" && iterator.Next() {
				t.Fatal("faulted iterator advanced twice")
			}
			if err := iterator.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}

	adapter, err := New(&listingBackend{Adapter: memory.New(), listErr: injected}, WithPrefix("private"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{}); !errors.Is(err, injected) {
		t.Fatalf("List() = %v", err)
	}
}

func TestRetryPolicyTerminationAndBackoff(t *testing.T) {
	injected := errors.New("retryable")
	policy := &RetryPolicy{Attempts: 3, Retryable: func(error) bool { return true }}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := retryCall(canceled, policy, func() (string, error) {
		t.Fatal("call ran with a canceled context")
		return "", nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("retryCall(pre-canceled) = %v", err)
	}

	calls := 0
	if result, err := retryCall(context.Background(), policy, func() (string, error) {
		calls++
		return "partial", context.DeadlineExceeded
	}); result != "partial" || !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("deadline result = %q, err = %v, calls = %d", result, err, calls)
	}

	calls = 0
	nonRetryable := &RetryPolicy{Attempts: 3, Retryable: func(error) bool { return false }}
	if _, err := retryCall(context.Background(), nonRetryable, func() (string, error) {
		calls++
		return "", injected
	}); !errors.Is(err, injected) || calls != 1 {
		t.Fatalf("non-retryable err = %v, calls = %d", err, calls)
	}

	calls = 0
	if _, err := retryCall(context.Background(), policy, func() (string, error) {
		calls++
		return "", injected
	}); !errors.Is(err, injected) || calls != 3 {
		t.Fatalf("exhausted err = %v, calls = %d", err, calls)
	}

	calls = 0
	withDelay := &RetryPolicy{
		Attempts:  2,
		Retryable: func(error) bool { return true },
		Backoff:   func(int) time.Duration { return time.Millisecond },
	}
	if result, err := retryCall(context.Background(), withDelay, func() (string, error) {
		calls++
		if calls == 1 {
			return "", injected
		}
		return "complete", nil
	}); err != nil || result != "complete" || calls != 2 {
		t.Fatalf("delayed result = %q, err = %v, calls = %d", result, err, calls)
	}

	backoffContext, cancelBackoff := context.WithCancel(context.Background())
	longDelay := &RetryPolicy{
		Attempts:  2,
		Retryable: func(error) bool { return true },
		Backoff:   func(int) time.Duration { return time.Hour },
	}
	if _, err := retryCall(backoffContext, longDelay, func() (string, error) {
		cancelBackoff()
		return "", injected
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled backoff = %v", err)
	}
}

type urlBackend struct {
	*memory.Adapter
	failures int
	calls    int
	path     filesystem.Path
	err      error
}

func (b *urlBackend) TemporaryURL(
	_ context.Context,
	path filesystem.Path,
	_ time.Duration,
	_ filesystem.TemporaryURLOptions,
) (string, error) {
	b.calls++
	b.path = path
	if b.calls <= b.failures {
		return "", b.err
	}
	return "https://example.test/signed", nil
}

type streamFaultBackend struct {
	*memory.Adapter
	stream  io.ReadCloser
	openErr error
}

func (b *streamFaultBackend) Open(context.Context, filesystem.Path) (io.ReadCloser, error) {
	return b.stream, b.openErr
}

type faultReadCloser struct {
	reader   io.Reader
	readErr  error
	closeErr error
}

func (r *faultReadCloser) Read(buffer []byte) (int, error) {
	if r.readErr != nil {
		return 0, r.readErr
	}
	return r.reader.Read(buffer)
}

func (r *faultReadCloser) Close() error { return r.closeErr }

type listingBackend struct {
	*memory.Adapter
	entries []filesystem.Entry
	fault   error
	listErr error
}

func (b *listingBackend) List(
	context.Context,
	filesystem.Path,
	filesystem.ListOptions,
) (filesystem.EntryIterator, error) {
	if b.listErr != nil {
		return nil, b.listErr
	}
	return fstest.NewFaultIterator(b.entries, len(b.entries), b.fault), nil
}

func TestRetryOnlyReplaysReadSafeSetup(t *testing.T) {
	injected := errors.New("temporary transport failure")
	underlying := &flakyBackend{
		Adapter:      memory.New(),
		statFailures: 1,
		statErr:      injected,
		writeErr:     injected,
	}
	path := filesystem.MustParsePath("object.txt")
	if _, err := underlying.Adapter.Write(
		context.Background(),
		path,
		strings.NewReader("content"),
		filesystem.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	adapter, err := New(underlying, WithRetry(RetryPolicy{
		Attempts: 3,
		Retryable: func(err error) bool {
			return errors.Is(err, injected)
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Stat(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if underlying.StatCalls() != 2 {
		t.Fatalf("Stat calls = %d", underlying.StatCalls())
	}
	if _, err := adapter.Write(
		context.Background(),
		filesystem.MustParsePath("write.txt"),
		strings.NewReader("content"),
		filesystem.WriteOptions{},
	); !errors.Is(err, injected) {
		t.Fatalf("Write() = %v", err)
	}
	if underlying.WriteCalls() != 1 {
		t.Fatalf("Write calls = %d", underlying.WriteCalls())
	}
}

type flakyBackend struct {
	*memory.Adapter
	mu           sync.Mutex
	statFailures int
	statErr      error
	statCalls    int
	writeCalls   int
	writeErr     error
}

func (b *flakyBackend) Stat(ctx context.Context, path filesystem.Path) (filesystem.Metadata, error) {
	b.mu.Lock()
	b.statCalls++
	failed := b.statCalls <= b.statFailures
	b.mu.Unlock()
	if failed {
		return filesystem.Metadata{}, b.statErr
	}
	return b.Adapter.Stat(ctx, path)
}

func (b *flakyBackend) Write(
	context.Context,
	filesystem.Path,
	io.Reader,
	filesystem.WriteOptions,
) (filesystem.Metadata, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writeCalls++
	return filesystem.Metadata{}, b.writeErr
}

func (b *flakyBackend) StatCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.statCalls
}

func (b *flakyBackend) WriteCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writeCalls
}

func TestChecksumsAddsExplicitStreamingDigests(t *testing.T) {
	underlying := &withoutChecksums{Adapter: memory.New()}
	path := filesystem.MustParsePath("object.txt")
	if _, err := underlying.Write(
		context.Background(),
		path,
		strings.NewReader("hello"),
		filesystem.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	adapter, err := New(underlying, WithChecksums())
	if err != nil {
		t.Fatal(err)
	}
	if !adapter.Capabilities().Supports(filesystem.CapabilityChecksum) {
		t.Fatal("checksum capability was not added")
	}
	wants := map[filesystem.ChecksumAlgorithm]string{
		filesystem.ChecksumMD5:    "5d41402abc4b2a76b9719d911017c592",
		filesystem.ChecksumSHA256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		filesystem.ChecksumCRC32C: "9a71bb4c",
	}
	for algorithm, want := range wants {
		checksum, err := adapter.Checksum(context.Background(), path, algorithm)
		if err != nil {
			t.Fatal(err)
		}
		if checksum.Algorithm != algorithm || checksum.Value != want {
			t.Errorf("Checksum(%q) = %+v", algorithm, checksum)
		}
	}
	if _, err := adapter.Checksum(context.Background(), path, "sha1"); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Checksum(sha1) = %v", err)
	}
}

type withoutChecksums struct{ *memory.Adapter }

func (*withoutChecksums) Capabilities() filesystem.CapabilitySet {
	return filesystem.NewCapabilitySet(
		filesystem.CapabilityRead,
		filesystem.CapabilityWrite,
		filesystem.CapabilityStreamingWrite,
	)
}

func (*withoutChecksums) Checksum(
	context.Context,
	filesystem.Path,
	filesystem.ChecksumAlgorithm,
) (filesystem.Checksum, error) {
	return filesystem.Checksum{}, filesystem.Unsupported(
		"without-checksums",
		filesystem.CapabilityChecksum,
		filesystem.OperationChecksum,
	)
}

func TestReadOnlyFiltersCapabilitiesAndRejectsMutations(t *testing.T) {
	underlying := memory.New()
	path := filesystem.MustParsePath("object.txt")
	if _, err := underlying.Write(
		context.Background(),
		path,
		strings.NewReader("content"),
		filesystem.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	adapter, err := New(underlying, ReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || string(content) != "content" {
		t.Fatalf("read = %q, %v, close = %v", content, readErr, closeErr)
	}
	for _, capability := range []filesystem.Capability{
		filesystem.CapabilityWrite,
		filesystem.CapabilityStreamingWrite,
		filesystem.CapabilityDelete,
		filesystem.CapabilityCopy,
		filesystem.CapabilityMove,
		filesystem.CapabilityMetadata,
		filesystem.CapabilityVisibility,
		filesystem.CapabilityMultipart,
	} {
		if adapter.Capabilities().Supports(capability) {
			t.Errorf("read-only adapter supports %q", capability)
		}
	}

	destination := filesystem.MustParsePath("destination.txt")
	calls := []func() error{
		func() error {
			_, err := adapter.Write(context.Background(), destination, strings.NewReader("x"), filesystem.WriteOptions{})
			return err
		},
		func() error { return adapter.Delete(context.Background(), path) },
		func() error { return adapter.Copy(context.Background(), path, destination, filesystem.CopyOptions{}) },
		func() error { return adapter.Move(context.Background(), path, destination, filesystem.MoveOptions{}) },
		func() error {
			return adapter.SetMetadata(context.Background(), path, map[string]string{"key": "value"})
		},
		func() error { return adapter.SetVisibility(context.Background(), path, filesystem.VisibilityPublic) },
	}
	for _, call := range calls {
		if err := call(); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
			t.Errorf("mutation error = %v", err)
		}
	}
	if _, err := adapter.OpenWriter(context.Background(), destination, filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("OpenWriter() = %v", err)
	}
	if _, err := adapter.Visibility(context.Background(), path); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Visibility() = %v", err)
	}
}
