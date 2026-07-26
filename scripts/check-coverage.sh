#!/bin/sh
set -eu

packages='.
./memory
./local
./s3
./r2
./sftp
./ftp
./decorator
./internal/redact
./internal/streamwriter'

for package in $packages; do
    profile="${TMPDIR:-/tmp}/filesystem-coverage-$(echo "$package" | tr '/.' '__').out"
    go test -coverprofile="$profile" "$package"
    total=$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%", "", $3); print $3}')
    if [ "$total" != "100.0" ]; then
        echo "coverage for $package is $total%, want 100.0%" >&2
        exit 1
    fi
done
