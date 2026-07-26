# Compatibility

The module currently requires Go 1.26. The public API is pre-1.0 and may change
between minor versions; migrations will be recorded in `CHANGELOG.md`.

CI exercises Linux with the toolchain declared in `go.mod`. Local, memory, and
in-process SFTP/FTP compatible-server tests run on every change. The integration
workflow pins a MinIO release and runs both the S3 and first-class R2
constructors through shared conformance against it; live cloud credentials are
not required by pull requests.

Supported protocols are Amazon S3, Cloudflare R2's S3-compatible API, SFTP v3
through `github.com/pkg/sftp`, and explicitly opted-in plaintext FTP through
`github.com/gonzalop/ftp`. Server extensions affect advertised SFTP move
support and FTP machine-listing behavior.

Explicit and implicit FTPS are rejected before dialing. The pinned FTP client
does not provide a verified protected-data-channel path, and accepting those
configurations would make the constructor promise stronger behavior than the
first transfer can deliver.

See [the hardening matrix](docs/hardening.md) for the per-adapter conformance,
integration, fault, fuzz, security, and performance evidence.
