# Changelog

All notable changes to this project will be documented in this file. The
format follows Keep a Changelog and the project uses Semantic Versioning.

## [Unreleased]

### Changed

- Adopt the immutable shared Go library tooling contract while retaining the
  package-owned manifests, API baseline, fixtures, and approved mutation
  evidence.

- Adopt the checksum-verified `go-library-tools` v1.3.0 CLI, schema-v2
  cohesion metadata, and repository-local cohesion gate.
- Pin reusable CI to the immutable v1.3.0 cohesion workflow.

### Documentation

- Link the module to the immutable v1.3.0 Golib ecosystem guidance and
  correct its released lifecycle and exact Go baseline.
- Replace archived monorepo and hardening terminology with package-owned
  documentation and a verification matrix.

## [1.0.0] - 2026-08-25

### Changed

- Exclude intentional nested modules from root local-proxy archives so local,
  bootstrap, CI, and public module checksums describe the same source
  boundary.

- Track the pinned documentation-tool lockfile so clean CI checkouts install
  the exact validated cspell dependency.

- Reconcile standalone dependency checksums against deterministic current
  module archives so CI, local verification, and release consumers resolve
  identical content.

- Harden standalone documentation validation with deterministic spelling and
  link checks, package-specific documentation gates, and repository-local
  contributor guidance.

### Documentation

- Link the package README to package-owned documentation.

### Fixed

- Apply TCP fault-proxy latency after source data arrives so idle connections
  cannot consume the configured transport delay before a transfer begins.
- Make local create-only writes publish with an atomic hard-link operation so
  concurrent `IfNoneMatch` writers cannot overwrite the first object.

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

- Publish the module from its standalone `github.com/faustbrian/go-filesystem` identity while preserving its documented API and behavior.
- FTP explicit and implicit TLS configurations now fail before dialing because
  the pinned protocol client cannot safely complete protected data transfers.
  Plaintext passive and active modes are covered by real transfer tests.

[Unreleased]: https://github.com/faustbrian/go-filesystem/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/faustbrian/go-filesystem/releases/tag/v1.0.0
