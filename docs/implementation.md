# Implementation Plan

Phased build plan for GarmX. Each phase produces a working, testable
increment. The sequencing is deliberate: **prove the MCP aggregation core with
a real client (Claude Code / OpenCode) as early as possible** — that is the
make-or-break risk, so it comes before persistence and UI.

## Architecture recap

The plan below assumes the model defined in `architecture.md`:

- **One shared daemon.** A single long-lived process owns all upstream MCP
  servers, their credentials, the SQLite catalog + audit store, and the
  management UI on `:9735`. Every client connection is a session against it.
- **stdio is a thin shim.** `garmx serve --stdio` proxies a client's stdio
  JSON-RPC to the daemon over a local channel, auto-starting the daemon if none
  runs. The shim holds no state.
- **SQLite is the source of truth** for the catalog; the config file is a
  one-directional seed/import, never a live mirror.
- **Profiles** scope what each client sees (curation-first; default exposes
  everything). Over stdio a profile is chosen with `--profile`; real per-agent
  identity waits for the HTTP face.
- **Observability is the differentiator.** Every transaction is audited
  (redacted, size-capped) and exportable via OTLP; the built-in UI stays
  minimal (emit, don't rebuild Grafana).

---

## Phase 0: Project scaffolding — done

Go module, package directories with `doc.go`, Makefile with the `check` gate,
`.golangci.yml`, CI running `make check`, and a thin `cmd/garmx/main.go`.
`make check` is green.

---

## Phase 1: MCP core — daemon, one upstream, stdio client

**Goal:** Claude Code (or OpenCode) launches `garmx serve --stdio`; the shim
relays to the daemon; a full `initialize` → `tools/list` → `tools/call`
round-trip works against **one** registered stdio upstream. No aggregation
across many servers yet, no persistence, no UI.

This phase de-risks everything: protocol correctness, stdio framing, the
response demultiplexer, and the shim↔daemon channel.

### Order of implementation

1. **`pkg/mcp/message.go`** — JSON-RPC 2.0 envelope: `Request`, `Response`,
   `Notification`, `Error`; `params`/`result` as `json.RawMessage`;
   `IsNotification()`.
2. **`pkg/mcp/methods.go`** — typed params/results for `initialize`,
   `tools/list`, `tools/call`, `resources/list`, `resources/read`,
   `prompts/list`, `prompts/get`, `ping`, and the `notifications/*` we emit.
3. **`pkg/mcp/parse.go`** — fast envelope decode; extract `method`/`id` for the
   hot path without full unmarshal.
4. **`internal/upstream/pending.go`** — `id → chan *mcp.Response`
   demultiplexer with cancellation and timeout.
5. **`internal/upstream/stdio.go`** — subprocess transport:
   - `exec.Command`, `Setpgid` for group cleanup, env injection.
   - **Framing:** newline-delimited JSON read with
     `bufio.Reader.ReadBytes('\n')` (or a scanner with an enlarged buffer) —
     **never** the default `bufio.Scanner` 64KB line cap.
   - **Drain stderr** to the daemon log; a full stderr pipe otherwise blocks
     the child.
   - Read loop dispatches via `pending` (match by id) / notification router.
   - `Start()`, `Stop()` (SIGTERM → wait → SIGKILL), `Health()`.
6. **`internal/upstream/transport.go`** — `Transport` interface:
   `Start/Stop/Send/SetHandlers/Health`. stdio implements it; Streamable HTTP
   arrives later.
7. **`internal/aggregator/session.go`** — per-connection session state
   (protocol version, client caps, profile, back-ref for notifications).
8. **`internal/aggregator/capabilities.go`** — `initialize`: negotiate version
   with the client, initialize the single upstream, return its capabilities.
9. **`internal/aggregator/aggregator.go`** — dispatch by method. With one
   upstream, `*/list` just forwards; `tools/call` forwards. Keep the dispatch
   seam ready for merging.
10. **`internal/daemon`** — the long-lived process: owns the upstream(s), the
    aggregator, the session registry, and the management HTTP server; exposes
    the local channel the stdio shim connects to.
11. **`internal/frontend/stdio.go`** — the client-facing stdio path: as the
    shim, relay client stdin/stdout to the daemon channel; as the daemon side,
    hand frames to the aggregator and write results back.
12. **`cmd/garmx/main.go`** — `serve` starts the daemon; `serve --stdio` runs
    the shim (auto-starting the daemon); graceful shutdown on SIGINT/SIGTERM
    (stop upstreams, flush).

### Tests

- Unit: `pkg/mcp` encode/decode (requests, responses, notifications, errors).
- Unit: `pending` demux — concurrent ids resolve to the correct channel.
- Integration: a **test upstream** (tiny Go program: reads stdin, answers
  `initialize`/`tools/list`/`tools/call`) driven through the aggregator;
  assert correct correlation under concurrency.
- Integration: the stdio shim relays a full round-trip to the daemon.
- **Manual acceptance:** register `garmx` in Claude Code, confirm the
  upstream's tools appear and one call succeeds. This is the phase's real gate.

### Deliverables

- Real client → shim → daemon → one stdio upstream works end to end.
- Correct stdio framing (large payloads), stderr drained, ids demultiplexed.
- Graceful shutdown cleans up the child process group.

---

## Phase 2: Aggregation — many upstreams, prefixing, profiles

**Goal:** Register N upstreams; the daemon presents the merged capability set
with `server___tool` prefixing, routes calls back correctly, and scopes the
merged view per session profile.

### Order of implementation

1. **`internal/aggregator/naming.go`**
   - `Prefix(server, name) string` → `server___name`.
   - `Split(exposed) (server, name string, ok bool)` — split on the **first**
     `___`.
   - Validate server name `^[a-z0-9][a-z0-9-]*$`; length-budget warning
     (`len(server)+3+len(tool) > 60`).
2. **Merge in `aggregator.go`**
   - `tools/list` / `prompts/list`: fan out to all enabled upstreams, rewrite
     names to prefixed, concatenate; cache the merged view + a
     `exposedName → (server, originalName)` route map, **keyed by profile**.
   - `resources/list`: merge; build a `uri → server` ownership map (no
     prefixing — URIs are already namespaced).
   - `tools/call` / `prompts/get`: `Split` → look up route → forward original
     name to the owning upstream.
   - `resources/read`: route by `uri` ownership.
3. **`internal/aggregator/profile.go`** — apply a profile (server subset +
   tool allow/deny patterns) when building a session's merged view. Default
   profile = all enabled servers, all tools. Decide per-profile handling of
   tools that appear later via `list_changed` (allow vs deny).
4. **`internal/upstream/manager.go`** — manage the set of upstreams:
   lifecycle, restart with exponential backoff + max retries, re-init on
   restart.
5. **`internal/aggregator/notify.go`** — on upstream
   `notifications/*/list_changed`: refresh that upstream, rebuild the affected
   per-profile merged maps, emit the corresponding `list_changed` to each
   connected session according to its profile.
6. **Capability merge** (`capabilities.go`): advertise the **union** of
   upstream capabilities to the client; record each upstream's negotiated
   protocol version.

### Tests

- Unit: `naming` prefix/split round-trip, including tool names that contain
  single underscores; reject names containing `___`.
- Unit: profile filtering — server subset and allow/deny patterns select the
  right exposed set; deny wins over allow.
- Unit: merge with two upstreams exposing a same-named tool → both visible,
  both routable.
- Integration: two test upstreams; assert `tools/list` returns the prefixed
  union per profile and each `tools/call` reaches the correct upstream.
- Integration: simulate `list_changed` from one upstream → affected merged
  views update and clients are notified per profile.

### Deliverables

- True multi-server aggregation with collision-safe naming.
- Per-profile merged views and `list_changed` propagation.
- **Acceptance:** Claude Code sees tools from two real MCP servers at once,
  scoped by the profile it launched with.

---

## Phase 3: Persistence — registry, catalog, audit

**Goal:** SQLite becomes the source of truth for the catalog; schemas are
cached; every transaction is audited (redacted, size-capped) with retention.

### Order of implementation

1. **`internal/registry/store.go`** — `Open` (WAL, foreign keys), `Migrate`;
   `modernc.org/sqlite`; single writer conn + read pool.
2. **`internal/registry/registry.go`** — CRUD
   (`List/Get/Create/Update/Delete`, `Enable`); SQLite is authoritative, so on
   create/update the registry starts/restarts the upstream via the manager.
3. **`internal/registry/import.go`** — `garmx import <path>`: parse existing
   client configs (Claude Code `.mcp.json` `mcpServers.<name>`, OpenCode
   `opencode.json` `mcp.<name>`) into `servers` rows; handle name collisions.
   `garmx export <path>` serializes the catalog back out (secrets never
   inlined). `--config` seeds the DB once on first run.
4. **`internal/registry/schema.go`** — cache tools/prompts/resources into
   `capability_cache` on registration and on a periodic refresh (5 min).
5. **`internal/audit/redact.go`** — redaction on the **write** path: `env` /
   `headers` values and configurable body patterns (`password`, `token`,
   `apiKey`, `authorization`). Runs before the store **and** before any export.
6. **`internal/audit/audit.go` + `store.go`** — async batched writer (flush
   every ~1s or ~100 entries); **size-cap** payloads (store metadata always,
   truncate/omit large bodies with a marker, record `payload_bytes` +
   `truncated`); retention sweep (max age / rows); paginated query.
7. **`internal/health/health.go`** — 30s ticker: stdio liveness via
   `Signal(0)` + optional `ping`; http via `ping` RPC; update status; emit
   changes toward the UI stream.

### Tests

- Registry CRUD against `:memory:` SQLite.
- Import: Claude Code and OpenCode fixtures → expected `servers` rows;
  collision handling.
- Schema cache with a mock `tools/list`.
- Redaction: secrets in `env`/`headers`/body patterns never reach the stored
  payload.
- Audit: batch insert, size-cap truncation, retention sweep, filtered query.
- Integration: register via API → appears in list → health reflects it.

### Deliverables

- Persistent catalog (SQLite authoritative), import/export, cached schemas.
- Redacted, size-capped, retained audit logs.
- Health status per upstream.

---

## Phase 4: Streamable HTTP (both faces)

**Goal:** GarmX speaks Streamable HTTP as a **client-facing** endpoint and as
an **upstream** client. (Legacy HTTP+SSE is not implemented.) This face also
introduces real per-session identity, the basis for token→profile mapping.

### Order of implementation

1. **`internal/upstream/streamhttp.go`** — Streamable HTTP **client** to remote
   upstreams: `POST` client→server messages; open the `GET` SSE stream for
   server→client messages; `Mcp-Session-Id` handling; reconnect with backoff;
   implements `Transport`.
2. **`internal/frontend/streamhttp.go`** — GarmX-as-Streamable-HTTP **server**:
   single MCP endpoint (e.g. `/mcp`), `POST` for requests, optional `GET`+SSE
   stream, per-session state keyed by the MCP session id.
3. **Identity:** map an authenticated session (bearer token) to a profile, so
   scoping becomes enforceable per connecting agent — not just a launch flag.
4. **Config**: `transport: "streamable-http"`, `url`, `headers` for upstreams;
   `serve --http` / `serve --stdio` (or both) for the daemon.
5. UI: transport-specific fields in add/edit forms.

### Tests

- Mock Streamable HTTP upstream: connect, initialize, route calls.
- Client-facing: drive the `/mcp` endpoint through a Streamable-HTTP client and
  assert an end-to-end call.
- Token→profile: two sessions with different tokens see different scoped views.

### Deliverables

- Remote MCP servers usable as upstreams.
- Clients can connect to GarmX over Streamable HTTP as well as stdio.
- Per-agent scoping enforceable via token identity.

---

## Phase 5: Embedded UI (HTMX + Templ)

**Goal:** Web interface for managing upstreams, browsing capabilities, and a
minimal live view of traffic.

### Prerequisites

`go install github.com/a-h/templ/cmd/templ@latest`; `make templ` before build.

### Order of implementation

1. **`internal/ui/server.go`** — `//go:embed` static assets; templ wiring.
2. **Templates:** `layout`, `dashboard`, `servers`, `server_detail`
   (show **exposed vs original** tool names), `profiles`, `logs`,
   `components`.
3. **HTMX handlers** in `internal/api/`: detect `HX-Request`; full page vs
   fragment; render via `templ.Render(ctx, w)`.
4. **Minimal observability view:** stat tiles (calls, error rate, p50/p95
   latency per server), a top-tools list, and per-client / per-profile counts,
   all derived from `audit_logs` with indexed queries — no separate metrics
   store. Anything deeper belongs in a Grafana dashboard fed by the exporter.
5. **WebSocket log stream:** `internal/audit/stream.go`
   (`Subscribe/Unsubscribe/Broadcast`); upgrade `GET /api/logs/stream`;
   `static/js/logs.js` appends rows.
6. **CSS:** minimal, dark default.
7. Wire: add-server form POSTs and HTMX-swaps the table; delete via
   `hx-delete`; dashboard polls health; log page opens the WebSocket.

### Tests

- Templ component render tests.
- HTMX handler tests (HX-Request detection, content type).
- WebSocket stream test (connect → log → receive).

### Deliverables

- Full UI at `http://localhost:9735`: server management, profile management,
  per-server tool browsing (prefixed vs original), minimal traffic stats, live
  logs.

---

## Phase 6: Export, security, performance, docs

**Goal:** Production-ready edges and the observability export path.

### Items

1. **OTLP export (`internal/audit/export.go`):** emit metrics, logs, and traces
   over OTLP to an OTel Collector / Grafana Alloy (Prometheus / Loki / Tempo).
   - Each `tools/call` is a span (client → garmx → upstream).
   - Metrics are counters + latency histograms with **bounded** labels
     (`server`/`tool`/`status`); keep high-cardinality ids out of labels.
   - **Tiered by privacy:** metrics on by default; logs/traces (which carry
     real tool args/results) opt-in per destination, redacted.
   - Weigh the OTel SDK against a hand-rolled OTLP/HTTP emitter (the
     `<100 lines?` rule).
2. **Security (the daemon holds every credential):**
   - Mask `env`/`headers` in the UI.
   - Bind `127.0.0.1` by default; `0.0.0.0` only via explicit flag with a
     warning.
   - Parameterized SQL everywhere; CSRF tokens on mutation endpoints.
3. **Error handling:** upstream crash mid-request → error to client + log +
   backoff restart; per-request timeout (default 30s); full input queue →
   MCP error, not a block; SQLite down → drop audit with a warning, never crash
   the gateway.
4. **Config:** JSONC parse (comment strip → `encoding/json`); validate on load
   (name regex, no duplicates, command resolves); optional hot reload (SIGHUP).
5. **Benchmarking (right-sized):** measure aggregator overhead per request and
   memory with `pprof`; report p50/p95/p99. **Do not chase sub-millisecond
   budgets** — end-to-end latency is dominated by upstream/model time; the goal
   is "no surprising overhead," not microseconds.
6. **Docs:** README, `garmx -h`, example config, and the first tutorials:
   **connecting Claude Code** and **connecting OpenCode** to GarmX.

### Deliverables

- OTLP export to the Grafana family; metrics by default, logs/traces opt-in.
- Security review done; credentials never leak to logs/UI/export.
- Robust error handling and shutdown.
- Benchmarks documented; README + client tutorials.

---

## Testing strategy summary

| Level       | Tool                        | Scope                                              |
|-------------|-----------------------------|----------------------------------------------------|
| Unit        | `go test`                   | pkg/mcp, aggregator naming/profile/capabilities, registry |
| Integration | `go test` + test upstreams  | aggregator with mock stdio/http upstreams, API     |
| Protocol    | real client (Claude Code)   | initialize/list/call acceptance at each phase gate |
| Performance | `go test -bench` / pprof    | aggregator overhead, allocations                   |
| UI          | Templ render + manual       | fragment rendering, HTMX, WebSocket                |

Naming: unit `*_test.go`; integration `*_integration_test.go` behind
`//go:build integration`; run with `go test -tags=integration ./...`.

---

## Dependency tracking

| Dependency | Phase | Purpose | Risk |
|------------|-------|---------|------|
| `modernc.org/sqlite` | 3 | Pure-Go SQLite (no CGo) | Low |
| `github.com/a-h/templ` | 5 | Type-safe templates | Medium — CLI needed for build |
| `github.com/coder/websocket` (or `gorilla/websocket`) | 5 | WebSocket log stream | Low |
| `golang.org/x/sync` | 2 | `errgroup` for fan-out | Low |
| OTLP export (`go.opentelemetry.io/otel*` or hand-rolled) | 6 | Metrics/logs/traces export | Medium — weigh SDK vs `<100 lines` |
| `github.com/mark3labs/mcp-go` | — | **Reference only** for wire behaviour, not a dependency | — |

**No HTTP router dependency** — Go 1.22+ `net/http` mux covers method + path
params. Keep `go.mod` lean: before adding anything, ask "can I write this in
<100 lines?" No CGo.
