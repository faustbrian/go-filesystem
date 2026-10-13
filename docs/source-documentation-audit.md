# Source documentation audit

[Documentation index](README.md) · [Repository entry](../README.md)

## Scope and reproduction

Prose-only audit of `a51aefa29844341a6f47a3ba828f6a2b883c31c0`.
Reproduce files with `git ls-files '*.go'`, modules with
`git ls-files '*go.mod'`, and assurance entry points with
`rg '^func (Test|Fuzz|Benchmark|Example)' --glob '*_test.go'`.
All 67 Go files were reviewed: 18 implementation/helper files and 49 assurance
files. Every public declaration/field/interface/method and important private
path, IO, lock, stream, session and publication boundary was included.

The root `/v2` module owns nine public packages (root, decorator, fstest, ftp,
local, memory, r2, s3, sftp) and two internal packages (redact, streamwriter).
The separate non-releasable `integration/tabular-ingestion` module consumes
released filesystem v2.0.0 and tabular v2.0.2 without local replacements.
Together they cover 12 source directories. There are no generated Go files,
generation directives or build/platform-tagged sources. Live-service tests use
explicit runtime environment/fixtures rather than declared tags. Saved corpora
are `testdata/fuzz/FuzzParsePath/00d80d4bf8398616` and
`internal/redact/testdata/fuzz/FuzzErrorRedaction/20edd5e5cbb50fe6`; the
FTP lifecycle fixture is generated/owned by its Go test helper, not a maintained
generated source. Root and ingestion go.mod/go.sum inputs remain unchanged.

## Initial gaps and investigated contracts

The source checker initially reported 21 missing interface/field comments in
capability.go, contracts.go and fstest/proxy.go. Existing public comments were
also inspected for stronger claims than the source can support:

- Path parsing is lexical normalization, not authorization or real-path
  containment. It converts backslashes, drops leading/repeated slashes and dot
  segments, rejects parent/control/ASCII drive/UNC syntax, but has no byte cap
  or Unicode normalization. Zero Path is Root; errors can include caller data.
- Context requirements, range EOF/clamping, unsupported-option handling,
  copy/move/preconditions and buffering differ by provider. Successful opens
  transfer handle ownership. A write error is not proof of no publication;
  several providers publish then Stat under the same potentially canceled ctx.
- Local containment is delegated to os.Root. DenySymlinks is a separate
  preflight, not an atomic no-follow policy under swaps; neither policy rejects
  preexisting hard links. Root selection/namespace ownership remain trusted.
  Create-only writes publish with Link, not merely an existence check. Rename,
  unlink-temporary and post-publication Stat failures have different windows.
  File Sync does not include directory fsync/crash-durability proof. Move's
  no-overwrite check is separate from Rename and is not race-free no-clobber.
- Local zero listing limit walks the full tree; memory zero collects all
  matching entries. Positive result limits do not universally bound working
  storage or upstream directory/page buffering. Remote listings are sorted
  collected prefixes, not atomic snapshots of concurrently changing services.
- Memory writes buffer the full object despite a reader-based interface.
  Stored content remains immutable; range readers share private content while
  full readers copy it. Metadata maps are copied, not caller-owned live views.
  Clock hooks are borrowed and some run under the write mutex: no reentrant
  adapter calls. Cancellation cannot preempt mutex waits/source Read. Equal
  source/destination Move with Overwrite true assigns then deletes the same
  key; avoid that input, not a guaranteed no-op.
- OpenWriter owns a pipe consumer goroutine. Close supplies EOF and joins the
  publication result; cancellation alone cannot unblock an already blocked
  source/pipe Read. Producers must close even when abandoning or canceling.
  Caller maps/options remain stable through asynchronous completion. Consumer
  panics are not contained and arbitrary callbacks can block Close indefinitely.
