# Adapter adoption guide

[Documentation index](README.md) · [Repository entry](../README.md)

## Upgrading to v2.0.2

After publication, adopt `github.com/faustbrian/go-filesystem/v2@v2.0.2` or
newer for the local/memory Move and streaming-writer corrections. The module
identity and Go 1.27.0 minimum are unchanged.

Local no-overwrite Move now uses exclusive native rename on Linux, macOS and
Windows. Other targets fail closed with `ErrUnsupportedCapability`; unavailable
kernel/filesystem support can return that classification or a native error.
Advertising Move capability does not guarantee every option is available.
Handle this option-specific failure when offering no-overwrite movement;
overwrite-enabled Move remains available on targets supported by `os.Root`.

Cancellation closes adapter-owned streaming-writer pipes and releases waiting
consumers. Call `Close` and inspect its result to join cleanup and publication.
This does not close borrowed `Write` readers or force blocked callbacks,
mutex acquisition or network backends to stop; those boundaries remain
cooperative. Overwrite-enabled memory self moves preserve their existing object.

## Memory

Use `memory.New()` for deterministic tests and ephemeral data. It is safe for
concurrent use and returns immutable read snapshots. `memory.WithClock` makes
timestamps deterministic. Equal-path `Move` with overwrite enabled is a no-op
that preserves bytes, metadata and timestamps; without overwrite it reports
`ErrAlreadyExists`. Missing sources still report `ErrNotFound`. The callback must be concurrency-safe and must not
reenter the adapter: some operations call it under the write lock.

```go
store := memory.New()
```

## Local

Call `local.Open(ctx, root)` and close the returned adapter. A nil or already
canceled context fails before options are applied or the host filesystem is
accessed. The default policy checks for symbolic links before use; it is not
an atomic no-follow policy under concurrent swaps. The adapter creates
files/directories with `0600`/`0700` permissions.
`local.AllowInternalSymlinks` permits links only when the opened root can still
contain the operation. Never derive `root` from an untrusted request. The
deprecated `local.New` compatibility function delegates with
`context.Background()`.

Local `Move` with overwrite disabled uses a single native exclusive rename on
Linux, macOS and Windows. It preserves a competing destination, concurrent
source replacements, directories and admitted symlink entries. Cross-filesystem
moves fail without a copy/delete fallback. Other targets, or a kernel/filesystem
that cannot provide exclusive rename, fail closed with `ErrUnsupportedCapability`
or the native filesystem error rather than silently enabling replacement. Capability advertisement does not promise
support for every Move option; handle this option-specific error before offering
no-overwrite movement. Overwrite-enabled moves still use `os.Root.Rename` on
all supported targets. Existing symlink-policy and root-containment limits apply.

```go
store, err := local.Open(ctx, "/var/lib/application/files")
if err != nil {
    return err
}
defer store.Close()
```

## Amazon S3

Load AWS configuration with the AWS SDK for Go v2 and pass an `*s3.Client` to
`filesystem/s3.New`. Credentials should come from the SDK credential chain,
workload identity, or a secret manager—not source code. Configure a prefix to
isolate tenants and a maximum listing size appropriate for the application.
User metadata is bounded in both directions at 128 entries and 64 KiB by
default; use `WithMetadataLimits` to select a different trusted ceiling.

S3 copy is server-side. Move is deliberately unsupported because copy plus
delete is not an atomic rename. Writes use the SDK transfer manager and only
become visible after a successful upload or multipart completion.

```go
configuration, err := config.LoadDefaultConfig(ctx)
if err != nil {
    return err
}
store, err := filesystemS3.New(
    s3.NewFromConfig(configuration),
    "application-files",
    filesystemS3.WithPrefix("tenant-123"),
    filesystemS3.WithMetadataLimits(128, 64*1024),
)
```

## Cloudflare R2

Use `r2.Load` with the 32-character account ID, bucket, and scoped R2 API
credentials. The adapter derives and validates the HTTPS account endpoint and
uses SigV4 region `auto`. A custom endpoint is rejected if it contains user
information, paths, queries, or fragments. Development HTTP endpoints require
the explicit development option. `Load` accepts a context because it loads the
AWS configuration and credentials; it does not open an R2 protocol session.

R2 is a separate profile, not an alias for S3. Consult `Adapter.Profile()` for
endpoint, path-style, ACL, copy-checksum, and multipart differences.
`r2.WithMetadataLimits` applies the same inbound and outbound metadata bound
as the S3 transport.

```go
store, err := r2.Load(ctx, r2.Config{
    AccountID:       os.Getenv("R2_ACCOUNT_ID"),
    Bucket:          "application-files",
    AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
    SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
    Prefix:          "tenant-123",
})
```

## SFTP

Provide an address, user, one or more `ssh.AuthMethod` values, and a real
`ssh.HostKeyCallback`. Use `knownhosts.New` for normal deployments. The adapter
rejects a missing callback; `ssh.InsecureIgnoreHostKey` should not be used in
production. Use root `/` inside the server-enforced account namespace and close
the adapter.

The adapter rejects observed links but cannot prevent a concurrent link swap
between preflight and use. FTP and SFTP default to root `/`; a non-root
directory requires `AllowUnsafeSubroot: true` and server-enforced isolation.
The flag acknowledges the limitation, it does not make client checks atomic.
Read-safe opens may reconnect once.
Writes are never replayed after an uncertain failure. Move is advertised only
when the server negotiates the OpenSSH POSIX rename extension.

```go
hostKeys, err := knownhosts.New("/etc/application/ssh_known_hosts")
if err != nil {
    return err
}
store, err := sftp.Open(ctx, sftp.Config{
    Address:         "files.example.com:22",
    User:            "application",
    Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
    HostKeyCallback: hostKeys,
    Root:            "/", // The server must jail this account to its storage namespace.
})
```

## FTP

The pinned protocol client cannot currently complete FTPS data transfers
safely. `TLSExplicit` and `TLSImplicit` therefore fail during construction
before credentials are sent. This is an intentional compatibility guard, not
a transient transfer error. Plain FTP requires `AllowPlaintext: true` and sends
credentials and content in clear text; use it only inside an independently
encrypted, trusted network.

Choose passive mode unless the network requires active mode. Both modes are
covered by concrete plaintext transfer tests. Machine-readable
MLSD/MLST listings are preferred, with bounded legacy-listing fallback. The
control session is serialized. Read-safe operations may reconnect once;
writes are never replayed. Cross-platform copy, move, ranges, metadata,
checksums, URLs, and visibility are not advertised.

```go
store, err := ftp.Open(ctx, ftp.Config{
    Address:  "files.example.com:21",
    Username: os.Getenv("FTP_USERNAME"),
    Password: os.Getenv("FTP_PASSWORD"),
    Root:     "/", // The server must jail this account to its storage namespace.
    TLSMode:        ftp.TLSPlaintext,
    AllowPlaintext: true,
})
```

Each acquisition or load returns an error before external I/O when required
security settings or bounds are invalid. A nil context returns
`filesystem.ErrContextRequired`, and a pre-canceled context performs no dial or
configuration load or local root acquisition. Local, FTP, and SFTP adapters own
their acquired resources; callers must use their immediate `Close` methods. An
HTTP client supplied to R2 is borrowed and remains caller-owned. The deprecated
`local.New`, `ftp.New`, `sftp.New`, and `r2.New` functions delegate to these
replacements for source compatibility.

Choose the next guide in the [documentation index](README.md)
or return to the [repository entry](../README.md) for installation and ownership.
