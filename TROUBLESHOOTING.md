# Troubleshooting

## `ErrUnsupportedCapability`

Check `Capabilities()` before presenting the operation. Use `errors.As` for
`*filesystem.CapabilityError` to report the adapter and operation.

## A path is rejected

Paths are logical, root-relative names. Remove leading slashes, Windows volume
names, parent segments, control characters, and ambiguous backslashes. Use
`Root()` only for listing or directory relationships.

## A remote write failed after sending bytes

Treat the result as unknown. Inspect the destination with `Stat`; do not replay
unless create-only preconditions or an application idempotency design make it
safe. SFTP and FTP intentionally avoid automatic write replay.

## Listings stop early

Check the caller `Limit` and adapter maximum. Consume `iterator.Err()` and call
`Close()` even after an early stop.

## SFTP move is unsupported

The server did not negotiate `posix-rename@openssh.com`. Copy and delete are
separate operations and are not silently presented as an atomic move.

## FTP connection or listing failures

Explicit and implicit FTPS currently return a construction error before any
network connection. For explicitly opted-in plain FTP, verify passive versus
active networking, EPSV support, and server MLSD/MLST support. Legacy listing
formats vary; malformed responses return errors rather than guessed metadata.

## `ErrResourceLimit` from S3 or R2

The response or write options exceeded the configured metadata entry or byte
ceiling. Inspect metadata cardinality and key/value sizes, then raise
`WithMetadataLimits` only when the source is trusted and the allocation is
acceptable.
