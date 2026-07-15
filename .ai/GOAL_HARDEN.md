# Hardening Goal: Filesystem Abstraction

## Objective

Prove safe behavior across hostile paths, unreliable networks, partial writes,
backend incompatibilities, and large streaming workloads.

## Required Audits

- Fuzz path normalization, separators, Unicode, traversal, root escape, empty
  segments, symlinks, and platform-specific local paths.
- Test short reads/writes, interrupted streams, cancellation, timeout, reconnect,
  retry safety, and cleanup of partial or multipart uploads.
- Verify S3 and R2 independently for endpoint, signing, region, checksum,
  multipart, conditional operation, metadata, and temporary URL behavior.
- Exercise SFTP host-key verification, authentication, reconnects, server limits,
  rename semantics, and connection loss.
- Exercise FTP passive/active behavior, TLS modes, reconnects, path encoding,
  partial transfer, and servers with inconsistent feature support.
- Verify local symlink policy, permissions, race-resistant root containment,
  atomic replacement, and concurrent access.
- Ensure credentials, signed URLs, headers, and remote errors are redacted.
- Prove listings and metadata are bounded under hostile remote responses.

## Required Deliverables

- Shared adapter conformance and backend-specific integration matrices.
- Fault proxies and fixtures for disconnects, truncation, latency, and corruption.
- Path and protocol fuzz corpora.
- Large-object streaming and allocation benchmarks.
- Security review covering traversal, symlinks, credentials, and SSRF-style
  endpoint configuration risks.
- Updated capability matrix, operations docs, FAQ, and `CHANGELOG.md`.

## Release Blockers

- Root escape, unsafe symlink traversal, credential leakage, or data corruption.
- Silent success after partial writes or unsupported operations.
- Unbounded buffering, listing, retry, or multipart resource leakage.
- False capability claims for any adapter, especially S3 versus R2.
- Missing Meaningful 100% coverage or a failing GitHub Actions gate.

## Completion Criteria

- Every adapter passes conformance and backend-specific failure testing.
- Race, fuzz, security, compatibility, and performance gates pass.
- Capability and consistency guarantees match observed backend behavior.
- No release blocker remains and `CHANGELOG.md` is current.
