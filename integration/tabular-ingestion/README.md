# Filesystem and tabular ingestion

This non-releasable integration module is the executable reference for
streaming a filesystem object into bounded tabular parsing. Applications import
the two public libraries independently; this module is evidence and example
code, not a framework bootstrap or application dependency.

## Supported stack

The verified version set is:

- `github.com/faustbrian/go-filesystem` v1.1.0; and
- `github.com/faustbrian/go-tabular` v1.0.0.

The application depends on both modules. `filesystem` does not import
`tabular`, and `tabular` does not import a filesystem adapter. A local, memory,
S3, R2, SFTP, or FTP adapter may satisfy `filesystem.Reader`; adapter
construction and credentials remain outside this composition.

## Construction and lifecycle

1. The application validates positive total-object, record, field, and row
   bounds before opening the object.
2. `filesystem.Reader.Open` acquires the source stream with the caller's
   context. A successful stream is owned by the composition.
3. A context-aware total-byte boundary wraps the stream before
   `tabular.NewCSVReader` applies record and field bounds.
4. The parser validates and consumes a required header, then delivers one data
   row at a time to the application callback.
5. Cancellation is checked before reads and before delivering a buffered row.
6. Parsing and callbacks stop before the source stream is closed. The owning
   adapter or network session is shut down separately after all ingestions.

There is no middleware, background goroutine, transaction, acknowledgement, or
automatic retry in this stack. A caller that acknowledges a job or object does
so only after `IngestCSV` succeeds. Retrying, quarantining, or resuming an input
is application policy and must account for any callback side effects already
completed before an error.

## Errors and ownership

- filesystem opening errors retain their filesystem or adapter classification;
- malformed headers and records retain `tabular.ErrorKind` classification;
- total-object and row limits use the application-owned
  `ErrObjectTooLarge` and `ErrRowLimitExceeded` categories;
- record and field limits retain `tabular.ErrorLimitExceeded`;
- cancellation and deadlines retain the context cause;
- callback errors remain application-owned; and
- close failures are joined with any earlier failure so neither outcome is
  discarded.

Configuration, backend credentials, correlation, logging, tracing, metrics,
and data-disclosure policy remain application-owned. The composition adds no
globals and does not log fields or payloads.

## Executable evidence

The tests exercise successful streaming, bounds, parser classification,
cancellation before buffered delivery, callback-before-close order, and joined
parse and close failures through public module APIs.

```sh
cd integration/tabular-ingestion
GOWORK=off go test ./...
```
