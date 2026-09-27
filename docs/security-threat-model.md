# Remote filesystem confinement threat model

This model covers FTP/SFTP path confinement in v2. It is not a claim that all
filesystem or ecosystem security criteria have been verified.

## Trust boundary

Logical paths are caller-controlled and parsed by `filesystem.ParsePath`.
Remote paths and their resolution are owned by the authenticated server. A
remote writer may replace directories or symbolic links between protocol
commands. Assets include content accessible to the authenticated account and
content in neighbouring directories that a configured client subdirectory might
otherwise appear to exclude.

## Attack and default control

FTP `Stat` and SFTP `Lstat` are separate from the following read, write, delete,
listing, or rename operation. A successful no-link preflight is not a
confinement guarantee: an ancestor can be replaced before path use. Adapter
locks serialize owned operations, not other server processes or clients.

Both public constructors reject canonical remote roots other than `/` before
network acquisition unless `AllowUnsafeSubroot` is explicitly enabled. With
root `/`, the authenticated server namespace is the boundary; deployments must
jail accounts or enforce the access policy required for their data. Existing
link checks remain defense in depth against stable links.

## Explicitly unsafe mode

`AllowUnsafeSubroot` acknowledges that client subdirectory confinement cannot
be guaranteed. Its owner is the deploying application and storage
administrator. Its rationale is interoperability with servers that enforce
the intended boundary themselves. Mitigate with server jailing, or ACLs and
ownership preventing hostile ancestor mutation. Reassess when those server
controls change or atomic no-follow, directory-relative protocol operations
become available. Do not enable the option for an untrusted shared writer.

## Evidence

The FTP and SFTP `TestOpenRejectsUnisolatedSubroot` tests detect the former
default and canonicalized-root variants. Root and explicit-unsafe acceptance
tests preserve deliberate connection acquisition. Each adapter's
`TestRemoteLinkPreflightIsNotConfinementBoundary` deterministically changes
server-side resolution after preflight and observes outside content, proving
why a client check cannot substitute for the server boundary. Concrete
loopback transfer/conformance tests cover each pinned protocol client; the
SFTP fixture's explicit unsafe root has no untrusted concurrent path writers.
