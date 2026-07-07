# GarmX Architecture

## Overview

GarmX is a local-first **MCP aggregating gateway** with a built-in server
catalog and web UI. It runs as a single Go binary. To an AI client it presents
itself as **one** MCP server; behind that facade it fans requests out to many
registered upstream MCP servers, merges their capabilities, and routes tool
calls to the correct upstream.

The mental model that matters:

> GarmX is a **stateful MCP server that delegates to other MCP servers.** It is
> not a byte pipe. For `initialize` and every `*/list` method it *synthesizes*
> an aggregated response; only `tools/call`, `resources/read`, and
> `prompts/get` are pass-through (with name rewriting).

First clients are **Claude Code** and **OpenCode**, both connecting over stdio.
Those two are the target of the first tutorials.

---

## Two faces of GarmX

GarmX has a **client-facing** side (what an AI client connects to) and an
**upstream-facing** side (how GarmX reaches the real MCP servers). Both sides
speak MCP, in both transports.

```
        CLIENT-FACING                         UPSTREAM-FACING
  ┌───────────────────────┐            ┌────────────────────────────┐
  │ Claude Code / OpenCode │            │  registered MCP servers    │
  │  → stdio (v1 primary)  │            │  → stdio subprocess        │
  │  → streamable-http     │            │  → streamable-http (remote)│
  └───────────┬───────────┘            └─────────────▲──────────────┘
              │                                       │
              ▼                                       │
  ┌───────────────────────────── GarmX daemon ───────┴──────────────┐
  │  frontend (client-facing MCP endpoints)                         │
  │        │                                                        │
  │  ┌─────▼──────────────── AGGREGATOR (core) ──────────────────┐  │
  │  │ • initialize   → negotiate version, merge capabilities    │  │
  │  │ • tools/list   → fan-out, merge, PREFIX names             │  │
  │  │ • tools/call   → strip prefix, route by tool name         │  │
  │  │ • resources/*  → route by URI ownership                   │  │
  │  │ • prompts/*    → prefix + route by name                   │  │
  │  │ • per-upstream response demux: map[id]chan Response       │  │
  │  │ • session registry (client conn ⇒ state)                  │  │
  │  └─────┬────────────────────────────────┬───────────────────┘  │
  │        │                                │                       │
  │   upstream/stdio                  upstream/streamhttp           │
  │                                                                 │
  │  SQLite: catalog · tool cache · audit    WebSocket: logs → UI   │
  │  Management HTTP: REST + HTMX UI on :9735                       │
  └─────────────────────────────────────────────────────────────────┘
```

The **management HTTP server** (REST + HTMX UI + WebSocket log stream) is a
separate concern from the client-facing MCP endpoint. Do not conflate the
two: the UI is for a human on `:9735`; the MCP endpoint is for an AI client.

---

## Client connection model

### stdio (v1 primary — Claude Code, OpenCode)

The client's config launches `garmx` as a subprocess and speaks
newline-delimited JSON-RPC over the process's stdin/stdout. GarmX is, from the
client's point of view, an ordinary local stdio MCP server.

Example (`claude mcp add`-style / `.mcp.json`):

```json
{
  "mcpServers": {
    "garmx": { "command": "garmx", "args": ["serve", "--stdio"] }
  }
}
```

When launched in `--stdio` mode GarmX still starts its management HTTP server
(so the UI works), but the MCP conversation happens over stdio, not HTTP.

### streamable-http (v1 secondary)

GarmX also exposes a **Streamable HTTP** MCP endpoint (single endpoint,
`POST` for client→server messages, optional `GET` + SSE for the server→client
stream). This is for clients configured to reach GarmX at a URL, and for
running GarmX as a shared local daemon.

> The legacy **HTTP+SSE** transport (spec 2024-11-05) is **deprecated** and is
> **not** implemented. Streamable HTTP (introduced 2025-03-26, current in the
> 2025-11-25 spec) is the only HTTP transport GarmX speaks.

---

## The aggregator (the core of the product)

