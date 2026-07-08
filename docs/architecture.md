# GarmX Architecture

## Overview

GarmX is a local-first **MCP registry and aggregating gateway** with a built-in server
catalog and web UI. It runs as a single Go binary. To any AI client it presents
itself as **one** MCP server; behind that facade it fans requests out to many
registered upstream MCP servers, merges their capabilities, and routes tool
calls to the correct upstream.

One of the key value adds - GarmX provides tracebility, audit and observability of all mcp tool usage.
Supports full observability OTEL exporting to any relevant analytics platform.

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

```text
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

## Process model: one shared daemon

GarmX runs as a **single long-lived daemon**, not a fresh instance per client.
One daemon owns all upstream MCP servers, their credentials, the SQLite
catalog + audit store, and the management UI on `:9735`. Every client
connection is a **session** against that one daemon.

This is deliberate and load-bearing:

- **One copy of each upstream.** N connected clients share the same upstream
  processes/connections, not N× duplicates.
- **A single collection point.** All client→MCP traffic converges in one
  process, which is what makes the audit/observability plane possible (see
  "Observability & export"). Per-client instances would fragment telemetry and
  each try to bind `:9735`.
- **Register once, visible everywhere.** A server registered through any door
  is immediately part of the aggregate for every session (subject to the
  session's profile).

The stdio face therefore does **not** boot its own daemon: `garmx serve
--stdio` is a **thin shim** that proxies the client's stdio JSON-RPC to the
running daemon over a local channel, starting the daemon on demand if none is
running. The daemon is the single holder of state; the shim is stateless.

## Client connection model

### stdio (v1 primary — Claude Code, OpenCode)

The client's config launches `garmx serve --stdio` as a subprocess and speaks
newline-delimited JSON-RPC over its stdin/stdout. GarmX is, from the client's
point of view, an ordinary local stdio MCP server; behind the shim the
conversation is handled by the shared daemon.

Example (`claude mcp add`-style / `.mcp.json`):

```json
{
  "mcpServers": {
    "garmx": { "command": "garmx", "args": ["serve", "--stdio"] }
  }
}
```

The management UI is served by the **daemon**, once, on `:9735` — not by each
stdio shim. A client that wants a specific scope passes `--profile <name>`
in its `args` (see "Profiles / access scoping").

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

```text
<serverName>___<toolName>
e.g.  postgres___query      github___create_issue
```

Rules:

- **On the way out** (`tools/list`, `prompts/list`): GarmX rewrites every
  upstream name to its prefixed form before returning to the client.
- **On the way in** (`tools/call`, `prompts/get`): GarmX splits on the **first**
  `___`, looks up the server, strips the prefix, and forwards the **original**
  name to the upstream.
- Server names are validated at registration to match `^[a-z0-9][a-z0-9-]*$`,
  length **1..32** (no underscores, lowercase). Forbidding underscores is what
  guarantees the first-`___` split is unambiguous — an upstream's *original*
  name may itself contain `___` and still round-trips.
- **Collision safety:** prefixing makes every exposed name globally unique, so
  two servers can both expose a `query` tool without conflict.
- **Length budget:** many clients cap tool names (commonly 64–128 chars), and a
  downstream client wraps the exposed name *again* (Claude Code presents it as
  `mcp__<garmx>__<exposed>`), so the effective budget is tighter than it looks.
  GarmX **warns** (non-fatal) at registration when the exposed name exceeds
  **60** chars and the UI surfaces the offending tools; it **never truncates**,
  since truncation would break the reversible split. Keep server names short.
- **Resources are exempt:** they are addressed by `uri`, which is already
  namespaced; GarmX tracks URI ownership instead of rewriting.

### Pagination (eager page-merge)

`*/list` methods are cursor-paginated. GarmX **drains every upstream's pages**
(follows each upstream's `nextCursor` to exhaustion) when it refreshes the
merged view, caches the **complete** merged list per profile, and serves it in
one response with **no client-facing `nextCursor`.** Local scale makes this
simple and correct; a single synthesized aggregate cannot cleanly re-expose N
independent upstream cursors through one opaque token. GarmX issues no cursors,
so a client-supplied `cursor` is rejected with `-32602` (invalid params).

### Protocol version negotiation

- **Client face:** GarmX supports `{2025-11-25, 2025-06-18}` and prefers
  `2025-11-25`. On `initialize` it echoes the client's requested version when
  supported, otherwise returns the preferred one. It **never fails `initialize`
  on version alone** — captured clients proceed on a differing server version.
- **Upstream face:** GarmX sends the preferred version and **accepts whatever
  the upstream reports** (the known revisions are wire-compatible for the
  methods GarmX uses), recording it in `servers.protocol_version`. An
  **unrecognized** version is **flagged as a mismatch** (degraded status, shown
  in the UI) — never a silent drop. A server is marked offline only when the
  handshake itself fails.

### Capability merge

The capabilities GarmX advertises to a client are the **union** of its
upstreams': a capability is present if **any** upstream has it, and boolean
sub-flags (`tools.listChanged`, `resources.subscribe`, …) are the **OR** across
the upstreams that expose that capability. This is what lets a forwarded
`list_changed` be meaningful — GarmX only advertises `listChanged` because at
least one upstream backs it.

### Response demultiplexing (concurrency-correct)

A single upstream transport has one read loop. Multiple client requests can be
in flight to the same upstream concurrently, so responses **must** be matched
by JSON-RPC `id`, never by "next message off the channel."

Each upstream owns a `pending` map:

```text
map[requestID]chan *mcp.Response
```

The read loop dispatches each inbound message:

- has an `id` matching a pending request → deliver to that request's channel;
- has an `id` but no pending entry (a **server→client request**) → hand to the
  session's callback router (deferred handling in v1: reply with a
  method-not-supported error);
- has no `id` (a **notification**) → hand to the notification router.

Matching by `id` keeps responses correctly delivered under concurrent
in-flight requests; delivering "the next message off the channel" would
misroute them.

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

## Registration & source of truth

Servers enter the catalog through several front doors, but there is exactly
**one authoritative store: SQLite.** Everything else is a way to write into it.

> **SQLite is the runtime source of truth.** The config file is a
> one-directional *seed/import*, never a live mirror. There is no continuous
> file↔DB sync (that path is an edit-race and secret-drift tar pit).

Registration paths:

| Path | Verb | Notes |
|------|------|-------|
| **REST + HTMX UI** | `POST/PUT/DELETE /api/servers` | Live mutation; the human-facing default. |
| **CLI** | `garmx server add/rm/ls` | Thin wrapper over the same registry package; used by the tutorials. |
| **Config seed** | `garmx serve --config garmx.jsonc` | On first run, import listed servers into SQLite if absent. Not re-read as truth afterward. |
| **Import (adopt)** | `garmx import <path>` | Parse an existing client config — Claude Code `.mcp.json` (`mcpServers.<name>`) or OpenCode `opencode.json` (`mcp.<name>`) — and register its servers. The primary onboarding flow: sweep the servers already scattered across a user's clients into GarmX, then repoint each client at just `garmx`. |
| **Export** | `garmx export <path>` | Serialize the DB back to a JSONC file for backup/portability (secrets masked or referenced, never inlined verbatim). |
| **Public MCP Registry** | *(later)* | Optional discovery source (`registry.modelcontextprotocol.io`) — browse-and-add. Distinct from GarmX's own local catalog. |
| **Agent self-registration** | *(deferred, gated)* | A `garmx___register_server` meta-tool would let an agent add servers — but that grants arbitrary command execution. Not in v1; behind an explicit opt-in flag if ever. |

Import is an **explicit verb**, not a watcher. `--config` seeds once; after that
the DB wins. This is what makes live UI/REST/CLI registration coherent — runtime
mutation is first-class, so the store that the daemon mutates must be
authoritative.

---

## Profiles / access scoping

By default GarmX exposes **every enabled server and every tool** to whoever
connects — no cost to users who don't want scoping. On top of that, a **profile**
is a named subset of the aggregate that a given client sees.

The framing is **curation first, security second.** The main value is that a
focused toolset (the 12 relevant tools, not all 140) improves agent tool
selection and cuts per-turn token cost; hiding destructive tools (a `read-only`
profile) is a secondary safety benefit.

```text
profile "coding" = {
  servers: [github, postgres, filesystem],
  tools:   allow ["github___*", "postgres___query"], deny ["*___delete_*"]
}
```

**Identity is transport-bound — this is the load-bearing constraint:**

- **stdio (v1 primary):** GarmX is launched as a subprocess by exactly *one*
  client, with *no* authentication. There is no runtime principal to enforce
  against, so access control is a **launch-time selection**:
  `garmx serve --stdio --profile coding`. The invocation *is* the identity.
- **Streamable HTTP shared daemon (later):** multiple sessions with real
  MCP OAuth2/bearer identity. Here a **token → profile** mapping yields genuine
  per-agent RBAC. This is the *only* place "which agent can access which MCP"
  becomes enforceable — so it waits for that face.

v1 ships **static profiles** only — no roles, inheritance, or policy DSL. Those
are premature while the enforceable-identity story doesn't exist yet.

**Architecture impact — the merged view becomes per-profile, not global:**

- `tools/list` / `prompts/list` caching is keyed by profile.
- The prefix length budget is evaluated per profile.
- `list_changed` fan-out targets each session according to its profile.
- Newly-appeared tools (via `list_changed`) need a per-profile default:
  allow (friendlier) vs deny (safer). Decided per profile.
- The session row records its profile, so the audit trail gains
  "which profile called what" for free.

---

## Observability & export

Beyond aggregation, GarmX's value is that **every client→MCP transaction flows
through one process** — so it can be captured, attributed per client, shown in a
minimal UI, and exported to external systems. This is the differentiator, and
the shared-daemon model exists largely to enable it.

### Guiding principle: emit, don't rebuild Grafana

GarmX owns the **raw audit trail** (SQLite) and a **deliberately minimal**
built-in UI. It does **not** reimplement historical trends, alerting, long
retention, or cross-service correlation — those are delegated to the Grafana
family via export. If the built-in UI starts wanting time-range pickers and
stacked charts, that is the signal it belongs in Grafana, fed by the exporter.

```text
client ──stdio shim──► GarmX daemon ──► upstreams
                          │
                          ├─ audit (redacted, size-capped) ─► SQLite ─► minimal UI (stat tiles, live log)
                          │                                       └─ WebSocket live stream
                          └─ OTLP exporter ─► metrics (default) ─────┐
                                              logs / traces (opt-in) ─┴─► OTel Collector ─► Prometheus / Loki / Tempo / Grafana
