#!/usr/bin/env bash
# Total statement coverage including main(), which `go test -coverprofile` cannot
# credit (its os.Exit can't run in-process). Uses Go 1.20+ integration coverage:
# unit-test coverage is merged with a coverage-instrumented run of the real binary.
set -euo pipefail
cd "$(dirname "$0")"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/unit" "$work/bin"

# Unit tests, emitting coverage in the binary (covdata) format.
go test -cover -coverpkg=./... ./... -args -test.gocoverdir="$work/unit" >/dev/null

# Coverage-instrumented binary; run it to exercise main() and the dispatch.
go build -cover -coverpkg=./... -o "$work/bonpreu" ./cmd/bonpreu
GOCOVERDIR="$work/bin" "$work/bonpreu" help >/dev/null 2>&1 || true
GOCOVERDIR="$work/bin" "$work/bonpreu" version >/dev/null 2>&1 || true
GOCOVERDIR="$work/bin" "$work/bonpreu" nope >/dev/null 2>&1 || true
GOCOVERDIR="$work/bin" BONPREU_BASE_URL=http://127.0.0.1:1 \
	"$work/bonpreu" search x >/dev/null 2>&1 || true

echo "== merged coverage (unit tests + real binary) =="
go tool covdata percent -i="$work/unit,$work/bin"
