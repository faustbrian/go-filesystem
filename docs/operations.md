# Operations guide

## Credentials

Use workload identity or secret stores. Scope object credentials to the
smallest bucket/prefix and operations required. Pin SFTP host keys. FTP TLS
modes currently fail closed; plain FTP must run only inside a separately
encrypted trusted network. Error messages must not include passwords, secret
keys, authorization headers, signed query strings, or private keys.

## Streaming and cancellation

Pass the request context into every operation. Always close readers, writers,
iterators, and adapters. A successful `Open` transfers stream ownership to the
caller. A canceled write may have reached the backend; retry only when the
backend and request precondition make duplication safe.

Use `OpenWriter` when content is produced incrementally. Write all chunks and
then check `Close`: publication errors, including multipart completion or
temporary-file rename failures, are reported there. Cancel the operation
context if a producer abandons a writer before close.

`WriteOptions.IfNoneMatch` expresses create-only publication where supported.
Do not use it as a distributed lock unless the backend documents the required
consistency.

## Retries and consistency

Adapters do not apply a universal retry policy. The AWS SDK owns S3/R2
transport retries. SFTP and FTP retry only read-safe setup after a confirmed
connection failure. The optional decorator requires an explicit classifier and
retries only read-safe operation setup. It never retries a streamed write,
delete, copy, move, or metadata/visibility mutation after an ambiguous response.

Read-after-write and listing consistency belong to the selected backend.
Applications migrating between adapters must tolerate the weaker documented
contract, not the strongest source contract.

## Multipart uploads and temporary URLs

S3 and R2 use the AWS transfer manager for multipart uploads. Tune thresholds
through adapter options and bound concurrency in memory-constrained pods. R2
requires its service-specific uniform-part behavior. Failed completion is an
error; never report success for an incomplete upload.

Bound S3/R2 metadata with `WithMetadataLimits`. The default is 128 entries and
64 KiB of keys plus values for both caller-supplied and remote metadata.
Exceeding either limit returns `filesystem.ErrResourceLimit`.

Temporary URLs are bearer credentials. Keep lifetimes short, avoid logging the
URL, constrain response filename/content type, and deliver over TLS. They are
unsupported outside S3/R2.

## Kubernetes

Use workload identity where possible, mount SFTP keys read-only, and source FTP
passwords from Secrets. Set memory/CPU limits with multipart concurrency in
mind. Configure shutdown grace periods long enough to cancel transfers and
close clients. NetworkPolicies should restrict storage endpoints and DNS.

## Migration

Inventory required capabilities first. Copy objects with streaming reads and
writes, validate size plus an explicitly matching checksum algorithm, then
switch readers. Preserve metadata only when both adapters support it. Do not
assume visibility, ETags, rename atomicity, timestamps, or checksum algorithms
survive migration.

## Testing

Run the reusable `fstest.TestFilesystem` suite against every adapter factory.
Use `fstest.NewFaultReader`, `fstest.NewFaultWriter`, and
`fstest.NewFaultIterator` for short operations, latency, corruption,
disconnects, malformed pages, and cleanup assertions. Put
`fstest.NewTCPFaultProxy` between an adapter and an in-process server when the
dial, socket teardown, or bidirectional stream behavior must be exercised. The
memory adapter is appropriate for domain tests only when the domain does not
depend on a weaker backend guarantee.
