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
- FTPS verifies certificates; plaintext FTP requires an explicit opt-in.
- R2 custom endpoints are validated to reduce credential disclosure and SSRF
  risk. S3 clients remain caller-configured, so endpoint control is trusted.
- Listing limits and streaming APIs bound attacker-controlled resource use.
- Secrets and signed URLs must not be placed in paths, errors, or logs.

Review endpoint allowlists, DNS behavior, proxy settings, credential scope,
and egress policy before accepting storage configuration from another tenant.
