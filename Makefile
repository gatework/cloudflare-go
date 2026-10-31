GO ?= go
VERSION ?= dev
COMMIT ?= $(shell git rev-parse HEAD)
OS ?= $(shell $(GO) env GOOS)
ARCH ?= $(shell $(GO) env GOARCH)
.PHONY: build test race vet bench package
build:
	$(GO) build -trimpath -ldflags '-s -w -X github.com/gatework/cloudflare-go/internal/cli.Version=$(VERSION) -X github.com/gatework/cloudflare-go/internal/cli.Commit=$(COMMIT)' -o bin/cloudflare-go ./cmd/cloudflare-go
test:
	$(GO) test ./...
race:
	$(GO) test -race -timeout 5m ./...
vet:
	$(GO) vet ./...
bench:
	$(GO) test ./internal/output ./internal/cloudflare -run '^$$' -bench 'BenchmarkDump|BenchmarkResponseValidation' -benchmem -count=5
package:
	python3 scripts/package.py build '$(VERSION)' '$(COMMIT)' '$(OS)' '$(ARCH)'
