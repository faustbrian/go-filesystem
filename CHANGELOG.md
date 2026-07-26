# Changelog

All notable changes to this project will be documented in this file. The
format follows Keep a Changelog and the project intends to use Semantic
Versioning after its first release.

## [Unreleased]

### Compatibility

- Added a pinned module export baseline so incompatible public API changes
  fail the canonical repository gate.

### Added

- Capability-based core contracts, strict logical paths, and typed errors.
- Local and deterministic in-memory adapters.
- Amazon S3 and first-class Cloudflare R2 adapters.
- Reconnecting SFTP and FTP adapters with safe replay policies.
- Close-verified streaming writers for every initial adapter.
- Composable prefix, read-only, checksum, safe-retry, and instrumentation
  decorators.
- Read-only `io/fs` interoperability.
- Shared conformance tests, deterministic fault injection, fuzz targets, and
  performance benchmarks.
- Bidirectional TCP fault proxy, concurrent symlink-containment stress, S3/R2
  multipart cleanup integration, and 64 MiB allocation benchmarks.
- Credential-safe remote error wrapping and configurable S3/R2 metadata bounds.
- Capability, adoption, operations, architecture, security, compatibility,
  troubleshooting, contribution, and FAQ documentation.

### Changed

- FTP explicit and implicit TLS configurations now fail before dialing because
  the pinned protocol client cannot safely complete protected data transfers.
  Plaintext passive and active modes are covered by real transfer tests.
