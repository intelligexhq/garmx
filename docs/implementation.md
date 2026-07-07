# Implementation Plan

Phased build plan for GarmX. Each phase produces a working, testable
increment. The sequencing is deliberate: **prove the MCP aggregation core with
a real client (Claude Code / OpenCode) as early as possible** — that is the
make-or-break risk, so it comes before persistence and UI.

---

## Phase 0: Project Scaffolding

**Goal:** Go module with build tooling, linting, and CI skeleton.

### Steps
1. `go mod init github.com/<user>/garmx`
2. Create directory structure (see architecture.md layout):
   ```
   mkdir -p cmd/garmx \
     internal/{config,aggregator,upstream,frontend,registry,audit,health,api,ui/templates,ui/static/{css,js}} \
     pkg/mcp
   ```
3. **Makefile**: `build`, `test` (`go test -race -count=1 ./...`), `lint`
   (`golangci-lint run`), `vet`, `fmt` (`gofumpt`), `templ` (`templ generate`),
   `check` (fmt→lint→vet→test→build), `run`, `dev` (`entr`), `clean`,
   `coverage`.
4. `.golangci.yml`: `errcheck, gosimple, govet, ineffassign, staticcheck,
   misspell, gocritic`; exclude `bin`.
5. CI (`.github/workflows/ci.yml`): setup-go → `make check`.

### Deliverables
- `go.mod`, working `Makefile`, `.golangci.yml`
- `cmd/garmx/main.go` — minimal (parses `serve` flags, logs startup)
- CI green

---

## Phase 1: MCP core — single upstream, stdio, real client

**Goal:** Claude Code (or OpenCode) launches `garmx serve --stdio`, and a full
`initialize` → `tools/list` → `tools/call` round-trip works against **one**
registered stdio upstream. No aggregation yet, no DB, no UI.

This phase de-risks everything: protocol correctness, stdio framing, and the
response demultiplexer.

### Order of Implementation
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
   - **Framing:** newline-delimited JSON read with `bufio.Reader.ReadBytes('\n')`
     (or a scanner with an enlarged buffer) — **never** the default
     `bufio.Scanner` 64KB line cap.
   - **Drain stderr** to the daemon log; a full stderr pipe otherwise blocks
     the child.
   - Read loop dispatches via `pending` (match by id) / notification router.
   - `Start()`, `Stop()` (SIGTERM → wait → SIGKILL), `Health()`.
6. **`internal/upstream/transport.go`** — `Transport` interface:
   `Start/Stop/Send/SetHandlers/Health`. stdio implements it; streamhttp comes
   in Phase 4.
7. **`internal/aggregator/session.go`** — session state (protocol version,
   client caps, back-ref for notifications).
8. **`internal/aggregator/capabilities.go`** — `initialize`: negotiate version
   with the client, initialize the single upstream, return its capabilities.
9. **`internal/aggregator/aggregator.go`** — dispatch by method. With one
   upstream, `*/list` just forwards; `tools/call` forwards. (Merging arrives in
   Phase 2 — keep the dispatch seam ready for it.)
10. **`internal/frontend/stdio.go`** — GarmX-as-stdio-server: read client
    stdin, hand to aggregator, write results to stdout.
11. **`cmd/garmx/main.go`** — `serve --stdio`: load config, start the one
    upstream, wire frontend↔aggregator, handle SIGINT/SIGTERM graceful
    shutdown (stop upstream, flush).

### Tests
- Unit: `pkg/mcp` encode/decode (requests, responses, notifications, errors).
- Unit: `pending` demux — concurrent ids resolve to the correct channel.
- Integration: a **test upstream** (tiny Go program: reads stdin, answers
  `initialize`/`tools/list`/`tools/call`) driven through the aggregator;
  assert correct correlation under concurrency.
- **Manual acceptance:** register `garmx` in Claude Code, confirm the upstream's
  tools appear and one call succeeds. This is the phase's real gate.

### Deliverables
- Real client → GarmX → one stdio upstream works end to end.
- Correct stdio framing (large payloads), stderr drained, ids demultiplexed.
- Graceful shutdown cleans up the child process group.

---

## Phase 2: Aggregation — many upstreams + name prefixing

**Goal:** Register N upstreams; GarmX presents the merged capability set with
`server___tool` prefixing and routes calls back correctly.

### Order of Implementation
1. **`internal/aggregator/naming.go`**
   - `Prefix(server, name) string` → `server___name`.
   - `Split(exposed) (server, name string, ok bool)` — split on **first** `___`.
   - Validate server name `^[a-z0-9][a-z0-9-]*$`; length-budget warning
     (`len(server)+3+len(tool) > 60`).