```

### Built-in UI (minimal, on `:9735`)

A few stat tiles (calls, error rate, p50/p95 latency per server), a top-tools
list, per-client / per-profile counts, and the live log stream. These derive
from `audit_logs` with indexed queries — **no separate metrics store** until
query cost demands one. (Chart/tile work pulls in the dataviz guidance so the
UI reads as one system.)

### Export: OpenTelemetry (OTLP), not point integrations

GarmX emits **OTLP** — one protocol carrying three signals — and lets the OTel
Collector / Grafana Alloy fan out to any backend (vendor-neutral, not
Grafana-locked). Signal mapping:

| Signal   | GarmX source                                                    | Typical backend |
|----------|-----------------------------------------------------------------|-----------------|
| Traces   | each `tools/call` as a span: client → garmx → upstream, with tool/server/latency/error | Tempo |
| Metrics  | counters (calls, errors) + latency histograms, bounded labels   | Prometheus |
| Logs     | the audit payloads                                              | Loki |

### Non-negotiable constraints (see discovery for open mechanics)

- **Redaction happens before the fork**, not before display. Both the SQLite
  write *and* the export path are downstream of redaction — export is a new
  secret-egress path.
- **Tiered export by privacy.** Metrics (counts/latency) are safe by default;
  **logs and traces carry real tool arguments and results** (query outputs,
  file contents) and are **opt-in per destination**, redacted.
- **Audit payloads are size-capped.** Tool results can be multi-MB; store
  metadata always, truncate/omit large bodies with a marker, and enforce
  retention (max age / rows). Never let the audit DB grow unbounded.
- **Bounded metric cardinality.** Label metrics by `server` / `tool` / `status`
  (and maybe client-app); keep high-cardinality ids (session id) in
  logs/traces, never in metric labels.
- **Client attribution.** The raw unit is the session; the UI groups by
  **profile + client app** (`clientInfo`), since stdio gives no stronger
  identity than that. See discovery for the open decision.

---

## Module / Package Layout

```text
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
│   │   ├── redact.go            # Redaction on the write path (before store + export)
│   │   ├── export.go            # OTLP exporter: metrics (default), logs/traces (opt-in)
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

