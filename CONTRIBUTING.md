# Contributing to GarmX

Contributions are welcome — `GarmX` is actively looking for contributors and early
adopters. This guide covers licensing, the CLA, and the local workflow.

## License & CLA

GarmX is licensed under the **GNU Affero General Public License v3.0**
([`LICENSE`](LICENSE)). Because the project may offer a commercial exception
alongside the AGPL, contributors must agree to the
[Contributor License Agreement](CLA.md) before their contribution can be merged.

Signing is a one-time step: in your first pull request, add the following line to
the PR description:

> I have read the CLA document and I hereby sign the CLA.

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
3. Open a pull request describing the change, and include the CLA sign-off line
   in your first PR.

Thanks for helping me build GarmX.
