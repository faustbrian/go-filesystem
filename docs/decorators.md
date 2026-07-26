# Decorator guide

The `decorator` package composes optional policies around any initial adapter.
It preserves the backend capability set unless an option explicitly removes or
adds a guarantee.

```go
store, err := decorator.New(
    backend,
    decorator.WithPrefix("tenants/acme"),
    decorator.ReadOnly(),
    decorator.WithObserver(observeFilesystem),
)
```

## Prefix isolation

`WithPrefix` maps every caller-visible path beneath one normalized backend
prefix and removes it from returned metadata and listings. Invalid or escaped
listing entries fail with `filesystem.ErrInvalidPath`; they are never exposed
to the caller. A prefix is an isolation boundary, not an authorization system,
so credentials should still be scoped to the smallest backend namespace.

## Read-only access

`ReadOnly` returns typed unsupported-capability errors for write, streaming
write, delete, copy, move, metadata mutation, and visibility mutation. It
removes those capabilities, plus multipart upload, from `Capabilities()`.
Metadata and visibility are compound read/write contracts in the initial API,
so their capability flags are removed even though ordinary `Stat` results may
still contain metadata fields.

## Streaming checksums

`WithChecksums` adds MD5, SHA-256, and CRC-32C by opening and streaming the
complete object. It is useful when a backend has no portable native checksum,
but it incurs a full read and the object can change during calculation. The
returned algorithm is always explicit; ETags are never treated as checksums.

## Safe retries

`WithRetry` requires a caller-provided error classifier and attempt bound:

```go
store, err := decorator.New(backend, decorator.WithRetry(decorator.RetryPolicy{
    Attempts: 3,
    Retryable: func(err error) bool {
        return errors.Is(err, errTemporaryTransport)
    },
    Backoff: func(attempt int) time.Duration {
        return time.Duration(attempt) * 50 * time.Millisecond
    },
}))
```

Only setup for `Open`, `OpenRange`, `List`, `Stat`, `Checksum`,
`TemporaryURL`, and `Visibility` is retried. Stream reads after a successful
open and every mutation are never replayed. Context cancellation and deadlines
always stop retrying. Classifier and backoff callbacks must be concurrency-safe.

## Instrumentation

`WithObserver` synchronously reports operation, logical paths, start time,
duration, and returned error. Events never contain content, metadata values,
credentials, or signed URLs. Open and list events measure setup only; callers
must instrument stream reads, iterator consumption, and writer `Close`
separately when full transfer timing is required. Observer callbacks must be
fast and concurrency-safe.
