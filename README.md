# GarmX

> Project Status: early stage release. The design is settled; implementation is in progress; early build is usable and available

**GarmX** is the local-first observability & audit plane for MCP usage — see, audit, and trust what your AI coding agents and apps actually do across every tool. OTel export. All in a single lean Go binary.

To any AI client, GarmX presents itself as **one** MCP server — it merges every
registered upstream's tools into a single catalog, routes each MCP call to the right
server, and records every transaction along the way.

## Main value — observability & audit

Every AI client -> MCP transaction is captured, loged for auditing per client, can be vieved in a lean web UI, and exported over **OpenTelemetry** to Grafana / Prometheus / Loki and any platform which supports OTLP.

This is cross-server, cross-agent view.

**Also:**

- **Consolidate** — `garmx import` sweeps servers scattered across your client
  configs into one catalog; you then point every AI client at just `garmx`.
- **Curate** — per-agent profiles can expose the right number of tools you choose, not all of them at once: better tool-selection, lower token cost.
- **Safe by default** — secrets and other sensitive data you choose are redacted before they reach the audit store; `garmx` daemon only binds to `127.0.0.1`


## Quick start

```sh
make build          # compile to bin/garmx
./bin/garmx -h      # usage
make check          # full validation gate (run after every change)
make run            # build + run the daemon
```

The `garmx` daemon binds `127.0.0.1:9735` by default — it holds every upstream's
credentials and must not be exposed publicly.

## Development

for developers, make includes preconfigured methods to validate, test and build.

`make check` (`fmt` → `lint` → `vet` → `test` → `build`) is the gate; validate project with it after every
change. Dev tools (gofumpt, golangci-lint, templ) are invoked via `go run` with
pinned versions, so no global installs are required.

Standards and conventions are outlined in: [`AGENTS.md`](AGENTS.md).

## Documentation

Concepts, design discussions and implementation plan:

- [`docs/architecture.md`](docs/architecture.md) — system design, aggregation
  model, package layout, schemas.
- [`docs/implementation.md`](docs/implementation.md) — phased build and implementation plan.
- [`docs/discovery.md`](docs/discovery.md) — open research, decisions, and scope management.
