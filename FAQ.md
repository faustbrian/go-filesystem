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
