# Compatibility Policy

The repository contains one releasable root Go module and follows semantic
versioning. Root releases use `v<version>` tags on main. The v2 root module is
`github.com/faustbrian/go-filesystem/v2`, without version-specific source
directories or branches. Importers must update the root and adapter imports.

v2 rejects FTP/SFTP roots other than `/` by default. Use a server-enforced
account namespace and `Root: "/"`. For a non-root directory, enable
`AllowUnsafeSubroot` only after enforcing isolation or preventing untrusted
path mutation at the server; client preflight checks cannot prevent link races.

Before `v1`, minor releases MAY contain reviewed breaking changes, but every
break MUST be documented with migration guidance. Patch releases MUST remain
backward compatible. At and after `v1`, incompatible exported API or documented
behavior changes require a new major version.

Compatibility includes exported Go APIs, error classification, serialization,
protocol behavior, persistence schemas, environment variables, command output,
resource ownership, ordering, retry/idempotency semantics, and documented
defaults. A compile-compatible change can still be behaviorally breaking.

Specification-backed modules MUST NOT diverge from their declared standards.
Ambiguities require documented decisions and stable tests. Deprecated APIs
follow [`DEPRECATION.md`](DEPRECATION.md).
