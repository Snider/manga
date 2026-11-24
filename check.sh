#!/bin/sh
# Run before committing: format, SPDX headers, vet, tests, and a Windows build (where she runs it).
set -e
cd "$(dirname "$0")"
unformatted=$(gofmt -l .)
[ -z "$unformatted" ] || { echo "gofmt needed: $unformatted"; exit 1; }
missing=$(git ls-files -co --exclude-standard "*.go" "*.js" "*.css" "*.html" | xargs grep -L "SPDX-License-Identifier: EUPL-1.2" </dev/null || true)
[ -z "$missing" ] || { echo "SPDX header missing: $missing"; exit 1; }
go vet ./...
go test ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/mangashelf
echo "all good 📚"