### Method handling

| MCP method            | Handling                                                                 |
|-----------------------|--------------------------------------------------------------------------|
| `initialize`          | Negotiate protocol version with client; return **merged** capabilities. Fan out `initialize` to upstreams lazily/at startup. |
| `tools/list`          | Fan out, **merge**, rewrite each tool `name` → prefixed. Cache result.   |
| `tools/call`          | Split prefixed name → `(server, originalName)`; forward with original name to that upstream. |
| `resources/list`      | Fan out, merge; keep a `uri → upstream` ownership map.                    |
| `resources/read`      | Route by `uri` to owning upstream (URIs are already namespaced by scheme/host, so **no prefixing**). |
| `resources/templates/list` | Fan out, merge.                                                     |
| `prompts/list`        | Fan out, merge, rewrite prompt `name` → prefixed.                         |
| `prompts/get`         | Split prefixed name → route by name.                                      |
| `ping`                | Answer locally; also used by health checks against upstreams.            |
| `notifications/*` (from upstream) | e.g. `notifications/tools/list_changed`: refresh that upstream, rebuild merged maps, emit the same notification to connected clients. |
| `completion/complete` | Route by the ref (prompt/resource) to its owning upstream.               |
| server→client requests (`sampling/createMessage`, `elicitation/create`, `roots/list`) | **Deferred in v1** — see "Session model". |

### Name prefixing (AWS AgentCore pattern)

Tool and prompt names are prefixed with the **registered server name** using a
**triple-underscore** delimiter, matching AgentCore Gateway:

```
<serverName>___<toolName>
e.g.  postgres___query      github___create_issue
```

Rules:

- **On the way out** (`tools/list`, `prompts/list`): GarmX rewrites every
  upstream name to its prefixed form before returning to the client.
- **On the way in** (`tools/call`, `prompts/get`): GarmX splits on the **first**
  `___`, looks up the server, strips the prefix, and forwards the **original**
  name to the upstream.
- Server names are validated at registration to match `^[a-z0-9][a-z0-9-]*$`
  (no `___`, lowercase), guaranteeing the split is unambiguous.
- **Collision safety:** prefixing makes every exposed name globally unique, so
  two servers can both expose a `query` tool without conflict.
- **Length budget:** many clients cap tool names (commonly 64–128 chars). The
  prefix consumes part of that budget. GarmX warns at registration if
  `len(serverName) + 3 + len(toolName)` exceeds 60, and the UI surfaces any
  tools that would be truncated.
- **Resources are exempt:** they are addressed by `uri`, which is already
  namespaced; GarmX tracks URI ownership instead of rewriting.

### Response demultiplexing (concurrency-correct)

A single upstream transport has one read loop. Multiple client requests can be
in flight to the same upstream concurrently, so responses **must** be matched
by JSON-RPC `id`, never by "next message off the channel."

Each upstream owns a `pending` map:

```
map[requestID]chan *mcp.Response
```

The read loop dispatches each inbound message:

- has an `id` matching a pending request → deliver to that request's channel;
- has an `id` but no pending entry (a **server→client request**) → hand to the
  session's callback router (deferred handling in v1: reply with a
  method-not-supported error);
- has no `id` (a **notification**) → hand to the notification router.

This eliminates the response-misdelivery race in the original design.

---

## Session model

A **session** is one client connection (one stdio conversation, or one
Streamable HTTP session id). The session holds:

- negotiated protocol version and client capabilities;
- the merged capability view served to this client;
- a back-reference used to push notifications (and, later, server→client
  requests) to this specific client.

v1 fully handles the **client→gateway→upstream** direction. Server→client
requests (sampling, elicitation, roots) are **deferred**: GarmX replies to them
with a JSON-RPC error (`-32601`, method not found) and logs them. The session
registry already threads the "which client originated the upstream work"
back-reference, so adding real callback routing later is an extension, **not a
rewrite**.

---

## Module / Package Layout

