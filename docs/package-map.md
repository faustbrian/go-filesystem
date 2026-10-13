# Package map

[Documentation index](README.md) · [Repository entry](../README.md)

This map follows the current source modules, not a synchronized release.
Use the repository README for published installation versions; an unpublished
source major is not installable merely because its package appears here.
Internal packages and non-releasable fixtures are intentionally excluded.

## github.com/faustbrian/go-filesystem/v2

`filesystem` is a capability-based, streaming filesystem abstraction for Go. It supports local files, deterministic in-memory storage, Amazon S3, Cloudflare R2, SFTP, and FTP without claiming that those backends provide the same guarantees.

| Import path | Responsibility |
| --- | --- |
| [`github.com/faustbrian/go-filesystem/v2`](../capability.go) | Use for capability-oriented streaming filesystem contracts, logical paths, typed errors, and standard io/fs interoperability. |
| [`github.com/faustbrian/go-filesystem/v2/decorator`](../decorator/decorator.go) | Use to add prefixes, read-only policy, checksums, safe retries, or instrumentation around an existing filesystem contract. |
| [`github.com/faustbrian/go-filesystem/v2/fstest`](../fstest/conformance.go) | Use in tests for backend conformance, deterministic faults, and transport fault injection. |
| [`github.com/faustbrian/go-filesystem/v2/ftp`](../ftp/ftp.go) | Use Open for bounded FTP access when its weaker protocol guarantees are explicitly acceptable. |
| [`github.com/faustbrian/go-filesystem/v2/local`](../local/local.go) | Use Open for context-preflighted, root-contained local filesystem acquisition with explicit caller ownership, symlink policy, and publication behavior. |
| [`github.com/faustbrian/go-filesystem/v2/memory`](../memory/memory.go) | Use for deterministic in-memory storage and tests without external I/O. |
| [`github.com/faustbrian/go-filesystem/v2/r2`](../r2/r2.go) | Use Load for Cloudflare R2 configuration through an explicitly configured S3-compatible client. |
| [`github.com/faustbrian/go-filesystem/v2/s3`](../s3/s3.go) | Use for Amazon S3 or explicitly configured S3-compatible object storage. |
| [`github.com/faustbrian/go-filesystem/v2/sftp`](../sftp/sftp.go) | Use Open for bounded SFTP access with explicit host verification and connection ownership. |

Choose the next guide in the [documentation index](README.md)
or return to the [repository entry](../README.md) for installation and ownership.
