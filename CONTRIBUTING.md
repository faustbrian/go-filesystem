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

Full scheduled and release mutation gates must finish with zero surviving viable
mutants. Local mutation is optional, not a universal contribution prerequisite.

Do not add package-local workflows, permanent replacements, machine-specific
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

## Adding A Module

Follow [repository structure policy](AGENTS.md#repository-structure). New modules
require an explicit purpose, ownership boundary, dependency review, package
catalog entry, full quality gates, documentation, changelog, license, security
policy, compatibility plan, and release dry-run.
