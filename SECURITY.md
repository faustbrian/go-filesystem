# Security policy

## Reporting

Do not open a public issue for a suspected vulnerability. Send a private
report through [GitHub private vulnerability reporting](https://github.com/faustbrian/go-filesystem/security/advisories/new)
with the affected version, reproduction, and impact.

## Security model

- Parse untrusted object names with `ParsePath`; never concatenate OS or remote
  paths outside an adapter.
- Local storage denies symlinks by default and uses an opened root to contain
  filesystem operations.
- SFTP requires explicit host-key verification. FTP and SFTP reject observed
  links, but cannot atomically combine that check with path use. Their default
  root is `/`: the authenticated server namespace, not a client subdirectory,
  is the confinement boundary. Non-root directories require
  `AllowUnsafeSubroot` and server-side isolation.
- FTPS currently fails before dialing because protected data transfers are not
  verified with the pinned client. Plaintext FTP requires an explicit opt-in
  and an independently encrypted network.
- R2 custom endpoints are validated to reduce credential disclosure and SSRF
  risk. S3 clients remain caller-configured, so endpoint control is trusted.
- Listing limits, S3/R2 metadata limits, and streaming APIs bound
  attacker-controlled resource use.
- Unclassified S3/R2 errors redact URL user information, query strings,
  authentication headers, and known R2 credentials while preserving the error
  cause. Applications must still avoid logging returned temporary URLs.

Review endpoint allowlists, DNS behavior, proxy settings, credential scope,
and egress policy before accepting storage configuration from another tenant.

## Review evidence

| Threat | Control | Executable evidence |
|---|---|---|
| Traversal and root escape | strict logical paths and `os.Root` | path fuzzing and concurrent local symlink replacement |
| Symlink redirection | local rooted operations; remote server namespace confinement | remote constructor rejection and check/use characterization tests |
| Credential disclosure | endpoint validation and error redaction | R2 endpoint matrix and redaction fuzz corpus |
| Endpoint SSRF | HTTPS account endpoints; loopback-only development override | R2 endpoint validation tests |
| Partial publication | temporary files or multipart completion | failure cleanup and MinIO orphan checks |
| Resource exhaustion | listing and metadata ceilings | hostile pagination/listing/metadata tests and allocation benchmarks |

S3 accepts a caller-created AWS SDK client, so its endpoint, DNS resolution,
proxy, credentials, region, and retry policy are part of the caller's trusted
configuration boundary. R2 owns those choices and rejects endpoint credentials,
queries, fragments, non-root paths, non-HTTPS production URLs, and non-loopback
HTTP development endpoints.

### Remote subdirectory risk

An administrator or another remote writer can replace a checked ancestor with
a symbolic link before FTP/SFTP uses it. Client preflight checks do not prevent
this race. The deploying application and storage administrator own this risk
when explicitly setting `AllowUnsafeSubroot`: jail the account to the intended
namespace or enforce server ACLs/ownership preventing hostile path mutation.
Keep the option false otherwise. Reassess the opt-in when server isolation or
ownership changes, or a supported protocol exposes an atomic no-follow,
directory-relative operation.

See the scoped [remote confinement threat model](docs/security-threat-model.md).
