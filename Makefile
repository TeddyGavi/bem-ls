# Development tasks. CI runs the same targets.

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
STATICCHECK ?= honnef.co/go/tools/cmd/staticcheck@latest
GORELEASER  ?= github.com/goreleaser/goreleaser/v2@latest
BASE    ?= origin/main
TARGET  ?= all

.PHONY: all build test race lint fuzz bench bench-compare vscode vsix nvim-e2e snapshot clean

all: lint race

## build: static binaries for this platform in bin/
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

## lint: formatting, go vet, staticcheck, tidy go.mod
lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	$(GO) run $(STATICCHECK) ./...
	$(GO) mod tidy -diff

fuzz:
	scripts/fuzz.sh

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

## bench-compare: benchstat of BASE (default origin/main) against the working tree
bench-compare:
	scripts/bench-compare.sh $(BASE)

## vscode: install, check and bundle the VS Code extension
vscode:
	cd editors/vscode && npm ci && npm run typecheck && npm test && node scripts/sync-settings.mjs --check && npm run build

## vsix: package VSIX files (TARGET=darwin-arm64, ..., or all) into editors/vscode/vsix/
vsix:
	cd editors/vscode && npm ci && node scripts/package.mjs $(TARGET) $(VERSION)

## nvim-e2e: end-to-end test of the Neovim plugin (needs Neovim 0.12)
nvim-e2e: build
	nvim --headless -u NONE -l editors/nvim/tests/e2e.lua bin/bem-ls

## snapshot: a local goreleaser build into dist/, nothing is published
snapshot:
	$(GO) run $(GORELEASER) release --snapshot --clean

clean:
	rm -rf bin dist editors/vscode/vsix editors/vscode/dist editors/vscode/out editors/vscode/bin