2. **Merge in `aggregator.go`**
   - `tools/list` / `prompts/list`: fan out to all enabled upstreams, rewrite
     names to prefixed, concatenate; cache the merged view + a
     `exposedName → (server, originalName)` route map.
   - `resources/list`: merge; build a `uri → server` ownership map (no
     prefixing — URIs are already namespaced).
   - `tools/call` / `prompts/get`: `Split` → look up route → forward original
     name to the owning upstream.
   - `resources/read`: route by `uri` ownership.
3. **`internal/upstream/manager.go`** — manage the set of upstreams:
   lifecycle, restart with exponential backoff + max retries, and re-init on
   restart.
4. **`internal/aggregator/notify.go`** — on upstream
   `notifications/tools/list_changed` (or prompts/resources): refresh that
   upstream, rebuild merged maps, emit the corresponding `list_changed` to
   connected client sessions.
5. **Capability merge** (`capabilities.go`): advertise the **union** of upstream
   capabilities to the client (tools if any upstream has tools, etc.); record
   each upstream's negotiated protocol version.

### Tests
- Unit: `naming` prefix/split round-trip, including tool names that themselves
  contain single underscores; reject names containing `___`.
- Unit: merge with two upstreams exposing a same-named tool → both visible,
  both routable.
- Integration: two test upstreams; assert `tools/list` returns the prefixed
  union and each `tools/call` reaches the correct upstream.
- Integration: simulate `list_changed` from one upstream → merged view updates
  and clients are notified.

### Deliverables
- True multi-server aggregation with collision-safe naming.
- list-changed propagation.
- **Acceptance:** Claude Code sees tools from two real MCP servers at once.

---

## Phase 3: Registry + SQLite persistence

**Goal:** Persist the catalog, cache schemas, and record audit logs.

### Order of Implementation
1. **`internal/registry/store.go`** — `Open` (WAL, foreign keys), `Migrate`;
   `modernc.org/sqlite`; single writer conn + read pool.
2. **`internal/registry/registry.go`** — CRUD (`List/Get/Create/Update/Delete`,
   `Enable`); on create/update start/restart the upstream via the manager.
3. **`internal/registry/schema.go`** — cache tools/prompts/resources into
   `capability_cache` on registration and on a periodic refresh (5 min).
4. **`internal/audit/audit.go` + `store.go`** — async batched writer (flush
   every 1s or 100 entries); redaction on the write path (see Phase 6 security,
   but wire the redaction seam now); paginated query.
5. Swap the aggregator's stdout audit for the SQLite writer.
6. **`internal/health/health.go`** — 30s ticker: stdio liveness via
   `Signal(0)` + optional `ping`; http via `ping` RPC; update status; emit
   changes toward the UI stream.

### Tests
- Registry CRUD against `:memory:` SQLite.
- Schema cache with a mock `tools/list`.
- Audit batch insert + filtered query.
- Integration: register via API → appears in list → health reflects it.

### Deliverables
- Persistent catalog, cached schemas, audit logs in SQLite.
- Health status per upstream.

---

## Phase 4: Streamable HTTP (both faces)

**Goal:** GarmX speaks Streamable HTTP as a **client-facing** endpoint and as
an **upstream** client. (Legacy HTTP+SSE is not implemented.)

### Order of Implementation
1. **`internal/upstream/streamhttp.go`** — Streamable HTTP **client** to remote
   upstreams: `POST` client→server messages; open the `GET` SSE stream for
   server→client messages; session-id handling; reconnect with backoff;
   implements `Transport`.
2. **`internal/frontend/streamhttp.go`** — GarmX-as-Streamable-HTTP **server**:
   single MCP endpoint (e.g. `/mcp`), `POST` for requests, optional `GET`+SSE
   stream, per-session state keyed by the MCP session id.
3. **Config**: `transport: "streamable-http"`, `url`, `headers` for upstreams;
   `serve --http` / `serve --stdio` (or both) for the daemon.
4. UI: transport-specific fields in add/edit forms.

### Tests
- Mock Streamable HTTP upstream: connect, initialize, route calls.
- Client-facing: drive the `/mcp` endpoint through a Streamable-HTTP client and
  assert an end-to-end call.

### Deliverables
- Remote MCP servers usable as upstreams.
- Clients can connect to GarmX over Streamable HTTP as well as stdio.

---

