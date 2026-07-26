# Contributing

Create a conventional branch from `main`, add tests before behavior changes,
and keep commits focused. Public APIs require documentation and a capability
matrix update. An adapter must return typed unsupported errors rather than
emulate a weaker operation under a stronger name.

Before opening a pull request, run:

```sh
gofmt -w <changed-go-files>
go vet ./...
go test ./...
go test -race ./...
go test -run '^$' -fuzz '^FuzzParsePath$' -fuzztime=10s .
go test -run '^$' -fuzz '^FuzzSymlinkContainment$' -fuzztime=10s ./local
go test -run '^$' -fuzz '^FuzzMetadataAndListings$' -fuzztime=10s ./memory
go test -run '^$' -fuzz '^FuzzLogicalKeyTranslation$' -fuzztime=10s ./s3
go test -run '^$' -fuzz '^FuzzMalformedListingEntry$' -fuzztime=10s ./ftp
go test -run '^$' -fuzz '^FuzzMalformedListingInfo$' -fuzztime=10s ./sftp
go test -run '^$' -fuzz '^FuzzErrorRedaction$' -fuzztime=10s ./internal/redact
scripts/check-coverage.sh
```

New adapters must pass `fstest.TestFilesystem`, document credentials and
failure semantics, include real-service tests where practical, fuzz protocol
translation, and benchmark deterministic streaming/listing paths.
