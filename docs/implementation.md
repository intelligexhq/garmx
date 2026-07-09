# Implementation Plan

Phased build plan for GarmX. Each phase produces a working, testable
increment. The sequencing is deliberate: **prove the MCP aggregation core with
a real client (Claude Code / OpenCode) as early as possible** — that is the
make-or-break risk, so it comes before persistence and UI. The second deliberate
move is **observability-first**: the audit slice (Phase 3) is pulled ahead of
full persistence and UI because GarmX's positioning leads with observability &
audit (see [`research/mcp-protocol-map.md`](research/mcp-protocol-map.md)
Part 0) — the differentiator must be real early, not last.

## Architecture recap

The plan below assumes the model defined in `architecture.md`:

- **One shared daemon.** A single long-lived process owns all upstream MCP
  servers, their credentials, the SQLite catalog + audit store, and the
  management UI on `:9735`. Every client connection is a session against it.
- **stdio is a thin shim.** `garmx serve --stdio` proxies a client's stdio
  JSON-RPC to the daemon over a local channel, auto-starting the daemon if none
  runs. The shim holds no state. *(Target design; deferred past Phase 1, which
  runs the aggregator in-process — see the Phase 1 scope note.)*
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

**User value:** none directly — enabling work (module, gates, CI) so every later
phase ships on a green `make check`.

Go module, package directories with `doc.go`, Makefile with the `check` gate,
`.golangci.yml`, CI running `make check`, and a thin `cmd/garmx/main.go`.
`make check` is green.

---

## Phase 1: MCP core — one upstream, stdio client — done

**User value:** point a real AI client (Claude Code / OpenCode) at GarmX and
have it work like any MCP server — the beachhead that proves the whole approach.

**Goal:** Claude Code (or OpenCode) launches `garmx serve --stdio`; a full
`initialize` → `tools/list` → `tools/call` round-trip works against **one**
registered stdio upstream. No aggregation across many servers yet, no
persistence, no UI. **Done** — see the acceptance note below.

This phase de-risks the core: protocol correctness, stdio framing, and the
response demultiplexer.

