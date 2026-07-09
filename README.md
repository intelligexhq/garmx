# GarmX

**MCP registry and gateway** - self managed; includes, mcp server catalog, auditing, permission management, full OTEL observability export and a lean web UI. All in a single Go binary.

GarmX presents itself to an AI agents and clients (like  OpenCode, Claude Code, Langraph agents, etc.) as **one** MCP server;

- It presents tool catalog to agents and fans all MCP requests out to many registered upstream MCP servers;
- All traffic is audited and stored locally;
- Viewable in a built-in web UI.
- GarmX supports OTEL observability exports.

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

`make check` (fmt → lint → vet → test → build) is the gate; validate project with it after every
change. Dev tools (gofumpt, golangci-lint, templ) are invoked via `go run` with
pinned versions, so no global installs are required.

Standards and conventions are outlined in: [`AGENTS.md`](AGENTS.md).

## Documentation

Concepts, design discussions and implementation plan:

- [`docs/architecture.md`](docs/architecture.md) — system design, aggregation
  model, package layout, schemas.
- [`docs/implementation.md`](docs/implementation.md) — phased build and implementation plan.
- [`docs/discovery.md`](docs/discovery.md) — open research, decisions, and scope management.
