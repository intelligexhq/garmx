# GarmX Makefile.
#
# `make check` is the validation gate — run it after every change (see AGENTS.md).
# Dev tools (gofumpt, golangci-lint, templ) are invoked via `go run` with pinned
# versions so the targets work without global installs.

# Pinned tool versions (bump deliberately).
GOFUMPT_VERSION      := v0.9.1
GOLANGCI_LINT_VERSION := v2.6.1
TEMPL_VERSION        := v0.3.960

BIN     := bin/garmx
PKG     := ./...
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

GOFUMPT      := go run mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
TEMPL        := go run github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)
# In-repo Markdown normalizer (see tools/mdlint); no version pin — it is source.
MDLINT       := go run ./tools/mdlint

.DEFAULT_GOAL := check

.PHONY: check fmt fmt-check fmt-md lint lint-md vet test build templ run dev clean coverage tidy tools-help

## check: full validation gate — fmt-check → lint-md → lint → vet → test → build.
check: fmt-check lint-md lint vet test build

## fmt: format all Go files with gofumpt.
fmt:
	$(GOFUMPT) -l -w .

## fmt-check: fail if any Go file is not gofumpt-formatted (used by check/CI).
fmt-check:
	@out="$$($(GOFUMPT) -l .)"; \
	if [ -n "$$out" ]; then \
		echo "not gofumpt-formatted:"; echo "$$out"; \
		echo "run 'make fmt'"; exit 1; \
	fi

## fmt-md: rewrite Markdown files to canonical form.
fmt-md:
	$(MDLINT) -fix .

## lint-md: fail if any Markdown file is not canonical (used by check/CI).
lint-md:
	$(MDLINT) .

## lint: run golangci-lint.
lint:
	$(GOLANGCI_LINT) run $(PKG)

## vet: run go vet.
vet:
	go vet $(PKG)

## test: run all tests under the race detector.
test:
	go test -race -count=1 $(PKG)

## build: compile the garmx binary into bin/.
build:
	go build $(LDFLAGS) -o $(BIN) ./cmd/garmx

## templ: regenerate Templ (.templ.go) files. Run before build once the UI exists.
templ:
	$(TEMPL) generate

## run: build and run the daemon.
run: build
	./$(BIN)

## dev: rebuild and re-run on file changes (requires entr: `brew install entr`).
dev:
	@command -v entr >/dev/null || { echo "entr not found: brew install entr"; exit 1; }
	find . -name '*.go' | entr -r $(MAKE) run

## coverage: run tests with a coverage profile and print the summary.
coverage:
	go test -race -covermode=atomic -coverprofile=coverage.out $(PKG)
	go tool cover -func=coverage.out | tail -1

## tidy: tidy go.mod/go.sum.
tidy:
	go mod tidy

## clean: remove build artifacts.
clean:
	rm -rf bin coverage.out

## tools-help: list the pinned dev-tool versions.
tools-help:
	@echo "gofumpt        $(GOFUMPT_VERSION)"
	@echo "golangci-lint  $(GOLANGCI_LINT_VERSION)"
	@echo "templ          $(TEMPL_VERSION)"
