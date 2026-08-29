# Verification matrix

This matrix identifies the executable evidence behind each portability and
failure guarantee. Unit and in-process integration tests run in `go test
./...`; the pinned S3-compatible service runs in the integration workflow.

## Adapter conformance

| Adapter | Shared conformance | Real boundary | Adapter-specific evidence |
|---|---|---|---|
| Memory | `memory.TestConformance` | In-process store | cancellation, metadata copying, concurrent streams |
| Local | `local.TestConformance` | `os.Root` and host filesystem | symlink-swap containment, permissions, atomic rename cleanup, concurrent readers/writers |
| S3 | `s3.TestConformance` | pinned MinIO | pagination, conditional writes, metadata limits, temporary URLs, multipart abort |
| R2 | shared S3 transport suite | pinned MinIO through `r2.New` | `auto` region, endpoint validation, R2 profile, multipart abort |
| SFTP | `sftp.TestConformance` | in-process SSH and SFTP server | host keys, authentication, reconnect, POSIX rename negotiation |
| FTP | `ftp.TestConformance` | in-process FTP server | passive/active plaintext transfers, reconnect, legacy listings |

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

The shared v1 contract runs formatting, static analysis, tests, exact package
coverage, race checks, the fuzz and benchmark operations declared in
`.golib.yaml`, security checks, API compatibility, documentation, and
mutation verification:

```sh
make check
```

Repository structure, manifest, workflow, and shared configuration validation
run through the complete local contract:

```sh
make ci
```

The integration workflow additionally starts its pinned MinIO image and runs
`TestCompatibleServiceConformance` in both `./s3` and `./r2`.