- S3/R2 borrow SDK/HTTP/transfer collaborators; uploaded bytes and SDK body
  lifetimes remain explicit. Multipart cleanup depends on actual SDK options;
  no universal successful abort guarantee is invented. Metadata ceilings do
  not bound SDK response allocation. Move/no-clobber Copy/visibility/checksum
  remain unsupported; Write Visibility is not an ACL guarantee. Presigned URLs
  are sensitive bearer data, and requested lifetime is not availability proof.
- R2 endpoint validation checks syntax/scheme and literal localhost/loopback
  spelling, not DNS answers, redirects or a general SSRF allowlist. Profile
  records configuration/service expectations, not runtime conformance or
  enforced multipart policy against arbitrary transfer options. Config secret
  fields and unwrapped SDK errors are not safe general logging surfaces.
- FTP/SFTP link checks are separate protocol requests. Subroots require unsafe
  opt-in and server confinement; stable-link rejection does not stop swaps.
  FTP supports only explicitly opted-in plaintext; TLS modes fail before dial.
  Passwords are not intentionally added to local messages, but raw peer/client
  errors are not universally redacted by these providers.
- FTP serializes control operations through stream completion. Read setup and
  zero-delivered-byte transfer can retry once; consumed writes/deletes do not.
  Acquisition cancellation can return before a late protocol result is cleaned
  up. Closing a reader/adapter may block on the protocol. SFTP multiplexes a
  shared session, closes per-stream files on cancellation, and negotiates rename
  support at acquisition; post-acquisition controls have no package deadline.
  Sync unsupported is tolerated, not a durable-write guarantee.
- Decorator prefixing changes logical paths, not backend authorization.
  ReadOnly removes compound metadata/visibility capabilities. Retryable
  Checksum can encompass a full read and close, not just setup. Observer runs
  synchronously, is not panic-contained, excludes its own latency, and receives
  raw errors and paths that can contain sensitive data. Returned streams and
  backend shutdown remain caller-owned.
- Fault readers are not concurrency-safe; fault writers/iterators synchronize
  individual calls, not compound cursor protocols. Zero failure thresholds fail
  immediately. Proxy options retain corruption slices; upstream dialing and
  injected sleep can delay joined Close without a caller deadline.
- IOFS uses Background contexts, stricter fs.ValidPath names and synthesized
  directories. It materializes and sorts backend entries and can expose a
  truncated directory if the provider caps its default list. Snapshot metadata
  is not a subsequent transactional stat. Cursor handles are caller-serialized.
- CSV ingestion normalizes header names through tabular, consumes rows
  synchronously and joins stream-close errors. The byte-limit EOF probe may
  consume one extra underlying byte. Prior callback side effects are not rolled
  back; borrowed callbacks/typed-nil providers remain application responsibilities.

These findings are grounded in source and existing path, containment, exclusive
create, publication fault, metadata/list snapshot, remote subroot swap, concrete
transport, SDK body/abort, fixture-lifecycle, decorator retry and ingestion tests.
No runtime repair, new policy, conformance or performance claim is included.

## Native surfaces, variants and remaining questions

`.golib.yaml` configures root `apidiff` at api/v2.txt, not prose snapshots; no
native docs/snapshot generator exists. Render all 12 packages, including both
internal packages and the separate ingestion module. Existing examples and
fixtures are unchanged; no live FTP/SFTP/S3/R2 service qualification follows
from documentation rendering. The seven fuzz targets cover logical paths,
local link containment, memory listings/metadata, provider entry/key translation
and recognized error redaction. Three local/memory benchmarks describe streaming
or listing workloads; no newly measured result is claimed.

Dependency/API/source locks, protocol fixtures, saved fuzz seeds and the remote
confinement threat-model evidence remain unchanged. Deprecated New constructors
retain their existing acquisition compatibility. Unknown rationale is not
invented: race-free local no-clobber Move, same-path memory Move behavior,
strict link-denial policy, universal peer-error sanitization, cancellation-only
writer cleanup, bounded full-tree listing, and atomic cross-provider visibility
would require separately scoped behavior decisions/tests. Root/user worktree
changes are not included. Verification is selected formatting, native structural
preservation/rendering, source/local-link/specification checks, changed-content
secret scans and ordinary complete-diff inspection, not full runtime campaigns.