## Phase 5: Embedded UI (HTMX + Templ)

**Goal:** Web interface for managing upstreams and watching live traffic.

### Prerequisites
- `go install github.com/a-h/templ/cmd/templ@latest`; `make templ` before build.

### Order of Implementation
1. **`internal/ui/server.go`** — `//go:embed` static assets; templ wiring.
2. **Templates:** `layout`, `dashboard`, `servers`, `server_detail`
   (show **exposed vs original** tool names), `logs`, `components`.
3. **HTMX handlers** in `internal/api/`: detect `HX-Request`; full page vs
   fragment; render via `templ.Render(ctx, w)`.
4. **WebSocket log stream:** `internal/audit/stream.go`
   (`Subscribe/Unsubscribe/Broadcast`); upgrade `GET /api/logs/stream`;
   `static/js/logs.js` appends rows.
5. **CSS:** minimal, dark default (Pico/Water.css or hand-rolled).
6. Wire: add-server form POSTs and HTMX-swaps the table; delete via
   `hx-delete`; dashboard polls health every 10s; log page opens the WebSocket.

### Tests
- Templ component render tests.
- HTMX handler tests (HX-Request detection, content type).
- WebSocket stream test (connect → log → receive).

### Deliverables
- Full UI at `http://localhost:9735`: server management, live logs, per-server
  tool browsing with prefixed/original names, dashboard.

---

## Phase 6: Polish, security, performance

**Goal:** Production-ready edges.

### Items
1. **Security (do these seriously — the daemon holds every credential):**
   - Redact secrets in audit payloads on the **write** path (configurable
     patterns: `password`, `token`, `apiKey`, `authorization`, plus known
     `env`/`headers` keys).
   - Mask `env`/`headers` in the UI.
   - Bind `127.0.0.1` by default; `0.0.0.0` only via explicit flag with a warning.
   - Parameterized SQL everywhere; CSRF tokens on mutation endpoints.
2. **Error handling:** upstream crash mid-request → error to client + log +
   backoff restart; per-request timeout (default 30s); `InChan` full → 503-style
   MCP error, not a block; SQLite down → drop audit with a warning, never crash
   the gateway.
3. **Logging:** `slog` structured daemon logs; audit retention (max age / rows);
   rate-limited error logging.
4. **Config:** JSONC parse (comment strip → `encoding/json`); validate on load
   (name regex, no duplicates, command resolves); optional hot reload (SIGHUP).
5. **Benchmarking (right-sized):** measure aggregator overhead per request and
   memory with `pprof`; report p50/p95/p99. **Do not chase sub-millisecond
   budgets** — end-to-end latency is dominated by upstream/model time; the goal
   is "no surprising overhead," not microseconds.
6. **Docs:** README, `garmx -h`, example config, and the first tutorials:
   **connecting Claude Code** and **connecting OpenCode** to GarmX.

### Deliverables
- Security review done; credentials never leak to logs/UI.
- Robust error handling and shutdown.
- Benchmarks documented; README + client tutorials.

---

## Testing Strategy Summary

| Level       | Tool                         | Scope                                             |
|-------------|------------------------------|---------------------------------------------------|
| Unit        | `go test`                    | pkg/mcp, aggregator/naming, capabilities, registry|
| Integration | `go test` + test upstreams   | aggregator with mock stdio/http upstreams, API    |
| Protocol    | real client (Claude Code)    | initialize/list/call acceptance at each phase gate|
| Performance | `go test -bench` / pprof     | aggregator overhead, allocations                  |
| UI          | Templ render + manual        | fragment rendering, HTMX, WebSocket               |

Naming: unit `*_test.go`; integration `*_integration_test.go` behind
`//go:build integration`; run with `go test -tags=integration ./...`.

---

## Dependency Tracking

| Dependency | Phase | Purpose | Risk |
|------------|-------|---------|------|
| `modernc.org/sqlite` | 3 | Pure-Go SQLite (no CGo) | Low |
| `github.com/a-h/templ` | 5 | Type-safe templates | Medium — CLI needed for build |
| `github.com/coder/websocket` (or `gorilla/websocket`) | 5 | WebSocket log stream | Low |
| `golang.org/x/sync` | 2 | `errgroup` for fan-out | Low |
| `github.com/mark3labs/mcp-go` | — | **Reference only** for wire behaviour, not a dependency | — |

**No HTTP router dependency** — Go 1.22+ `net/http` mux covers method + path
params. Keep `go.mod` lean: before adding anything, ask "can I write this in
<100 lines?" No CGo.
