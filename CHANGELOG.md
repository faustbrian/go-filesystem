# Changelog

All notable changes to this project will be documented in this file. The
format follows Keep a Changelog and the project uses Semantic Versioning.

## 2.0.3 - 2026-10-22

### Changed

- Publish the current source and documentation with a fresh immutable tag.
- Build with Go 1.27.3 to address GO-2026-6809 in `crypto/x509`.
  Consumers should use Go 1.27.3 or newer.

## [Unreleased]

## [2.0.2] - 2026-10-13

### Fixed

- Preserve racing local Move destinations and concurrent source generations
  using atomic exclusive rename on Linux, macOS and Windows, retaining
  directory and admitted symlink behavior. No-overwrite requests fail closed
  with `ErrUnsupportedCapability` on other targets, or a native/unsupported
  error when the filesystem cannot provide exclusive rename;
  overwrite-enabled movement remains available. Handle this option-specific
  error when offering no-overwrite Move.
- Preserve content and metadata on overwrite-enabled memory self moves.
- Cancel adapter-owned streaming-writer pipes and release waiting consumers.
  Close still joins cleanup; borrowed readers and callbacks remain cooperative.
  [41488dc6ef](https://github.com/faustbrian/go-filesystem/commit/41488dc6ef6e7beba3a36f804ef99c26ca7532fd)
- Encode Windows exclusive-rename arguments without unsafe casts, validating
  handle widths, UTF-16 component bounds and native buffer lengths before
  narrowing ABI fields.
  [8ba0396558](https://github.com/faustbrian/go-filesystem/commit/8ba039655828fbaa96b5b569173f3c708189057c),
  [313c7280bd](https://github.com/faustbrian/go-filesystem/commit/313c7280bd0b7fb8b79eb8eb3a78510ea19ad5ce)

### Documentation

- Clarify provider buffering, containment/link limits, publication-error windows,
  stream close ownership and cancellation boundaries without changing IO behavior.
- Document checksum retry/observer privacy, local no-clobber move races and
  memory same-path move behavior rather than implying uniform guarantees.
  [8e3aec85d1](https://github.com/faustbrian/go-filesystem/commit/8e3aec85d15358f5cc0f5bf43f1a06d912615cf7)
- Make standalone adoption and package navigation discoverable and identify
  the published v2 module without implying that unpublished source is released.
  [d01529c88f](https://github.com/faustbrian/go-filesystem/commit/d01529c88f566c5799b1657f83b48c4943a82f3e),
  [089e24eafa](https://github.com/faustbrian/go-filesystem/commit/089e24eafa9c5b69d43da0b32816656410794ad4)
- Explain exact scanner conversion and checksum proofs at the security boundary.
  [96c1ea56c3](https://github.com/faustbrian/go-filesystem/commit/96c1ea56c3e6ebf5c475d2a9defbfa1c5ea56400)

### Maintenance

- Enforce source-documentation contracts with authenticated shared tooling,
  incorporating the earlier supply-chain tooling update.
  [a51aefa298](https://github.com/faustbrian/go-filesystem/commit/a51aefa29844341a6f47a3ba828f6a2b883c31c0),
  [94274684ae](https://github.com/faustbrian/go-filesystem/commit/94274684ae28654c1ea5cb5161423a6483c4df1c)
- Use bounded ordinary verification and selected scheduled assurance, retaining
  explicit full release qualification.
  [a0062df58c](https://github.com/faustbrian/go-filesystem/commit/a0062df58cbddc38be8370bf1b80bdb829d8676a),
  [851c409b6d](https://github.com/faustbrian/go-filesystem/commit/851c409b6d5e21038018d3541b081882b7fe0b99)
- Standardize repository development policy.
  [c61789cb9b](https://github.com/faustbrian/go-filesystem/commit/c61789cb9b8eaa16381d293390d283154f77b758)
- Build verification with Go 1.27.2 and patched documentation tooling while
  retaining the Go 1.27.0 module language minimum.
  [3dedc082a2](https://github.com/faustbrian/go-filesystem/commit/3dedc082a2092544cbbcaa9e56595ef4be502cf4)
- Refresh the unpublished internal ingestion harness to Tabular v2.0.2 and its
  public parser security patches; this does not release the nested module.
  [af3cf3172f](https://github.com/faustbrian/go-filesystem/commit/af3cf3172f93a89b64969db7a7c1908e4225481b)

## [2.0.1] - 2026-10-08

### Changed

- Refresh the AWS transfer manager, configuration and static-credential
  dependencies while retaining the newer S3 response-cleanup graph. Cover
  real SDK uploads, multipart publication and aborts, streaming writer
  failures, and explicit R2 signing through an always-runnable HTTP fixture.
  Configuration now recognizes `AWS_DISABLE_CLOCK_SKEW_CORRECTION` and its
  profile equivalent; malformed supplied values can fail R2 configuration
  loading. Validate explicitly configured values before upgrading.
  [86d599bf41](https://github.com/faustbrian/go-filesystem/commit/86d599bf4159a8c255e6247e0e6dda8fc5fe1a27)

- Adopt AWS SDK core v1.47.1 and Smithy v1.28.1 for S3 and R2.
  The SDK now uses trusted response-date information when classifying
  clock-skew retries and carries stream lengths through request construction;
  retain the existing S3 client and streaming response ownership.
  [4cd37d0309](https://github.com/faustbrian/go-filesystem/commit/4cd37d0309b15e92e6b739abd0789ce7bb52ba35)
- Refresh the internal ingestion harness to published Filesystem and Tabular
  v2, supplying the parser source-byte budget explicitly.
  [23dcd54f4d](https://github.com/faustbrian/go-filesystem/commit/23dcd54f4dcc8e66763c03e4d0aa3ff3073e8fba),
  [60f88d827c](https://github.com/faustbrian/go-filesystem/commit/60f88d827c06a483f6991b6e334f2a86022e30a2)
- Refresh shared CI enforcement.
  [9b2ecbd021](https://github.com/faustbrian/go-filesystem/commit/9b2ecbd021152cc6f90c490a131a9b3f4c0d6aef)

### Fixed

- Select S3 SDK v1.114.1 so received response bodies close when response
  interceptors reject an operation. Successful S3 and R2 read streams remain
  caller-owned; the selected SDK includes the upstream cleanup correction.
  Strengthen memory snapshot coverage.
  [cf024b645b](https://github.com/faustbrian/go-filesystem/commit/cf024b645b68145814526648f35bd4b13f0ccd64)

## [2.0.0] - 2026-09-27

### Fixed

- Preserve FTP machine-stat file-unavailable replies instead of replacing them
  with contradictory legacy-listing results or errors.

### Security

- FTP and SFTP reject non-root remote directories before dialing unless the
  caller explicitly sets `AllowUnsafeSubroot`. Client symlink checks cannot
  prevent concurrent remote link replacement; server-enforced account isolation
  is the confinement boundary.

### Changed

- Move the root module and imports to `github.com/faustbrian/go-filesystem/v2`
  for the breaking remote-root default. Prefer `Root: "/"` inside a jailed
  server namespace. Existing non-root deployments must provide server-side
  isolation before opting in to `AllowUnsafeSubroot`.

## [1.1.0] - 2026-09-09

### Added

- Add `local.Open`, `ftp.Open`, and `sftp.Open` for caller-owned resource
  acquisition and `r2.Load` for AWS configuration loading, with a shared
  `filesystem.ErrContextRequired` nil-context classification.

### Deprecated

- Deprecate the Local, FTP, SFTP, and R2 `New` functions in favor of their
  explicit acquisition or load names; each remains an exact compatibility
  delegation.

### Fixed

- Stop local root acquisition when its context becomes unavailable between
  option evaluation, root creation, and root opening, and close a root opened
  concurrently with cancellation.
- Report invalid Cloudflare R2 prefixes with the `r2` adapter identity while
  preserving `filesystem.ErrInvalidPath` classification.
- Run default fuzz smoke campaigns for a deterministic execution count so the
  Go fuzz harness cannot report its own duration deadline as an application
  failure.

### Security

- Reject nil SFTP authentication methods and unsafe R2 option collaborators
  before external I/O, and close partial SFTP acquisitions on cancellation.

- Update `golang.org/x/crypto` to v0.56.0 so SFTP SSH channel handling includes
  the fixes for GO-2026-6354 and GO-2026-6355 denial-of-service conditions.

### Changed

- Adopt the checksum-verified `go-library-tools` v1.4.0 CLI and immutable W14
  reusable workflow without changing the filesystem API or runtime behavior.

- Adopt the immutable shared Go library tooling contract while retaining the
  package-owned manifests, API baseline, fixtures, and approved mutation
  evidence.

- Adopt the checksum-verified `go-library-tools` v1.3.0 CLI, schema-v2
  cohesion metadata, and repository-local cohesion gate.
- Pin reusable CI to the immutable v1.3.0 cohesion workflow.

### Documentation

- Add a non-releasable executable recipe for bounded filesystem-to-tabular CSV
  ingestion, including cancellation, error ownership, and close ordering.

- Complete the package entry point with installation, an executable quick
  start, package selection, construction and lifecycle ownership, operational
  caveats, and the full support and ecosystem navigation contract.
- Document every adapter's concurrency and shutdown boundary, correct the root
  release tag convention, and link the enabled issue, discussion, and private
  vulnerability-reporting routes.

- Link ecosystem and Integration and Data Movement family guidance to the
  immutable v1.4.0 documentation release.

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

[2.0.2]: https://github.com/faustbrian/go-filesystem/compare/v2.0.1...v2.0.2
[2.0.0]: https://github.com/faustbrian/go-filesystem/compare/v1.1.0...v2.0.0
[1.1.0]: https://github.com/faustbrian/go-filesystem/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/faustbrian/go-filesystem/releases/tag/v1.0.0
