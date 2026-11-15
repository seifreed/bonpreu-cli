# bonpreu-cli — build & maintenance.
#
# Two build configurations:
#   default          dependency-free static binary (no chromedp, no Chrome at runtime)
#   browserlogin     adds `bonpreu login` (open-browser capture) via chromedp; needs Chrome
#
# `make check` builds and vets BOTH so a chromedp-side break can't slip through a
# release that only ever built the default binary.

BIN ?= bonpreu
GOVULNCHECK ?= $(shell go env GOPATH)/bin/govulncheck

.PHONY: build build-browserlogin test fmt vet lint vulncheck tidy check clean

build:
	go build -o $(BIN) ./cmd/bonpreu

build-browserlogin:
	go build -tags browserlogin -o $(BIN) ./cmd/bonpreu

test:
	go test ./...

fmt:
	@test -z "$$(gofmt -l .)" || { echo "unformatted:"; gofmt -l .; exit 1; }

vet:
	go vet ./...
	go vet -tags browserlogin ./...

lint:
	golangci-lint run ./cmd/... ./internal/...
	golangci-lint run --build-tags browserlogin ./cmd/... ./internal/...

vulncheck:
	$(GOVULNCHECK) ./...

# go mod tidy (Go 1.26) scans all build tags, so it keeps the tag-only chromedp
# require — no special flags needed.
tidy:
	go mod tidy

# Pre-release gate: formatting, both vet/lint configs, tests, builds, and known-vulnerability scan.
check: fmt vet test build build-browserlogin lint vulncheck
	@echo "ok — default + browserlogin both build"

clean:
	rm -f $(BIN)
