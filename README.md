# GarmX

A local-first **MCP aggregating gateway**, server catalog, and web UI in a
single Go binary.

GarmX presents itself to an AI client (Claude Code, OpenCode, …) as **one** MCP
server, then fans requests out to many registered upstream MCP servers: it
merges their tools/prompts/resources, prefixes names to avoid collisions
(`server___tool`), and routes each call to the owning upstream. All traffic is
audited locally and viewable in a built-in web UI.

> Status: early scaffold. The design is settled; implementation is in progress.

## Quick start

```sh
make build          # compile to bin/garmx
./bin/garmx -h      # usage
make check          # full validation gate (run after every change)
make run            # build + run the daemon
```

The daemon binds `127.0.0.1:9735` by default — it holds every upstream's
credentials and must not be exposed publicly.

## Development

`make check` (fmt → lint → vet → test → build) is the gate; run it after every
change. Dev tools (gofumpt, golangci-lint, templ) are invoked via `go run` with
pinned versions, so no global installs are required.

Standards and conventions: [`AGENTS.md`](AGENTS.md).

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — system design, aggregation
  model, package layout, schema.
- [`docs/implementation.md`](docs/implementation.md) — phased build plan.
- [`docs/discovery.md`](docs/discovery.md) — open research, decisions, and
  dropped scope.
