# filesystem

[![CI](https://github.com/faustbrian/go-filesystem/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/faustbrian/go-filesystem/actions/workflows/ci.yml)
[![CodeQL](https://img.shields.io/badge/CodeQL-required-blue)](https://github.com/faustbrian/go-filesystem/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Mutation](https://img.shields.io/badge/mutation-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Documentation](https://img.shields.io/badge/docs-checked_in_CI-blue)](docs/)
[![Go Reference](https://pkg.go.dev/badge/github.com/faustbrian/go-filesystem.svg)](https://pkg.go.dev/github.com/faustbrian/go-filesystem)
[![Release](https://img.shields.io/github/v/release/faustbrian/go-filesystem?sort=semver)](https://github.com/faustbrian/go-filesystem/releases)
[![Go](https://img.shields.io/badge/go-1.26.6-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`filesystem` is a capability-based, streaming filesystem abstraction for
Go. It supports local files, deterministic in-memory storage, Amazon S3,
Cloudflare R2, SFTP, and FTP without claiming that those backends provide the
same guarantees.

It owns logical paths, capability-oriented contracts, backend adapters,
decorators, and conformance helpers. It does not select an application storage
policy or erase backend-specific consistency, atomicity, durability,
permissions, or retry behavior.

The module has a stable v1 API and requires Go 1.26.6 or newer. The latest
public release is `v1.0.0`; additive acquisition APIs on `main` remain
unreleased until the next tagged version.

The API is portable Go. The release gate runs on Ubuntu 24.04; the local
adapter additionally requires a target supported by `os.Root` and retains the
standard library's platform-specific filesystem semantics. Network adapters
require a reachable compatible service.

For ecosystem-wide package selection and composition guidance, see the
versioned [Golib ecosystem index](https://github.com/faustbrian/go-library-tools/blob/v1.4.0/docs/ecosystem/README.md)
and [integration and data movement family guidance](https://github.com/faustbrian/go-library-tools/blob/v1.4.0/docs/ecosystem/design-language.md#package-families-and-selection).

## Installation

```sh
go get github.com/faustbrian/go-filesystem@v1
```

## Quick start

The [compiler-checked example](example_test.go) is the five-minute starting
point. Run it from a repository checkout with:

```sh
go test -run '^Example$' .
```

```go
package main

import (
    "context"
    "fmt"
    "io"
    "strings"

    filesystem "github.com/faustbrian/go-filesystem"
    "github.com/faustbrian/go-filesystem/memory"
)

func main() {
    ctx := context.Background()
    store := memory.New()
    path := filesystem.MustParsePath("documents/report.txt")

    _, err := store.Write(ctx, path, strings.NewReader("report"), filesystem.WriteOptions{
        ContentType: "text/plain",
    })
    if err != nil {
        panic(err)
    }

    stream, err := store.Open(ctx, path)
    if err != nil {
        panic(err)
    }
    defer func() { _ = stream.Close() }()
    content, err := io.ReadAll(stream)
    if err != nil {
        panic(err)
    }
    fmt.Println(string(content))
}
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

## Packages

| Package | Use |
| --- | --- |
| [`filesystem`](https://pkg.go.dev/github.com/faustbrian/go-filesystem) | Define logical paths, capability contracts, options, entries, typed errors, and the read-only `io/fs` bridge. |
| [`decorator`](https://pkg.go.dev/github.com/faustbrian/go-filesystem/decorator) | Add prefixes, read-only policy, streaming checksums, safe setup retries, or instrumentation around an adapter. |
| [`local`](https://pkg.go.dev/github.com/faustbrian/go-filesystem/local) | Store files beneath an opened, symlink-controlled local root. |
| [`memory`](https://pkg.go.dev/github.com/faustbrian/go-filesystem/memory) | Use deterministic, concurrency-safe in-memory storage for ephemeral data and tests. |
| [`s3`](https://pkg.go.dev/github.com/faustbrian/go-filesystem/s3) | Adapt a caller-configured AWS SDK v2 client to Amazon S3 or an explicitly configured compatible service. |
| [`r2`](https://pkg.go.dev/github.com/faustbrian/go-filesystem/r2) | Load and validate Cloudflare R2 configuration with R2-specific transport semantics. |
| [`sftp`](https://pkg.go.dev/github.com/faustbrian/go-filesystem/sftp) | Acquire bounded SFTP access with explicit authentication, host verification, and session ownership. |
| [`ftp`](https://pkg.go.dev/github.com/faustbrian/go-filesystem/ftp) | Acquire bounded plaintext FTP access only after an explicit security opt-in. |
| [`fstest`](https://pkg.go.dev/github.com/faustbrian/go-filesystem/fstest) | Test adapter conformance and deterministic stream, iterator, and transport faults; close every started TCP fault proxy. |

All packages are part of the single
`github.com/faustbrian/go-filesystem` module; there are no releasable nested
modules.

## When to use it

Use this module when code should depend on the smallest filesystem capability
it needs, stream content without whole-object buffering, select a backend
explicitly, and preserve backend-specific guarantees. It is also suitable for
implementing a custom backend against the `fstest` conformance suite.

Do not use it as an ORM, object synchronization engine, distributed lock,
cross-backend transaction layer, authorization system, or promise that every
backend supports the same operations. Prefer `io/fs` directly when read-only
standard-library traversal is the complete requirement.

## Construction and configuration

- `memory.New` creates an empty in-process adapter; its zero configuration is
  useful, and `WithClock` supplies a deterministic caller-owned clock. A
  shared adapter requires that callback to be concurrency-safe.
- `local.New` validates options and opens or creates the configured root. The
  default denies symlinks and creates files and directories with restrictive
  permissions; callers close the returned adapter.
- `s3.New` validates a bucket and options around a caller-owned AWS SDK client.
  The caller continues to own the client, credentials, endpoint, and retry
  policy.
- `r2.Load` accepts a context because it loads AWS configuration. It validates
  credentials, endpoint policy, bucket, prefix, and resource limits before
  returning an adapter; a supplied HTTP client remains caller-owned.
- `sftp.Open` and `ftp.Open` validate configuration and acquire caller-owned
  sessions. Their deprecated `New` functions remain exact compatibility
  delegations.
- `decorator.New` validates additive policy options around a borrowed backend;
  it does not take ownership of that backend.

There is no universal backend or configuration default. Adapter defaults,
validation rules, security prerequisites, and examples are in the
[adapter guide](docs/adapters.md).

## Errors, cancellation, and ownership

Every capability-interface operation accepts the caller's `context.Context`,
but input validation or capability errors can take precedence over
cancellation. Memory and local writes check cancellation while consuming the
source, and local readers check it before each read; a memory reader is an
independent snapshot that does not continue consulting the context after
`Open`. S3 and R2 pass the context to AWS SDK calls. FTP and SFTP read-stream
cancellation closes or aborts that active stream. Their writers poll
cancellation while reading caller input, but a blocked protocol write or
control call may not observe it until the client returns. SFTP's configured
timeout covers the TCP dial only. The operation context closes the connection
on cancellation or deadline during SSH setup, but post-acquisition control
calls have no package-set deadline.

The `filesystem.NewIOFS` bridge is intentionally non-contextual because the
standard `io/fs` interfaces have no context parameter. Its `Open`, `Stat`, and
`ReadDir` methods invoke the backend with `context.Background()`, so callers
cannot cancel a network-backed bridge call through the `io/fs` API.

Decorators forward the context, but also own precedence at their boundary:
retry-enabled reads check cancellation before each attempt, while read-only
mutation guards can return a capability error without consulting the context.

A canceled mutation can have an unknown backend outcome and must not be
retried blindly.

Invalid paths and resource ceilings support `errors.Is` through package
sentinels. Unsupported operations return `*filesystem.CapabilityError`;
backend failures preserve safe causes through wrapping or return a redacted
backend error. Inspect capabilities before exposing optional behavior.

Callers must close every successful reader and listing iterator. They must also
close each streaming writer and check the returned error because final
publication occurs during `Close`. FTP and SFTP sessions and local adapters are
caller-closed; memory, S3, R2, and decorators have no shutdown method. Adapters
start no hidden fleet or application lifecycle. Concurrency, callback, retry,
and shutdown details are documented in the [operations guide](docs/operations.md)
and [adapter guide](docs/adapters.md).

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

## Documentation

Start with the [documentation index](docs/README.md),
[capability matrix](docs/capabilities.md), [adapter guide](docs/adapters.md),
[decorator guide](docs/decorators.md), and
[operations guide](docs/operations.md). Adoption and maintenance references
include the [API reference](https://pkg.go.dev/github.com/faustbrian/go-filesystem),
[executable example](example_test.go), [`fstest` testing helpers](https://pkg.go.dev/github.com/faustbrian/go-filesystem/fstest),
[architecture](ARCHITECTURE.md), [compatibility](COMPATIBILITY.md),
[performance and verification evidence](docs/verification.md),
[FAQ](FAQ.md), [troubleshooting](TROUBLESHOOTING.md),
[release history](CHANGELOG.md), [support](SUPPORT.md),
[private security reporting](SECURITY.md), and [license](LICENSE).

## Status

The API is stable at v1. Compatibility commitments and tested service versions
are recorded in [COMPATIBILITY.md](COMPATIBILITY.md). Google Cloud Storage and
Azure Blob Storage are intentionally outside the initial release.

## Development

```sh
make inventory
make cohesion
make repository-check
make check
make ci
```

The shared repository gate compiles documentation examples, checks local and
external links, and validates the package and module manifests.

## License

Licensed under the [MIT License](LICENSE).
