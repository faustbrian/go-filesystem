# Capability and guarantee matrix

Capabilities report operations that an adapter can provide without hiding
unsafe emulation. `S` means supported, `C` means conditional on a negotiated
server feature, and `-` means a typed unsupported-capability error.

| Capability | Memory | Local | S3 | R2 | SFTP | FTP |
|---|---:|---:|---:|---:|---:|---:|
| Read | S | S | S | S | S | S |
| Write | S | S | S | S | S | S |
| Streaming writer | S | S | S | S | S | S |
| Delete | S | S | S | S | S | S |
| List/stat | S | S | S | S | S | S |
| Range read | S | S | S | S | S | - |
| Copy | S | S | S | S | S | - |
| Move | S | S | - | - | C | - |
| User metadata | S | - | S | S | - | - |
| Checksum | S | S | - | - | S | - |
| Temporary URL | - | - | S | S | - | - |
| Visibility | S | - | - | - | - | - |
| Multipart upload | - | - | S | S | - | - |

## Semantics

| Property | Memory | Local | S3/R2 | SFTP | FTP |
|---|---|---|---|---|---|
| Write publication | Atomic under adapter lock | Temporary file plus rename | Object PUT/multipart completion | Temporary file plus rename | Temporary upload plus rename only when destination is absent |
| Rename | Atomic in process | OS rename beneath root | No rename primitive | Only advertised with `posix-rename@openssh.com` | Not advertised |
| Consistency | Immediate | Host filesystem | Service contract | Server filesystem | Server filesystem |
| Retry policy | None needed | None | AWS SDK read-safe policy | One reconnect for opening read-safe operations | One reconnect for read-safe operations only |
| Listing bound | Caller limit | Caller limit | Adapter maximum, default 10,000 | Adapter maximum, default 10,000 | Adapter maximum, default 10,000 |
| Metadata bound | Caller-owned | Not supported | 128 entries / 64 KiB default | Not supported | Not supported |

`OpenWriter` sends chunks through a bounded pipe to the adapter's normal
publication path. `Close` waits for the final sync/upload/rename and returns
that error; dropping a writer without closing it does not publish a successful
object and can retain the transfer goroutine until its context is canceled.

An adapter may implement a method so a common conformance type can call it
while still omitting that capability. Such calls always return
`ErrUnsupportedCapability`; method presence is not a capability claim.

S3 and R2 share an S3-compatible transport but use distinct profiles. R2 uses
region `auto`, validates the account endpoint, does not expose ACL visibility,
and keeps its documented multipart constraints explicit.

FTP capabilities apply only to explicitly opted-in plaintext transport.
Explicit and implicit TLS configurations fail before dialing because the
pinned client cannot currently prove protected data-transfer behavior.