## Complete tracked file review

| Go file | Contract or assurance purpose |
| --- | --- |
| `capability.go` | advertised capability identity, order, copying and programming errors |
| `capability_test.go` | 5 assurance entry points; capability_test.go provider/input/ownership failure partitions |
| `contracts.go` | provider interfaces, returned resources and optional semantics |
| `contracts_test.go` | 1 assurance entry points; contracts_test.go provider/input/ownership failure partitions |
| `decorator/contracts_test.go` | 0 assurance entry points; decorator provider/input/ownership failure partitions |
| `decorator/decorator.go` | prefix projection, borrowed backend, observer privacy and checksum retry exception |
| `decorator/decorator_test.go` | 11 assurance entry points; decorator provider/input/ownership failure partitions |
| `errors.go` | classification and raw caller-owned labels |
| `errors_test.go` | 1 assurance entry points; errors_test.go provider/input/ownership failure partitions |
| `example_test.go` | 1 assurance entry points; example_test.go provider/input/ownership failure partitions |
| `fstest/conformance.go` | selected portable capability/ownership partitions and caller-owned fixture cleanup |
| `fstest/fault.go` | deterministic thresholds, copied fault data, latency and concurrency differences |
| `fstest/fault_test.go` | 11 assurance entry points; fstest provider/input/ownership failure partitions |
| `fstest/proxy.go` | loopback listener, joined forwarding, borrowed options and unbounded dial/latency |
| `ftp/fixture_lifecycle_test.go` | 3 assurance entry points; ftp provider/input/ownership failure partitions |
| `ftp/ftp.go` | fail-closed TLS, serialized session, stream-owned locks and safe replay boundaries |
| `ftp/ftp_edge_test.go` | 15 assurance entry points; ftp provider/input/ownership failure partitions |
| `ftp/ftp_fuzz_test.go` | 1 assurance entry points; ftp provider/input/ownership failure partitions |
| `ftp/ftp_modes_test.go` | 2 assurance entry points; ftp provider/input/ownership failure partitions |
| `ftp/ftp_test.go` | 11 assurance entry points; ftp provider/input/ownership failure partitions |
| `ftp/subroot_security_test.go` | 3 assurance entry points; ftp provider/input/ownership failure partitions |
| `integration/tabular-ingestion/composition.go` | public-version CSV composition, bounded lookahead, callback/stream ownership |
| `integration/tabular-ingestion/composition_test.go` | 9 assurance entry points; nested public-dependency ingestion and close/cancellation partitions |
| `internal/redact/redact.go` | recognized error-text redaction versus raw unwrap/generic formatting |
| `internal/redact/redact_edge_test.go` | 3 assurance entry points; internal provider/input/ownership failure partitions |
| `internal/redact/redact_test.go` | 4 assurance entry points; internal provider/input/ownership failure partitions |
| `internal/streamwriter/writer.go` | pipe backpressure, worker completion and mandatory producer close |
| `internal/streamwriter/writer_test.go` | 3 assurance entry points; internal provider/input/ownership failure partitions |
| `iofs.go` | strict io/fs names, background contexts, listing synthesis and handle lifetime |
| `iofs_edge_test.go` | 6 assurance entry points; iofs_edge_test.go provider/input/ownership failure partitions |
| `iofs_test.go` | 4 assurance entry points; iofs_test.go provider/input/ownership failure partitions |
| `local/exclusive_create_test.go` | 1 assurance entry points; local provider/input/ownership failure partitions |
| `local/local.go` | os.Root containment, link preflight, publication windows and quiescent close |
| `local/local_benchmark_test.go` | 1 assurance entry points; local provider/input/ownership failure partitions |
| `local/local_concurrency_test.go` | 2 assurance entry points; local provider/input/ownership failure partitions |
| `local/local_edge_test.go` | 11 assurance entry points; local provider/input/ownership failure partitions |
| `local/local_fault_test.go` | 12 assurance entry points; local provider/input/ownership failure partitions |
| `local/local_fuzz_test.go` | 1 assurance entry points; local provider/input/ownership failure partitions |
| `local/local_test.go` | 6 assurance entry points; local provider/input/ownership failure partitions |
| `memory/listing_snapshot_test.go` | 2 assurance entry points; memory provider/input/ownership failure partitions |
| `memory/memory.go` | immutable content snapshots, map ownership, unlimited buffers and self-move caveat |
| `memory/memory_edge_test.go` | 6 assurance entry points; memory provider/input/ownership failure partitions |
| `memory/memory_fuzz_test.go` | 3 assurance entry points; memory provider/input/ownership failure partitions |
| `memory/memory_internal_test.go` | 4 assurance entry points; memory provider/input/ownership failure partitions |
| `memory/memory_test.go` | 4 assurance entry points; memory provider/input/ownership failure partitions |
| `memory/race_test.go` | 1 assurance entry points; memory provider/input/ownership failure partitions |
| `path.go` | lexical normalization, root zero value and untrusted error data |
| `path_fuzz_test.go` | 1 assurance entry points; path_fuzz_test.go provider/input/ownership failure partitions |
| `path_test.go` | 6 assurance entry points; path_test.go provider/input/ownership failure partitions |
| `r2/r2.go` | configuration validation, borrowed HTTP transport and unenforced profile policy |
| `r2/r2_edge_test.go` | 10 assurance entry points; r2 provider/input/ownership failure partitions |
| `r2/r2_integration_test.go` | 1 assurance entry points; r2 provider/input/ownership failure partitions |
| `r2/r2_test.go` | 4 assurance entry points; r2 provider/input/ownership failure partitions |
| `r2/sdk_stream_test.go` | 1 assurance entry points; r2 provider/input/ownership failure partitions |
| `s3/s3.go` | SDK body ownership, transfer/metadata budgets, publication then Stat and paging |
| `s3/s3_edge_test.go` | 11 assurance entry points; s3 provider/input/ownership failure partitions |
| `s3/s3_fuzz_test.go` | 1 assurance entry points; s3 provider/input/ownership failure partitions |
| `s3/s3_integration_test.go` | 1 assurance entry points; s3 provider/input/ownership failure partitions |
| `s3/s3_test.go` | 7 assurance entry points; s3 provider/input/ownership failure partitions |
| `s3/sdk_cleanup_test.go` | 1 assurance entry points; s3 provider/input/ownership failure partitions |
| `s3/sdk_upload_test.go` | 3 assurance entry points; s3 provider/input/ownership failure partitions |
| `sftp/sftp.go` | SSH acquisition cancellation, shared sessions, link races and extension publication |
| `sftp/sftp_edge_test.go` | 16 assurance entry points; sftp provider/input/ownership failure partitions |
| `sftp/sftp_fuzz_test.go` | 1 assurance entry points; sftp provider/input/ownership failure partitions |
| `sftp/sftp_integration_test.go` | 3 assurance entry points; sftp provider/input/ownership failure partitions |
| `sftp/sftp_test.go` | 6 assurance entry points; sftp provider/input/ownership failure partitions |
| `sftp/subroot_security_test.go` | 3 assurance entry points; sftp provider/input/ownership failure partitions |

## Objective Enforcement

The checksum-pinned public Tools v2.4.0 verifier now supplies
`make source-docs-check` as a prerequisite of the existing local and CI Make
routes. Ordinary shared CI selects strict source-documentation checking and
requires its result. Native prerequisites, operation budgets, security and
release controls are retained. Hosted exact-head evidence is recorded by the
coordination ledger; configuration alone does not establish green delivery.

Choose the next guide in the [documentation index](README.md)
or return to the [repository entry](../README.md) for installation and ownership.
