# FAQ

## Why not one large `Filesystem` interface?

Small interfaces let consumers state what they require and prevent a backend
from implying operations it cannot perform safely.

## Why is S3 move unsupported?

S3 has copy and delete, not atomic rename. Reporting that pair as move would
hide a partial state.

## Why are S3 ETags not checksums?

Their meaning depends on upload mode and encryption. A checksum is exposed only
with an explicit algorithm and reliable backend semantics.

## Can I use an adapter with `io/fs`?

Yes. Pass read, stat, and list capabilities to `filesystem.NewIOFS`. The bridge
is read-only and synthesizes logical directories from prefixes.

## Should tests always use memory storage?

Use it for backend-independent domain tests. Run `fstest.TestFilesystem` and
backend-specific integration tests for behavior involving atomicity, retries,
metadata, consistency, or protocol limits.

## Why is plaintext FTP available?

Some legacy private networks require it. The constructor requires explicit
opt-in so an insecure downgrade cannot occur accidentally.

## Why are FTPS configurations rejected?

The pinned client cannot currently prove protected data-channel behavior.
Accepting a TLS control connection and then failing or downgrading on the first
transfer would be unsafe, so explicit and implicit FTPS fail before dialing.

## Why can `Stat` return `ErrResourceLimit`?

Remote S3-compatible metadata is attacker-controlled. S3 and R2 cap metadata
entry count and key/value bytes before cloning it into caller-visible state.
Use `WithMetadataLimits` when a trusted workload needs a different ceiling.

## Can failed writes be retried automatically?

Only when the operation is known not to have published bytes or an explicit
precondition makes replay safe. SFTP and FTP never replay streamed writes after
connection loss; S3/R2 transport retries remain the AWS SDK's responsibility.