```
garmx/
├── cmd/
│   └── garmx/
│       └── main.go              # Entrypoint, flags, serve modes, wiring
│
├── internal/
│   ├── config/
│   │   └── config.go            # Config load (JSONC), defaults, validation
│   │
│   ├── aggregator/
│   │   ├── aggregator.go        # Core: dispatch by method, merge, route
│   │   ├── naming.go            # prefix / split (server___tool), validation
│   │   ├── session.go           # Client session state + registry
│   │   ├── capabilities.go      # initialize negotiation + capability merge
│   │   └── notify.go            # Upstream→client notification propagation
│   │
│   ├── upstream/
│   │   ├── transport.go         # Transport interface (stdio | streamhttp)
│   │   ├── stdio.go             # Subprocess: framing, stderr drain, demux
│   │   ├── streamhttp.go        # Streamable HTTP client to remote upstream
│   │   ├── manager.go           # Lifecycle, restart/backoff, health
│   │   └── pending.go           # id → response-channel demultiplexer
│   │
│   ├── frontend/
│   │   ├── stdio.go             # GarmX-as-stdio-server (client-facing)
│   │   └── streamhttp.go        # GarmX-as-streamable-http-server
│   │
│   ├── registry/
│   │   ├── registry.go          # Catalog CRUD (enable/disable, config)
│   │   ├── store.go             # SQLite persistence
│   │   └── schema.go            # Tool/prompt schema cache from *_/list
│   │
│   ├── audit/
│   │   ├── audit.go             # Async batched audit writer
│   │   ├── store.go             # SQLite persistence for audit logs
│   │   └── stream.go            # WebSocket emitter for the live UI
│   │
│   ├── health/
│   │   └── health.go            # Periodic ping / liveness, status updates
│   │
│   ├── api/                     # MANAGEMENT plane (human UI), not MCP
│   │   ├── router.go            # net/http mux route definitions
│   │   ├── registry_handler.go  # REST + HTMX for server CRUD
│   │   ├── health_handler.go    # Status endpoint
│   │   └── audit_handler.go     # Log query + WebSocket upgrade
│   │
│   └── ui/
│       ├── server.go            # //go:embed static + templ wiring
│       ├── templates/           # *.templ
│       └── static/              # css/js/favicon
│
├── pkg/
│   └── mcp/
│       ├── message.go           # JSON-RPC 2.0 envelope types
│       ├── methods.go           # Typed MCP method params/results
│       └── parse.go             # Envelope decode + id/method extraction
│
├── go.mod / go.sum
├── Makefile
└── docs/  (architecture.md, implementation.md, discovery.md)
```

Notes on the rename from the earlier draft:

- `pkg/jsonrpc` → **`pkg/mcp`**: we need typed MCP method params/results
  (initialize, tools, resources, prompts), not just a minimal id/method
  scraper. The minimal-parse helper still lives here for the hot path.
- `internal/gateway` → split into **`aggregator`** (protocol logic),
  **`upstream`** (transports to real servers), and **`frontend`**
  (client-facing endpoints). The old design folded all three into one "hub."

---

## Data Flow

### 1. `tools/list` (aggregation path — synthesized, not forwarded)

```
Client                        GarmX aggregator                 Upstreams
  │  tools/list                    │                               │
  │ ─────────────────────────────► │  fan out tools/list ─────────►│ (pg)
  │                                │  ─────────────────────────────►│ (github)
  │                                │  ◄──────── merge results ──────│
  │                                │  rewrite names:                │
  │                                │    query → pg___query          │
  │                                │    create_issue → github___... │
  │                                │  cache merged list             │
  │  merged tool list              │                                │
  │ ◄───────────────────────────── │                                │
```

### 2. `tools/call` (pass-through path — routed by tool name)

