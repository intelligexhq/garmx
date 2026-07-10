# Contributing to GarmX

Contributions are welcome — `GarmX` is actively looking for contributors and early
adopters.

## Development workflow

The `Makefile` provides the full validation, test, and build tooling.

```sh
make build          # compile to bin/garmx
make check          # fmt → lint → vet → test → build (the quality gate)
make run            # build + run the daemon
```

Run `make check` after every change — it is the gate that CI enforces. Dev tools
(`gofumpt`, `golangci-lint`, `templ`) are invoked via `go run` with pinned
versions, so no global installs are required.

## Standards

Coding standards and conventions are documented in [`AGENTS.md`](AGENTS.md).
Design context lives in the [`docs/`](docs/) directory — start with
[`docs/architecture.md`](docs/architecture.md).

## Submitting changes

1. Branch off `main`.
2. Make your change and ensure `make check` passes.
3. Open a pull request describing the change.

Thanks for helping me build GarmX.