> **Scope note — daemon/shim deferred.** `garmx serve --stdio` currently runs
> the aggregator **in-process** against the upstream (one process per client
> connection), not as a thin shim relaying to a shared daemon. The daemon/shim
> split (single-instance daemon, local-socket relay, auto-start, reconnect) is
> unresolved mechanics (discovery #4b) and only pays off once multiple clients
> must share upstreams and the UI daemon exists. The aggregator/session/frontend
> seam is transport-agnostic, so introducing the daemon later is additive, not a
> rewrite. Until then, each client launches its own `garmx` which does its own
> upstream handshake.

### Delivered

- `pkg/mcp`: JSON-RPC envelope + typed initialize/list/call surface, fast
  `Parse`/`Kind` classification, shared newline framing (`ReadMessage`/
  `WriteMessage`, multi-MB safe).
- `internal/upstream`: `Transport` interface; `pending` id→chan demux
  (concurrency-correct, race-tested); `StdioTransport` (subprocess, process
  group, stderr drain, id allocation, graceful stop).
- `internal/aggregator`: dispatch (initialize/tools/prompts/resources/ping),
  `server___tool` prefix on the way out and split on the way in, eager
  page-merge with client-cursor rejection, `_meta` pass-through, upstream→client
  notification forwarding; builds on the spike's `naming`/`capabilities`.
- `internal/frontend`: client-facing stdio server (serialized writes, pushed
  notifications).
- `cmd/garmx`: `serve --stdio` wiring with `--upstream-*` flags and signal-based
  shutdown.
- Tests: `pkg/mcp` classification/round-trip; `pending` concurrency; a real
  re-exec subprocess correlation test; aggregator dispatch (prefix/split/drain/
  cursor/notification); an end-to-end stdio frontend round-trip. `make check`
  green.
- **Acceptance:** real **Claude Code** registered `garmx` (fronting a stdio
  probe), connected, and a live session called `mcp__garmx__probe___echo` — GarmX
  stripped the prefix to `echo`, routed to the probe, and returned the result
  with `_meta` preserved.

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

## Phase 2: Aggregation — many upstreams, prefixing, profiles — done

**User value:** register many MCP servers and expose them to an agent as **one**
— no per-client config sprawl — and scope each agent to just the tools it needs
(better tool selection, lower token cost). This is GarmX's core promise.

**Goal:** Register N upstreams; GarmX presents the merged capability set with
`server___tool` prefixing, routes calls back correctly, and scopes the merged
view per session profile. **Done** — see the acceptance note below.

> **Design choices.** (1) **Live fan-out, no cache.** Lists are merged fresh on
> each request (each upstream drained to exhaustion) rather than cached; a client
> re-fetching after `list_changed` always gets a fresh view, so no cache
> invalidation is needed. A per-profile cache is a later optimization, not a
> correctness need. (2) **Routing needs no route map.** Since the prefix *is* the
> server name, `Split` yields the owning server directly; only resources (uri, not
> prefixed) keep a `uri→server` ownership map, populated on `resources/list`.
> (3) **Auto-restart deferred.** The Manager does start/stop/lookup/notification
> routing; crash-restart with backoff is deferred to the error-handling phase.

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

### Delivered

- `internal/upstream/manager.go`: named-transport set, StartAll/StopAll,
  Get/Names, per-upstream notification tagging.
- `internal/aggregator`: multi-upstream live fan-out merge (tools/prompts
  prefixed, resources by uri ownership), union capabilities, `Split`-based tool
  routing, `profile.go` (server subset + tool allow/deny globs, deny wins),
  `notify.go` (debounced, profile-scoped forwarding of upstream `list_changed`).
- `internal/config`: JSON config (servers + profiles) with validation;
  `serve --config` / `--profile` wiring, single-upstream flags retained.
- Tests: profile filtering, notifier coalescing, manager lifecycle/tagging,
  config validation, and a two-upstream aggregator suite (union caps, merged
  prefixed list, same-named tool routed to the right server, profile scoping,
  scoped notifications). `make check` green.
- **Acceptance passed:** real **Claude Code**, one `garmx` fronting **two**
  stdio upstreams, called `mcp__garmx__alpha___echo` and
  `mcp__garmx__beta___echo`; each routed to the correct upstream (name stripped,
  `_meta` preserved). Profile scoping verified (`--profile solo` → alpha only;
  a `*___echo` deny → empty list).

---

## Phase 3: Observability slice — SQLite audit + minimal UI — done

**User value:** see every MCP call your agents make in one place — tool, server,
latency, errors, redacted args — so GarmX's audit/observability differentiator
is real and visible, not just promised.

**Goal:** every client transaction is written to SQLite (redacted, size-capped),
and a **read-only** UI on `:9735` shows recent calls + basic stats. No
registry-in-SQLite, no live WebSocket, no auth — the smallest thing that makes
the audit plane real and visible.

### Scope (deliberately minimal)

1. **`internal/audit`** — `redact.go` (strip secret-ish fields on the write
   path), `store.go` (`modernc.org/sqlite`, WAL, single writer conn), `audit.go`
   (async batched writer; size-cap payloads with a truncation marker; record
   `payload_bytes`/`truncated`). The aggregator emits one row per client
   request/response (server, method, exposed+original tool, latency, error).
2. **`internal/api` + `internal/ui`** — one read-only page: stat tiles (calls,
   error rate, p50/p95) and a recent-calls table, served from `audit_logs` with
   indexed queries. Plain HTML + a small poll (no WebSocket yet); Templ optional.
3. **Coordination — DECIDED: shared SQLite file, no daemon.** Each `serve
   --stdio` opens the shared audit SQLite (WAL + a busy-timeout) and appends its
   rows; a separate `garmx ui` command opens the same DB **read-only** and serves
   the page on `:9735`. This keeps "SQLite is the source of truth" and defers the
   daemon (discovery #4b) until a live stream or shared upstreams actually
   justify it. Consequences to honor:
   - Concurrent writers: multiple stdio processes write the same DB, so use WAL,
     a `busy_timeout`, short transactions, and the batched writer's retry path.
     (This is a real deviation from the "single dedicated writer" note, which
     assumed one daemon; revisit that guidance when the daemon lands.)
   - No live push: the UI **polls** the DB (e.g. every ~2s); the WebSocket live
     stream waits for the daemon (option B, a later phase).
   - `session_id` should be unique per stdio process (generate at startup) so
     the UI can group by client.

### Tests

- Redaction: secrets in args/env never reach the stored payload.
- Audit: batch insert, size-cap truncation, paginated/filtered query (`:memory:`).
- API: stat + recent-calls handlers render from a seeded DB.

### Deliverable

- Run two upstreams through `garmx`, make some calls, open `:9735`, and see the
  calls and per-server stats. The observability differentiator is visible.

### Delivered

- `internal/audit`: `store.go` (`modernc.org/sqlite`, WAL + busy_timeout;
  `OpenWriter` pins one connection per process, `OpenReader` is query-only;
  `Recent` + `Stats` with nearest-rank p50/p95 via ORDER BY + OFFSET), `redact.go`
  (recursive secret-key scrubbing on the write path, config-additive keys),
  `audit.go` (non-blocking `Record`, single background goroutine batching on a
  1s tick / 100 rows, per-payload size cap with a truncation marker, best-effort:
  drops with a warning rather than blocking or crashing).
- `internal/aggregator`: an `Event`/`Sink` seam owned by the producer (keeps the
  aggregator SQLite-free); calls audit themselves in `handleNamedCall` /
  `handleResourcesRead`, and the `all` scope additionally records synthesized
  methods at the `Handle` wrapper.
- `internal/config`: an `audit` block (`enabled`/`dbPath`/`payload`/`scope`/
  `maxPayloadBytes`/`redactKeys`) with `ResolveAudit` layering
  defaults ← file ← flags/env and enum validation.
- `internal/api` + `internal/ui`: a read-only `net/http` server (`GET /`
  dashboard, `GET /logs/{id}` per-transaction detail, `GET /api/logs`,
  `GET /health`) rendering embedded `html/template` pages (stat tiles +
  per-server + recent-calls with 2s meta-refresh; a static detail page with
  pretty-printed request/response bodies and, for failures, the captured error
  code + message).
- `cmd/garmx`: audit wired into `serve` (unique per-process session id,
  `--audit-db`/`--audit-payload`/`--audit-scope`/`--no-audit`, `GARMX_AUDIT_DB`);
  a new `garmx ui` subcommand bound to `127.0.0.1:9735` by default.
- Tests: redaction, audit store/writer (batch, size-cap, non-blocking drop,
  stats), aggregator emit/scope, config resolve/precedence, and API handlers.
  `make check` green.
- **Acceptance:** a scripted stdio client drove `initialize` → `tools/list` →
  `tools/call` through `garmx serve --stdio`; the call was persisted with the
  client/server/exposed+original tool, a secret arg redacted to `[REDACTED]`, and
  `garmx ui` rendered it. An unwritable DB path disabled audit with a warning
  without interrupting the round-trip.

> **Schema deviation to reconcile in Phase 4.** `audit_logs.server_name` is free
> text with **no `server_id` foreign key** — the `servers` registry table does
> not exist yet. `tool_exposed`/`tool_original` are also added beyond the
> architecture-doc schema. Reconcile both when the SQLite catalog lands.

---

## Phase 4: Persistence — registry, catalog

**User value:** GarmX remembers your catalog across restarts, and `garmx import`
sweeps servers already scattered across your Claude Code / OpenCode configs into
one place — then you repoint every client at just `garmx`. The onboarding lever.

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

## Phase 5: Streamable HTTP (both faces)

**User value:** connect **remote** MCP servers and reach GarmX from **remote**
agents over HTTP — and, via per-agent token→profile identity, get real access
control ("which agent may use which tools"), not just launch-time curation.

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
3. **Identity & RBAC (OAuth 2.1 resource server).** On the HTTP face, be a
   correct resource server: publish RFC 9728 Protected Resource Metadata, answer
   401 with `WWW-Authenticate`, and **validate every bearer token's signature,
   expiry, issuer (`iss`, RC SEP-2468), and audience (= GarmX's canonical URI,
   RFC 8707)** — rejecting foreign-audience tokens. Validation is **generic OIDC**
   (discovery + JWKS), so any compliant IdP works with no per-IdP code; **GarmX
   is never an authorization server.** Then map **token → role → profile** so
   scoping is enforceable per connecting agent, not just a launch flag. (Design
   basis: `research/mcp-protocol-map.md` Part 3.5.)
4. **Upstream auth — Model A, no token passthrough.** For a protected remote
   upstream, GarmX acts as *its* OAuth client and holds a **separate** token
   bound to that upstream; it **MUST NOT** forward the client's token
   (confused-deputy / spec-forbidden). **Identity terminates at GarmX**
   (upstreams see "GarmX"); per-user propagation via OBO/token-exchange is a
   deferred, demand-gated capability — never raw passthrough.
5. **Config**: `transport: "streamable-http"`, `url`, `headers` for upstreams;
   `serve --http` / `serve --stdio` (or both) for the daemon.
6. UI: transport-specific fields in add/edit forms.

### Tests

- Mock Streamable HTTP upstream: connect, initialize, route calls.
- Client-facing: drive the `/mcp` endpoint through a Streamable-HTTP client and
  assert an end-to-end call.
- Token→profile: two sessions with different tokens see different scoped views.
- Auth validation: a token with the wrong audience or a bad `iss` is rejected
  (401/403); a valid token resolves to its role→profile.
- No passthrough: the client's token is never sent upstream — the upstream
  receives GarmX's own credential.

### Deliverables

- Remote MCP servers usable as upstreams.
- Clients can connect to GarmX over Streamable HTTP as well as stdio.
- Per-agent scoping enforceable via token identity (audience/`iss`-validated),
  with identity terminating at GarmX and no token passthrough to upstreams.

---

## Phase 6: Embedded UI (HTMX + Templ)

**User value:** manage everything from a browser — add/edit servers, browse each
server's tools (exposed vs original names), and watch live traffic — instead of
hand-editing config files.

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

## Phase 7: Async & interactive tool lifecycle — Tasks, elicitation, progress, cancellation

**User value:** long-running tool calls (deep research, builds, batch jobs) and
mid-call user prompts (a confirmation, a missing field) both work **through**
GarmX instead of breaking behind it — and every one is **audited**. You get one
view of all in-flight long-running MCP operations across every upstream, an audit
trail of what upstreams ask your users for (catch secret-fishing; rate-limit),
and the *reverse* and *long-running* directions of the observability plane that
no single client gives you.

**Goal:** GarmX **brokers** — never executes — the full async/interactive tool
lifecycle between clients and upstreams (long-running Tasks, input-required
elicitation, progress, cancellation) and audits it. Tasks is the spine;
elicitation is its input-required branch.

### Design basis

`research/mcp-protocol-map.md` Part 3.4 (input-required / elicitation) + Part 3.6
(Tasks). Both build on the RC (2026-07-28): input-required uses the multi-round-
trip `InputRequiredResult` (SEP-2322); Tasks is an optional, independently-
versioned extension (SEP-1686 / SEP-2663). Both fit GarmX's existing pass-through
routing — **no task state machine and no bidirectional callback channel are
reimplemented.**

### Order of implementation

1. **Tasks — broker-only routing (the spine).** Advertise the Tasks extension to
   clients as the **union** of upstreams that support it. When an upstream answers
   a `tools/call` with a `CreateTaskResult`, relay it; route `tasks/get` /
   `tasks/result` / `tasks/update` / `tasks/cancel` back to the owning upstream by
   **wrapping the upstream's opaque task ID** with the server name (reversible,
   stateless — same philosophy as `server___tool`). GarmX keeps **only** the
   id→upstream handle + audit rows; the upstream owns task state. *Verify first:*
   confirm real clients treat task IDs as opaque before fixing the wrap format.
2. **Input-required (elicitation) — the interactive branch.** RC multi-round-trip:
   relay the upstream's `InputRequiredResult`; route the client's re-issued call
   (carrying `inputResponses` + opaque `requestState`) to the **same upstream**
   (affinity); preserve `requestState` / `inputRequests` opaquely. Advertise
   `elicitation` to upstreams as the **union** of clients; **auto-decline** cleanly
   if the originating client lacks it.
3. **Progress & cancellation.** Relay `notifications/progress` (by `progressToken`)
   back to the originating session; standardize `tasks/cancel` for task
   cancellation; no-op `notifications/cancelled` for unknown/finished ids
   (OpenCode emits these for completed calls).
4. **Safety mediation.** Flag/deny elicitation that appears to solicit secrets
   (spec: servers MUST NOT); optional per-upstream rate limiting.
5. **Audit / OTel.** Model a task as a **long-lived span** with lifecycle
   transitions (`working` → `completed`/`failed`/`cancelled`) and real duration;
   record input-required exchanges as "upstream → user" events (which upstream,
   prompt/schema, accept/decline/cancel — **never** the entered values when
   sensitive).
6. **Legacy bridge (decide).** Whether to also support clients still on the stable
   `elicitation/create` server→client request model, or require the RC shape
   (open — `research/mcp-protocol-map.md` Part 6, item 2).

**Accepted edge case:** an upstream crash mid-task loses the task (state lived
upstream) → GarmX surfaces `failed`. GarmX is a broker, not durable across
upstream restarts.

### Tests

- Tasks: a mock upstream returns `CreateTaskResult`; GarmX relays it and routes
  `tasks/get`/`tasks/result` (via the wrapped id) to the same upstream; the
  lifecycle is audited.
- Input-required: a mock upstream returns `InputRequiredResult`; the client
  re-issues with `requestState`; GarmX routes the follow-up to the same upstream;
  the exchange is audited.
- Capability mismatch: originating client lacks `elicitation` → GarmX
  auto-declines and nothing hangs.
- Cancellation: `tasks/cancel` reaches the owning upstream; `notifications/cancelled`
  for a finished id is a no-op.
- Upstream crash mid-task → GarmX surfaces `failed`.
- Secret-solicitation heuristic flags a disallowed elicitation.

### Deliverables

- Long-running (Tasks) and interactive (elicitation) tool calls work end-to-end
  through GarmX, broker-only.
- The full async/interactive lifecycle is audited and visible in the UI — the
  reverse and long-running directions of the observability plane.

---

## Phase 8: Export, security, performance, docs

**User value:** pipe GarmX's telemetry into the observability stack you already
run — Grafana / Prometheus / Loki / Tempo — so GarmX **feeds** your dashboards
rather than becoming a second one; plus production-hardened credential handling.

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

## Phase 9: Demo stack — one-command showcase (docker compose)

**User value:** evaluate GarmX in minutes — `docker compose up` and see
aggregation + centralized audit + OTEL observability on real MCP servers with
**zero required secrets**, before committing to it. The adoption on-ramp.

**Goal:** `docker compose up` and, within minutes, an adopter sees GarmX's
value — **aggregation + centralized audit + OTEL observability across multiple
upstreams** — with real MCP servers and **zero required secrets**. This is the
adoption on-ramp, not a product feature; it lives in the repo as a runnable
showcase and the basis for the "getting started" tutorial.

**Depends on:** Phase 5 (Streamable HTTP client-facing face, so containerized
agents/clients reach GarmX at a URL) and Phase 8 (OTLP export, so the
observability panes populate). Design rationale and open questions live in
[`research/mcp-protocol-map.md`](research/mcp-protocol-map.md) → Part 5.

### Components

1. **`garmx`** — Streamable HTTP MCP endpoint + UI on `:9735`; OTLP → the
   collector. The star of the demo.
2. **`grafana/otel-lgtm`** — all-in-one (OTel Collector + Prometheus + Tempo +
   Loki + Grafana) as the **fast path**; one container gives the full LGTM
   stack. A layered variant (discrete services) can follow.
3. **Reference upstreams (2–3)** — at least one **stdio** server (in the garmx
   container) and one **remote Streamable-HTTP** server (peer container) to
   exercise both upstream transports; mix a read-only server with one that has
   side effects (e.g. a Postgres-backed server) so traces are meaningful.
4. **Zero-secret synthetic agent** — a traffic generator that drives
   representative tool calls through GarmX so dashboards light up with **no API
   keys**. Optional real-client overlay (OpenCode → local model, or Claude Code).
5. **Pre-built Grafana dashboard** for GarmX spans/metrics, shipped with the
   demo so value is visible on first load.

### Deliverables

- `docker-compose.yml` (fast path) + optional layered compose (broken-out
  collector/Prometheus/Tempo/Loki/Grafana).
- `demo.jsonc` seeding the upstreams; a pre-provisioned Grafana dashboard.
- A short "run the demo" tutorial.

### Open decisions (tracked in research doc Part 5)

- Which reference upstreams best showcase aggregation *and* audit value.
- Synthetic agent: scripted deterministic traffic vs a small local-model loop.
- Single fast compose vs a layered fast+realistic pair.

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
| `github.com/a-h/templ` | 6 | Type-safe templates | Medium — CLI needed for build |
| `github.com/coder/websocket` (or `gorilla/websocket`) | 6 | WebSocket log stream | Low |
| `golang.org/x/sync` | 2 | `errgroup` for fan-out | Low |
| OTLP export (`go.opentelemetry.io/otel*` or hand-rolled) | 8 | Metrics/logs/traces export | Medium — weigh SDK vs `<100 lines` |
| `github.com/mark3labs/mcp-go` | — | **Reference only** for wire behaviour, not a dependency | — |

**No HTTP router dependency** — Go 1.22+ `net/http` mux covers method + path
params. Keep `go.mod` lean: before adding anything, ask "can I write this in
<100 lines?" No CGo.
