# Security policy

## Reporting

Do not open a public issue for a suspected vulnerability. Send a private
report to the repository owner with the affected version, reproduction, and
impact. Until a dedicated address is published, use GitHub private
vulnerability reporting when enabled.

## Security model

- Parse untrusted object names with `ParsePath`; never concatenate OS or remote
  paths outside an adapter.
- Local storage denies symlinks by default and uses an opened root to contain
  filesystem operations.
- SFTP requires explicit host-key verification and rejects symlink traversal.
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
| Symlink redirection | deny-by-default local/SFTP policies | adapter mutation, listing, and fuzz tests |
| Credential disclosure | endpoint validation and error redaction | R2 endpoint matrix and redaction fuzz corpus |
| Endpoint SSRF | HTTPS account endpoints; loopback-only development override | R2 endpoint validation tests |
| Partial publication | temporary files or multipart completion | failure cleanup and MinIO orphan checks |
| Resource exhaustion | listing and metadata ceilings | hostile pagination/listing/metadata tests and allocation benchmarks |

S3 accepts a caller-created AWS SDK client, so its endpoint, DNS resolution,
proxy, credentials, region, and retry policy are part of the caller's trusted
configuration boundary. R2 owns those choices and rejects endpoint credentials,
queries, fragments, non-root paths, non-HTTPS production URLs, and non-loopback
HTTP development endpoints.
