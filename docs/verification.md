# Verification matrix

[Documentation index](README.md) · [Repository entry](../README.md)

This matrix identifies the executable evidence behind each portability and
failure guarantee. Unit and in-process integration tests run in
`GOWORK=off go test ./...`. Live S3/R2 cases skip unless an S3-compatible
endpoint is explicitly configured; the current workflow and manifest do not
provision MinIO.

Local commands and CI use the same strict contract through the v2.1.1
`go-library-tools` release pinned in `.golib.yaml`. The repository keeps only
package policy, source-specific evidence, and fixtures; shared gate
implementation is supplied by the pinned tool.

## Adapter conformance

| Adapter | Shared conformance | Real boundary | Adapter-specific evidence |
|---|---|---|---|
| Memory | `memory.TestConformance` | In-process store | cancellation, metadata copying, concurrent streams |
| Local | `local.TestConformance` | `os.Root` and host filesystem | symlink-swap containment, permissions, atomic rename cleanup, concurrent readers/writers |
| S3 | `s3.TestConformance` | explicitly configured S3-compatible service | pagination, conditional writes, metadata limits, temporary URLs, multipart abort |
| R2 | shared S3 transport suite | explicitly configured S3-compatible service through the R2 constructor | `auto` region, endpoint validation, R2 profile, multipart abort |
| SFTP | `sftp.TestConformance` | in-process SSH and SFTP server | host keys, authentication, reconnect, POSIX rename negotiation |
| FTP | `ftp.TestConformance` | independent in-process FTP server | passive/active plaintext transfers, reconnect, legacy listings, authoritative machine-stat errors |

S3 and R2 use separate constructors and integration buckets. The S3 caller
owns AWS SDK region, credentials, signing, endpoint, retry, and checksum
settings. R2 fixes the signing region to `auto`, validates the account endpoint,
uses path-style requests, and keeps ACL/copy-checksum differences explicit.
Neither profile advertises portable checksums. Both profiles separately cover
conditional writes, metadata, presigning, multipart success, and orphan cleanup.

FTPS is not listed as an operational boundary. The pinned FTP client cannot
honor protected data transfers, so explicit and implicit TLS configurations
fail before dialing. This avoids a connection that appears secure but fails or
downgrades when the first data channel opens.

Concrete FTP checks use the production `gonzalop/ftp` v1.6.1 client against
`fclairamb/ftpserverlib` v0.32.4 (MIT) with `afero` v1.15.0 (Apache-2.0) as a
test-only local filesystem driver. Successive transfers remain on one control
session. The client's own server implementation is not used: controlled
previous-transfer close and subsequent-read barriers reproduced its duplicate
cleanup canceling the next transfer after a successful completion reply.
The independent server closes the backend file before replying to the transfer.

## Fault and resource matrix

| Failure class | Fixture or target | Required observation |
|---|---|---|
| Short read or write | `fstest.FaultReader`, `fstest.FaultWriter` | exact byte boundary and surfaced error |
| Disconnect or truncation | fault streams and `fstest.TCPFaultProxy` | no replay of ambiguous writes; no success on partial data |
| Latency and timeout | fault streams/proxy plus contexts | cancellation reaches the active stream and cleanup completes |
| Corruption | fault streams/proxy byte offsets | changed bytes are observable; checksums are never inferred from ETags |
| Malformed listing | FTP/SFTP fuzz targets and fault iterator | bounded error, never guessed metadata |
| Hostile object keys | path and S3 key fuzz targets | no root escape or ambiguous normalized path |
| Symlink replacement | local stress test and local fuzz target | opened root is never escaped during concurrent swaps |
| Concurrent replacement | local race test | readers observe only complete generations during concurrent writes |
| Hostile metadata | S3/R2 metadata limits | `ErrResourceLimit` before cloning or forwarding oversized metadata |
| Large streams | 64 MiB local benchmark, multipart integration | allocation counts reported without pre-buffering the source |

Network listings default to at most 10,000 entries per call. S3 and R2 user
metadata defaults to at most 128 entries and 64 KiB of key/value bytes. Both
bounds can be lowered or raised explicitly; exceeding them returns an error.

## Required gates

The ordinary contract runs bounded local checks, deterministic tests and fuzz
seeds/corpora, plus the selected local create-only publication race. It does not
run full fuzz, benchmark or mutation campaigns:

```sh
make check
```

Repository structure, manifest, workflow, and shared configuration validation
also run through the bounded local route:

```sh
make ci
```

Changed-source CI adds bounded active smoke for affected fuzz targets. For short
single-target exploration and optional local mutation, see
[contributing](../CONTRIBUTING.md#verification). The original seven 10000x fuzz
budgets (each 20m), local/memory streaming and listing benchmarks (1x/20m),
full coverage/race, API, security, documentation and mutation checks remain:

```sh
make check-full
```

For the separate live boundary, provision a disposable compatible service and
set `S3_INTEGRATION_ENDPOINT`, `S3_INTEGRATION_ACCESS_KEY` and
`S3_INTEGRATION_SECRET_KEY` for:

```sh
GOWORK=off go test -run '^TestCompatibleServiceConformance$' -count=1 -timeout=3m ./s3 ./r2
```

These tests create and clean their own buckets and objects; they require a
service and credentials permitting those operations. Neither an unconfigured
Go run nor the manifest's service-free Make route proves this live boundary.

Choose the next guide in the [documentation index](README.md)
or return to the [repository entry](../README.md) for installation and ownership.
