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

Verify explicit versus implicit TLS, passive versus active networking, EPSV
support, certificate/SNI settings, and server MLSD/MLST support. Legacy listing
formats vary; malformed responses return errors rather than guessed metadata.
