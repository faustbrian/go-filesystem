# Architecture

The root package contains value types and narrow operation interfaces. It has
no dependency on an adapter. Adapters translate logical paths and portable
errors into one backend protocol and advertise only guarantees they can keep.

`Path` is the trust boundary: callers parse external names once, and adapters
receive normalized root-relative values. A zero `Path` is the logical root for
relationship and listing operations; it is never an object name.

Streams are owned explicitly. Reads return `io.ReadCloser`; writes consume an
`io.Reader`; listings return closeable iterators. This permits bounded memory,
cancellation, paging, and transport cleanup.

Capabilities are data as well as Go interfaces because some backends can only
determine support after connection negotiation. Typed unsupported errors make
that distinction observable. Backend-native guarantees are not synthesized
when doing so could hide partial writes or non-atomic mutation.

The S3 and R2 packages share a transport implementation, while R2 owns its
configuration validation and semantic profile. FTP and SFTP dependencies are
isolated behind internal session interfaces so protocol faults can be tested
without leaking dependency types into the public API.

The optional `decorator` package wraps the common adapter surface. Prefixing
translates paths at the trust boundary, read-only mode filters capabilities,
checksums explicitly stream a full read, retries are restricted to read-safe
setup, and instrumentation reports caller-visible logical operations.