```
Client                        GarmX aggregator                 Upstream (pg)
  │  tools/call                    │                               │
  │  name="pg___query"             │                               │
  │ ─────────────────────────────► │  split "pg___query"           │
  │                                │   → server=pg, name=query      │
  │                                │  async: audit + WS emit        │
  │                                │  rewrite name → "query"        │
  │                                │  forward, register id in       │
  │                                │  pending map ────────────────► │
  │                                │  ◄──── response (matched id) ──│
  │  result                        │                               │
  │ ◄───────────────────────────── │                               │
```

### 3. Registry CRUD (management path)

```
Browser                          GarmX (api)                    SQLite
  │ POST /api/servers               │                             │
  │ {name,command,args,env,          │  validate (name regex,     │
  │  transport}                      │  prefix length warn)       │
  │ ──────────►                      │  insert ──────────────────►│ servers
  │                                  │  start upstream, initialize │
  │                                  │  cache tools/prompts ──────►│ tool_schemas
  │ 200 + rendered row (HTMX)        │  rebuild merged view        │
  │ ◄──────────                      │                             │
```

### 4. Health / list-changed

```
health.go (ticker 30s)                upstream notification
  ├─ stdio:  process alive?           notifications/tools/list_changed
  │          + optional ping          │
  ├─ http:   ping RPC w/ timeout      ├─ refresh that upstream's tools
  └─ update status → WS → UI          ├─ rebuild merged maps
                                      └─ emit list_changed → clients
```

---

## SQLite Schema

```sql
-- Registry: registered upstream MCP servers
CREATE TABLE servers (
    id          TEXT PRIMARY KEY,             -- UUID
    name        TEXT NOT NULL UNIQUE,         -- prefix name; ^[a-z0-9][a-z0-9-]*$
    transport   TEXT NOT NULL DEFAULT 'stdio',-- 'stdio' | 'streamable-http'
    command     TEXT,                         -- executable (stdio)
    args        TEXT NOT NULL DEFAULT '[]',   -- JSON array of strings (stdio)
    env         TEXT NOT NULL DEFAULT '{}',   -- JSON object (stdio); values secret
    url         TEXT,                         -- endpoint (streamable-http)
    headers     TEXT NOT NULL DEFAULT '{}',   -- JSON object (streamable-http auth)
    enabled     INTEGER NOT NULL DEFAULT 1,   -- include in aggregation?
    status      TEXT NOT NULL DEFAULT 'unknown', -- online|degraded|offline|unknown
    protocol_version TEXT,                     -- negotiated with this upstream
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Cached tool/prompt schemas (kind distinguishes them)
CREATE TABLE capability_cache (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id     TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,              -- 'tool' | 'prompt' | 'resource'
    original_name TEXT NOT NULL,              -- name as the upstream reports it
    exposed_name  TEXT NOT NULL,              -- prefixed name we show the client
    description   TEXT,
    payload       TEXT NOT NULL,              -- JSON (inputSchema / arguments / uri)
    fetched_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(server_id, kind, original_name)
);
CREATE INDEX idx_cap_exposed ON capability_cache(exposed_name);

-- Audit log: every MCP message through the aggregator
CREATE TABLE audit_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT,
    server_id   TEXT REFERENCES servers(id),  -- resolved upstream, if any
    direction   TEXT NOT NULL,                -- 'client_in'|'upstream_out'|'upstream_in'|'client_out'
    method      TEXT,
    rpc_id      TEXT,
    raw_payload TEXT NOT NULL,                -- redacted before write (see security)
    latency_ms  INTEGER,
    error_code  INTEGER,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_audit_created ON audit_logs(created_at);
CREATE INDEX idx_audit_server  ON audit_logs(server_id);
CREATE INDEX idx_audit_session ON audit_logs(session_id);

-- Client sessions
CREATE TABLE sessions (
    id               TEXT PRIMARY KEY,
    client_info      TEXT,                    -- name/version from initialize
    protocol_version TEXT,
    transport        TEXT,                    -- 'stdio' | 'streamable-http'
    started_at       TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at         TEXT
);
```

