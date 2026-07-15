# Adapter adoption guide

## Memory

Use `memory.New()` for deterministic tests and ephemeral data. It is safe for
concurrent use and returns immutable read snapshots. `memory.WithClock` makes
timestamps deterministic.

```go
store := memory.New()
```

## Local

Call `local.New(root)` and close the returned adapter. The default policy
rejects symbolic links and creates files/directories with `0600`/`0700`
permissions. `local.AllowInternalSymlinks` permits links only when the opened
root can still contain the operation. Never derive `root` from an untrusted
request.

```go
store, err := local.New("/var/lib/application/files")
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
)
```

## Cloudflare R2

Use `r2.New` with the 32-character account ID, bucket, and scoped R2 API
credentials. The adapter derives and validates the HTTPS account endpoint and
uses SigV4 region `auto`. A custom endpoint is rejected if it contains user
information, paths, queries, or fragments. Development HTTP endpoints require
the explicit development option.

R2 is a separate profile, not an alias for S3. Consult `Adapter.Profile()` for
endpoint, path-style, ACL, copy-checksum, and multipart differences.

```go
store, err := r2.New(ctx, r2.Config{
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
production. Set a rooted absolute server directory and close the adapter.

The adapter rejects symlink traversal. Read-safe opens may reconnect once.
Writes are never replayed after an uncertain failure. Move is advertised only
when the server negotiates the OpenSSH POSIX rename extension.

```go
hostKeys, err := knownhosts.New("/etc/application/ssh_known_hosts")
if err != nil {
    return err
}
store, err := sftp.New(ctx, sftp.Config{
    Address:         "files.example.com:22",
    User:            "application",
    Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
    HostKeyCallback: hostKeys,
    Root:            "/srv/application",
})
```

## FTP and FTPS

FTP sends credentials in clear text unless TLS is enabled. The default is
explicit FTPS and requires a verifying `tls.Config`. Implicit FTPS is also
available. Plain FTP requires `AllowPlaintext: true` and should only be used on
a separately protected network.

Choose passive mode unless the network requires active mode. Machine-readable
MLSD/MLST listings are preferred, with bounded legacy-listing fallback. The
control session is serialized. Read-safe operations may reconnect once;
writes are never replayed. Cross-platform copy, move, ranges, metadata,
checksums, URLs, and visibility are not advertised.

```go
store, err := ftp.New(ctx, ftp.Config{
    Address:  "files.example.com:21",
    Username: os.Getenv("FTP_USERNAME"),
    Password: os.Getenv("FTP_PASSWORD"),
    Root:     "/application",
    TLSMode:  ftp.TLSExplicit,
    TLSConfig: &tls.Config{
        MinVersion: tls.VersionTLS12,
        ServerName: "files.example.com",
    },
})
```

Each constructor returns an error before use when required security or bounds
are missing. S3, R2, SFTP, FTP, and local adapters own resources and should be
closed where their type exposes `Close`.