Layout rationale:

- **`pkg/mcp`** holds typed MCP method params/results (initialize, tools,
  resources, prompts) plus a minimal-parse helper for the hot path.
- The three core concerns stay separate: **`aggregator`** (protocol logic),
  **`upstream`** (transports to real servers), and **`frontend`**
  (client-facing endpoints).

---

## Data Flow

### 1. `tools/list` (aggregation path — synthesized, not forwarded)

```text
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

```text
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

```text
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

```text
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
    raw_payload TEXT,                         -- redacted before write; NULL/truncated if over cap
    payload_bytes INTEGER,                    -- original size before any truncation
    truncated   INTEGER NOT NULL DEFAULT 0,   -- 1 if raw_payload was capped (see Observability)
    latency_ms  INTEGER,
    error_code  INTEGER,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_audit_created ON audit_logs(created_at);
CREATE INDEX idx_audit_server  ON audit_logs(server_id);
CREATE INDEX idx_audit_session ON audit_logs(session_id);

-- Profiles: named subsets of the aggregate exposed to a client
CREATE TABLE profiles (
    name         TEXT PRIMARY KEY,            -- selected via --profile / token map
    description  TEXT,
    servers      TEXT NOT NULL DEFAULT '[]',  -- JSON array of server names ([] = all enabled)
    tool_allow   TEXT NOT NULL DEFAULT '[]',  -- JSON array of exposed-name patterns ([] = all)
    tool_deny    TEXT NOT NULL DEFAULT '[]',  -- JSON array of exposed-name patterns
    default_new  TEXT NOT NULL DEFAULT 'allow',-- 'allow'|'deny' for tools appearing via list_changed
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Client sessions
CREATE TABLE sessions (
    id               TEXT PRIMARY KEY,
    client_info      TEXT,                    -- name/version from initialize
    protocol_version TEXT,
    transport        TEXT,                    -- 'stdio' | 'streamable-http'
    profile          TEXT REFERENCES profiles(name), -- scoping applied to this session
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

```text
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
| **SQLite is the registration source of truth** | Config file is a one-directional seed/import, not a live mirror; continuous file↔DB sync is an edit-race/secret-drift tar pit. Live UI/REST/CLI mutation is first-class, so the mutated store must win. |
| **`garmx import` adopts existing client configs** | The onboarding lever for "central point of MCP consumption": sweep servers already scattered across Claude Code / OpenCode configs into GarmX, then repoint clients at GarmX alone. |
| **Static profiles, curation-first** | Over stdio there's one unauthenticated client per process — no runtime principal to enforce against, so scoping is a launch-time `--profile` selection. Real per-agent RBAC waits for the HTTP daemon's token identity. Default exposes everything. |
| **One shared daemon; stdio is a thin shim** | A single process holds all upstreams, credentials, and the audit store — the one vantage point where all client→MCP traffic converges. Per-client instances would duplicate upstreams, fragment telemetry, and collide on `:9735`. |
| **Observability plane: emit, don't rebuild Grafana** | GarmX owns the raw audit trail + a minimal built-in UI, and exports via OTLP (metrics/logs/traces) to the Grafana family. It does not reimplement trends/alerting/retention. This audit+export plane is the differentiator beyond aggregation. |
| **Redact before the export fork; tier by privacy** | Redaction precedes both the SQLite write and export. Metrics export by default; logs/traces carry real tool args/results and are opt-in per destination. Audit payloads are size-capped with retention. |
