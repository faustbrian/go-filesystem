# Operations guide

## Credentials

Use workload identity or secret stores. Scope object credentials to the
smallest bucket/prefix and operations required. Pin SFTP host keys. FTP TLS
modes currently fail closed; plain FTP must run only inside a separately
encrypted trusted network. Error messages must not include passwords, secret
keys, authorization headers, signed query strings, or private keys.

## Streaming and cancellation

Pass the request context into every capability-interface operation. Validation
and capability checks may return before an adapter consults cancellation.
Memory and local writes check the context while consuming their source, and
local readers check it before each read. Memory readers are independent
snapshots and do not keep consulting the context after `Open` or `OpenRange`
returns. S3 and R2 pass the context into AWS SDK operations.

FTP and SFTP read-stream cancellation closes or aborts that active stream.
Their write paths poll cancellation while reading caller input, but a blocked
protocol write or control call may not observe it until the client returns.
SFTP's configured timeout bounds the TCP dial only. During SSH setup, operation
context cancellation or deadline closes the connection and bounds the
handshake. Post-acquisition control calls have no package-set deadline.
Context cancellation is therefore not a hard deadline for every FTP or SFTP
operation.

The `filesystem.NewIOFS` bridge is non-contextual because standard `io/fs`
methods have no context parameter. Its `Open`, `Stat`, and `ReadDir` methods
invoke the backend with `context.Background()`, so callers cannot cancel a
network-backed bridge call through the `io/fs` API.

Decorators forward the context, but also own precedence at their boundary.
Retry-enabled reads check cancellation before each attempt and can return it
before a wrapped backend validates its input. Read-only mutation guards can
instead return a capability or invalid-path error without consulting
cancellation. Observer callbacks run for the resulting decorator operation.

Always close readers, writers, iterators, and adapters. A successful `Open`
transfers stream ownership to the caller. A canceled write may have reached the
backend; retry only when the backend and request precondition make duplication
safe.

Use `OpenWriter` when content is produced incrementally. Write all chunks and
then check `Close`: publication errors, including multipart completion or
temporary-file rename failures, are reported there. Cancel the operation
context if a producer abandons a writer before close.

`WriteOptions.IfNoneMatch` expresses create-only publication where supported.
Do not use it as a distributed lock unless the backend documents the required
consistency.

## Concurrency and shutdown

| Adapter | Sharing contract | Close versus active work |
| --- | --- | --- |
| Memory | Safe for concurrent operations when a supplied `WithClock` callback is also concurrency-safe; reads are immutable snapshots. | No adapter close. Callers still close readers, writers, and iterators. |
| Local | Safe for concurrent operations while its opened `os.Root` remains live. | Stop new calls, wait for active calls, close returned streams and iterators, then call `Adapter.Close`. Calls racing adapter close are outside the contract. |
| S3 | Safe for concurrent operations when the caller-supplied AWS SDK client and option collaborators are safe for concurrent use. | No adapter close. The caller retains the SDK client and transport and closes transport resources only after operations and returned streams finish. |
| R2 | Safe for concurrent operations through its immutable S3 transport. A supplied HTTP client and transfer options must be safe for concurrent use. | No adapter close. A supplied HTTP client remains caller-owned; close its idle transport resources only after operations and returned streams finish. |
| SFTP | Operations may overlap on the shared multiplexed session; reconnect and closed state are synchronized. | Stop new calls and wait for active calls and returned streams before `Adapter.Close`. A close racing an operation may interrupt it and is outside the normal completion contract. |
| FTP | Calls share one control session and are serialized. | `Adapter.Close` aborts an active transfer, waits for the serialized operation, and closes the session. Do not start new calls after close begins. |
| Decorator | Shareability is exactly that of the wrapped backend. Retry, backoff, observer, and option collaborators used after construction must be concurrency-safe. | No decorator close. Quiesce decorated calls and close returned streams before closing the wrapped backend according to its contract. |

Every adapter configuration is fixed after successful construction. Do not
mutate caller-owned clients or callback state concurrently unless that
collaborator documents its own synchronization.

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
dial, socket teardown, or bidirectional stream behavior must be exercised.
Starting the proxy opens a listener and worker goroutines; the caller must
close it to terminate the listener and active connections and wait for its
workers. The memory adapter is appropriate for domain tests only when the
domain does not depend on a weaker backend guarantee.
