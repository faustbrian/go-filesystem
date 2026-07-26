# filesystem

`filesystem` is a capability-based, streaming filesystem abstraction for
Go. It supports local files, deterministic in-memory storage, Amazon S3,
Cloudflare R2, SFTP, and FTP without claiming that those backends provide the
same guarantees.

```go
store := memory.New()
path := filesystem.MustParsePath("documents/report.txt")

_, err := store.Write(ctx, path, source, filesystem.WriteOptions{
    ContentType: "text/plain",
})
if err != nil {
    return err
}

stream, err := store.Open(ctx, path)
if err != nil {
    return err
}
defer stream.Close()
_, err = io.Copy(destination, stream)
```

Consumers depend on the smallest interface they need:

```go
func download(ctx context.Context, reader filesystem.Reader, path filesystem.Path) error
```

Incremental producers use `filesystem.WriteOpener` and must check `Close`,
which waits for final publication:

```go
writer, err := store.OpenWriter(ctx, path, filesystem.WriteOptions{})
if err != nil {
    return err
}
if _, err := io.Copy(writer, source); err != nil {
    _ = writer.Close()
    return err
}
if err := writer.Close(); err != nil {
    return err
}
```

Inspect `Capabilities()` before offering backend-dependent behavior. Calling
an unsupported operation returns a typed `*filesystem.CapabilityError` that
wraps `filesystem.ErrUnsupportedCapability`.

## Design guarantees

- Logical paths are root-relative, slash-separated, and traversal-safe.
- Reads and writes stream through `io.Reader` and `io.Writer`; whole-object
  buffering is never part of the root contract.
- Listings are closeable iterators and every network adapter applies a bound.
- S3/R2 metadata has configurable entry and byte bounds.
- Remote adapters validate credentials, host identity, transport support, and
  root settings before use; unsupported FTPS configurations fail before dial.
- Retry and atomicity behavior is adapter-specific and documented explicitly.
- `filesystem.NewIOFS` exposes read-only capabilities through standard
  `io/fs` APIs.

See the [capability matrix](docs/capabilities.md),
[adapter guide](docs/adapters.md), [decorator guide](docs/decorators.md),
[operations guide](docs/operations.md), [hardening matrix](docs/hardening.md),
and [security policy](SECURITY.md).
The module requires Go 1.26 or newer.

## Status

The API is pre-1.0. Compatibility commitments and tested service versions are
recorded in [COMPATIBILITY.md](COMPATIBILITY.md). Google Cloud Storage and
Azure Blob Storage are intentionally outside the initial release.

## License

Licensed under the [MIT License](LICENSE).