Driver: **`modernc.org/sqlite`** (pure Go, no CGo). Concurrency: one dedicated
writer connection plus a small read pool. WAL mode is enabled for concurrent
reads; do **not** claim concurrent-read benefits while also pinning
`MaxOpenConns(1)` — use a separate read `*sql.DB` if reads must not block on the
writer.

---

## API Surface

### Client-facing MCP (the product)

| Transport        | How the client reaches it                                  |
|------------------|------------------------------------------------------------|
| stdio            | client launches `garmx serve --stdio`                      |
| streamable-http  | `POST`/`GET` on the MCP endpoint, e.g. `:9735/mcp`         |

These carry MCP JSON-RPC only. There is **no** bespoke `/json-rpc` endpoint.

### Management REST (human UI plane, `:9735`)

| Method | Path                       | Description                        |
|--------|----------------------------|------------------------------------|
| GET    | /api/servers               | List registered servers            |
| GET    | /api/servers/{id}          | Server details + cached tools      |
| POST   | /api/servers               | Register a server                  |
| PUT    | /api/servers/{id}          | Update config                      |
| DELETE | /api/servers/{id}          | Unregister                         |
| POST   | /api/servers/{id}/enable   | Enable/disable in aggregation      |
| POST   | /api/servers/{id}/restart  | Restart upstream                   |
| GET    | /api/servers/{id}/tools    | Cached tool/prompt schemas         |
| GET    | /health                    | Daemon + upstream statuses         |
| GET    | /api/logs                  | Query audit logs (paginated)       |
| GET    | /api/logs/stream           | WebSocket — live logs              |

### UI (HTMX)

| Path            | Description                        |
|-----------------|------------------------------------|
| /               | Dashboard                          |
| /servers        | Server list + add/edit             |
| /servers/{id}   | Server detail (config, tools, log) |
| /logs           | Live audit log viewer              |

---

## UI Component Hierarchy (HTMX + Templ)

```
layout.templ (shell — nav, main)
├── dashboard.templ      → ServerStatusCards, TrafficSummary, RecentLog
├── servers.templ        → ServerTable → ServerRow, AddServerForm
├── server_detail.templ  → ServerConfig, ToolList (exposed vs original names),
│                          HealthTimeline, LiveServerLog
└── logs.templ           → LogFilterBar, LogTable (infinite scroll)
```

HTMX drives DOM fragment swaps (partials detected via the `HX-Request`
header). WebSocket is used **only** for the live log stream.

---

## Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **GarmX is a stateful aggregator, not a byte pipe** | `initialize`/`*/list` must be merged & synthesized; only calls/reads are pass-through. This is the core of the product. |
| **Route `tools/call` by tool name, never by method prefix** | MCP methods are fixed protocol verbs (`tools/call`), not `server/method`. The `/` is a category, not a namespace. |
| **AgentCore-style `server___tool` prefixing** | Guarantees globally-unique names across upstreams; reversible via first-`___` split; matches an established gateway convention. |
| **Per-upstream `id → chan` response demux** | Correct under concurrent in-flight requests; the "read next off OutChan" model misdelivers responses. |
| **stdio primary, streamable-http secondary (client-facing)** | Claude Code & OpenCode connect over stdio today; Streamable HTTP covers remote/daemon use. Legacy HTTP+SSE is dropped. |
| **Defer server→client callbacks, keep session back-ref** | Sampling/elicitation/roots add real complexity; first flows don't need them. Session model makes later support an extension, not a rewrite. |
| **stdlib `net/http` mux (Go 1.22+)** | Method + path-param routing is built in; no Chi dependency needed. |
| **`modernc.org/sqlite`, WAL, single writer** | Pure Go (easy cross-compile); local single-user workload doesn't need more. |
| **HTMX + Templ, embedded assets, single binary** | Zero JS-framework overhead; type-safe templates; one `go build`. |
| **Redact secrets before audit write** | `env`/`headers`/tool args can carry credentials; redaction happens on the write path, not at display time only. |
| **Bind `127.0.0.1` by default** | The daemon holds every upstream's credentials; never expose it on `0.0.0.0` without explicit opt-in. |
