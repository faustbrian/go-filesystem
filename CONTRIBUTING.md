# Contributing

## Before Editing

1. Read [`AGENTS.md`](AGENTS.md) and the affected module's docs.
2. Run standalone Go tests in the affected module.
3. Identify owned dependencies and reverse dependants in `modules.json`.
4. Preserve unrelated work and generated/corpus provenance.

## Changes

Keep commits focused and conventional. Update every affected changelog with
the behavior and migration impact. Public API changes require compatibility
evidence and documentation. Specification behavior requires a decision record,
fixture coverage, and interoperability evidence.

New direct dependencies and dependency updates must follow the
[dependency governance policy](AGENTS.md#dependencies-and-supply-chain). Package-local
update bots are forbidden; the root policy owns every module and action update.

Specification-backed changes must follow the
[specification governance contract](AGENTS.md#design), update
the affected stable decision entries, and complete the Specification Decisions
section of the pull request template. An unresolved interpretation or stale
source pin is release-blocking; peer behavior cannot silently select policy.

Selected full mutation qualification must finish with zero surviving viable
mutants. Local mutation is optional, not a universal contribution prerequisite.

Do not fork shared verification workflows, add permanent replacements, machine-specific
paths, bypass flags, broad mutation exclusions, or aggregate quality metrics
that hide a failing package.

## Verification

Use Go 1.27.2 for development and CI; module minima remain Go 1.27.0. Test
the root independently of the workspace and sibling checkouts:

```bash
GOWORK=off go test ./...
```

Ordinary tests run deterministic fuzz seeds and saved corpus regressions, not
active exploration. Neither owned module declares services or test/build tags.
The root's live S3/R2 tests skip without `S3_INTEGRATION_ENDPOINT`; that pass
does not prove compatible-service conformance. See the
[verification matrix](docs/verification.md) for the explicit live boundary.
The non-releasable [tabular ingestion module](integration/tabular-ingestion/README.md)
has its own standalone public-dependency test command.

With the checksum-pinned public verifier selected by `.golib.yaml`, use:

```bash
make inventory
make check
make ci
```

Both Make routes use bounded local checks. Base-free checks run seeds/corpora;
changed-source CI selects bounded active smoke for causally affected targets.
Ordinary CI consumes the public verifier without building it from source. A
single local race selects create-only publication among 16 joined writers,
requiring one success and 15 precondition failures, once with a two-minute bound.

Short active exploration selects one target; optional local mutation selects
one package:

```bash
GOMAXPROCS=2 GOWORK=off go test -run '^$' -fuzz '^FuzzParsePath$' -fuzztime=2s -parallel=1 -timeout=2m .
golib mutation --module . --package ./local
```

`make check-full` retains all seven native 10000x fuzz campaigns, local/memory
streaming and listing benchmarks, and full coverage, race, mutation, API,
security and documentation qualification. Filesystem containment, atomic
publication, stream/session ownership and adapter changes retain their affected
fault and conformance tests; service qualification needs a disposable live
endpoint. Public API changes retain compatibility and directly affected public
consumers, including the owned ingestion module. Release rehearsal remains
`golib release dry-run --module .`. Report unavailable, skipped and failed
boundaries honestly; local tests are not hosted-CI or public-release proof.

## Selected Scheduled Assurance

The separate [selected-assurance caller](.github/workflows/assurance.yml) runs
only on main, every Thursday at 04:11 UTC. It rotates one current declared root
fuzz target for 30 seconds, exploring:
logical path normalization, symlink containment, metadata ownership and
listing limits, adapter input translation and error redaction.
The seven targets use local temporary files, memory or fake backend/session
inputs; they do not qualify live S3/R2, FTP or SFTP services. The owned ingestion
module and explicit live-endpoint qualification remain separate.

Base-free ordinary checks execute deterministic seeds, not active exploration;
committed-range CI selects affected active smoke. Manual assurance requires an
exact anchored selector from the root fuzz operation in `.golib.yaml`, for
example `^FuzzSymlinkContainment$`.
Empty, unknown and malformed selections refuse.
The shared job bounds setup and execution to ten minutes, active workers and
CPU to two, owned memory to 2 GiB, native output to 1 MiB and private-cache
lifetime. Failure, cancellation, exhausted budgets and cleanup refusal remain
attributable failures. This lane does not run mutation or replace Required,
explicit `make check-full`, or relevant release qualification.

## Adding A Module

Follow [repository structure policy](AGENTS.md#repository-structure). New modules
require an explicit purpose, ownership boundary, dependency review, package
catalog entry, full quality gates, documentation, changelog, license, security
policy, compatibility plan, and release dry-run.

## Source Documentation

Install the checksum-pinned public verifier declared in `.golib.yaml`.
`make source-docs-check` validates objective Go comment, marker, link and
generated-producer structure; `make check`, `make check-full` and `make ci`
require it before their existing checks. Shared CI runs the same strict
contract and includes its result in Required. This is structural enforcement,
not a substitute for source-truth review or behavioral verification.
