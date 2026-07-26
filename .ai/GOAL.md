# Goal: Capability-Based Filesystem Abstraction

## Objective

Build a production-grade open-source filesystem package inspired by Flysystem:
one coherent API for local, memory, S3, Cloudflare R2, SFTP, and FTP storage,
without pretending that incompatible backends have identical guarantees.

## Initial Adapters

- Local filesystem.
- Deterministic in-memory filesystem for tests.
- Amazon S3 through AWS SDK for Go v2.
- Cloudflare R2 as a first-class adapter/profile with explicit capability and
  configuration differences despite its S3-compatible transport.
- SFTP through `github.com/pkg/sftp` and `golang.org/x/crypto/ssh`.
- FTP through a maintained, narrowly isolated client dependency.
- Google Cloud Storage and Azure Blob Storage are explicitly out of scope for
  the initial release.

## API And Capability Model

- Define small capabilities for read, write, delete, list, stat, copy, move,
  ranges, metadata, checksums, temporary URLs, and visibility where supported.
- Use streaming `io.Reader`/`io.Writer` APIs and avoid mandatory whole-object
  buffering.
- Interoperate with `io/fs` where its read-only model fits.
- Normalize logical paths and reject traversal, root escape, invalid segments,
  and backend-specific ambiguity.
- Publish a capability matrix for atomicity, rename, consistency, ranges,
  checksums, metadata, multipart upload, and temporary URLs.
- Unsupported operations MUST return typed capability errors, never silently
  emulate unsafe semantics.

## Package Shape

- Root package: capability interfaces, path types, metadata, options, errors.
- `local`, `memory`, `s3`, `r2`, `sftp`, and `ftp` adapter packages.
- `fstest`: reusable conformance suite and fault-injection helpers.
- Optional decorators for prefixing, read-only access, checksums, retries, and
  instrumentation.

## Quality Requirements

- Meaningful 100% statement coverage is required across core and adapters.
- Every adapter MUST pass the shared conformance suite plus backend-specific
  tests against real compatible services where practical.
- Race tests MUST cover shared clients, streaming, retries, and cancellation.
- Fuzz tests MUST cover paths, keys, metadata, listings, and malformed server
  responses.
- Benchmarks MUST cover streaming throughput, allocation, listing, and large
  object behavior without embedding unstable network expectations.

## Documentation Deliverables

- Complete API documentation and an adapter capability matrix.
- Adoption guides and examples for local, memory, S3, R2, SFTP, and FTP.
- Guides for credentials, streaming, retries, consistency, multipart uploads,
  temporary URLs, Kubernetes, migration between adapters, and testing.
- Architecture, security, troubleshooting, FAQ, compatibility, contribution,
  and maintained `CHANGELOG.md` documentation.

## Automation And Release

GitHub Actions MUST run formatting, vetting, linting, tests, race tests, fuzz
smoke tests, coverage enforcement, vulnerability scanning, examples, API
compatibility, and adapter integration matrices with pinned service versions.

## Phases

1. Specify capabilities, path rules, errors, and conformance contracts.
2. Implement core, memory, and local adapters.
3. Implement S3 and first-class R2 adapters.
4. Implement SFTP and FTP adapters with reconnect and cancellation semantics.
5. Complete hostile-input, performance, security, and adoption documentation.

## Acceptance Criteria

- Every adapter passes common and backend-specific conformance tests.
- Backend differences are exposed through capabilities rather than hidden.
- Streaming, cancellation, cleanup, and credential handling are production-safe.
- Meaningful 100% coverage and all GitHub Actions gates pass.
- Documentation enables adoption without source inspection and `CHANGELOG.md`
  is current.
