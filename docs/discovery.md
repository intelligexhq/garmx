# Discovery & Research

What still needs investigation, what is already decided, and what was dropped.
Ordered by impact. The open items block or shape the architecture, so resolve
them early — several before Phase 1.

---

## OPEN — resolve early (these shape the architecture)

### 1. Client integration: Claude Code & OpenCode over stdio

**Question:** Exactly how do Claude Code and OpenCode register and launch a
local stdio MCP server, and what do they expect during `initialize`?

**Why:** They are the first two clients and the subject of the first tutorials.
GarmX must be a drop-in stdio MCP server for both.

**Learn:**
- Claude Code: `claude mcp add` flags and the `.mcp.json` / project config
  shape; how it launches the command; env passing; whether it supports
  `--stdio`-style args cleanly.
- OpenCode: its MCP config file location and schema; stdio launch semantics.
- What `clientInfo`, capabilities, and `protocolVersion` each sends on
  `initialize`, and how each reacts to a version it doesn't prefer.
- Do either surface `tools/list_changed` to the user live, or only at startup?

**Action:** Wire both against a trivial GarmX and capture the real handshake.
Write the two tutorials from these captures.

**Findings** (see `docs/research/client-handshakes.md` for raw evidence):
- **Both clients captured.** Both request `protocolVersion` **2025-11-25**, use
  integer ids from 0, send `notifications/initialized`, negotiate **leniently**
  (accepted a server downgrade to 2025-06-18), and their status/health path
  fetches **only `tools/list`**.
- **OpenCode 1.17.13:** `clientInfo {opencode,1.17.13}`; advertises **only
  `roots:{}`**. Config: `opencode.json` → `mcp.<name>` `type:"local"`,
  `command:[argv…]`, `environment:{}`.
- **Claude Code 2.1.203:** rich `clientInfo` (name/title/description/websiteUrl/
  version); advertises **`roots:{listChanged:true}` + `elicitation:{}`**. Config:
  `claude mcp add … -s local` or project `.mcp.json` `mcpServers.<name>` with
  `command`/`args`/`env`.
- **Neither advertises `sampling`; only Claude Code advertises `elicitation`.**
  Confirms deferring server→client callbacks in v1; the session model must
  record **per-client** advertised capabilities.
- Still unobserved for both: `prompts/list`, `resources/list`, a real
  `tools/call`, and `list_changed` re-fetch behavior — needs an authenticated
  session, not just a status/list command.

---

### 2. Aggregation semantics

**Question:** Precise rules for merging capabilities and routing across
upstreams.

**Why:** This is the core of the product (see architecture.md → aggregator).

**Learn / decide:**
- **Prefixing:** confirmed `server___tool` (AgentCore, triple underscore).
  Split on the **first** `___`. Enforce server-name regex so the split is
  unambiguous. Decide the exact length-budget warning threshold.
- **Resources:** routed by `uri` ownership, **not** prefixed. Confirm no real
  upstreams emit colliding opaque URIs; if they can, add a disambiguation
  scheme.
- **`initialize` capability merge:** union of upstream capabilities. What if
  upstreams disagree on a sub-capability (e.g. `tools.listChanged`)? Decide:
  advertise the capability if **any** upstream has it, and forward
  list-changed accordingly.
- **`list_changed` propagation:** on upstream change → refresh + rebuild merged
  view + emit to clients. Confirm debounce/coalescing to avoid storms.
- **Pagination:** `tools/list` etc. support cursors. Decide whether GarmX
  merges all pages eagerly (simpler; fine for local scale) or proxies cursors.

**Action:** Study MetaMCP, unrelated-ai/mcp-gateway, and AgentCore for edge
cases. Encode decisions as tests in `aggregator/naming` and `capabilities`.

---

### 3. Streamable HTTP wire details (both faces)

**Question:** What exactly does the Streamable HTTP transport require, as a
**client** (to remote upstreams) and as a **server** (client-facing)?

**Why:** Phase 4. Replaces the deprecated HTTP+SSE transport, which GarmX does
**not** implement.

**Learn:**
- The single MCP endpoint contract: `POST` for client→server; `GET`+SSE for
  the server→client stream; when the server may respond on the POST body vs
  the SSE stream.
- Session id: the `Mcp-Session-Id` header lifecycle (issue on initialize, echo
  on subsequent requests, termination).
- Reconnect/resumability (`Last-Event-ID` on the SSE stream) and backoff.
- Origin validation / auth headers for remote upstreams.

**Action:** Read the 2025-11-25 (or current) spec transports section; validate
against a reference server. Note: a 2026-07-28 spec RC exists — check whether
anything relevant changed before Phase 4.

---

### 4. Protocol version negotiation across heterogeneous upstreams

**Question:** GarmX sits between one client and N upstreams that may speak
different spec versions. How does it negotiate?

**Why:** `initialize` carries `protocolVersion`. The client negotiates with
GarmX; GarmX negotiates separately with each upstream.

**Decide:**
- The set of spec versions GarmX supports client-side (start with one current
  version).
- Behaviour when an upstream only speaks an older/newer version: degrade,
  translate, or reject with a clear status in the UI.
- Record each upstream's negotiated version (already in `servers.protocol_version`).

**Action:** Prototype in `capabilities.go`; make mismatch visible, never silent.

---

## OPEN — resolve during implementation (mechanics, not architecture)

### 5. Go child-process lifecycle (stdio upstreams)

Graceful shutdown (SIGTERM → wait → SIGKILL); process groups (`Setpgid`) so a
daemon crash doesn't orphan children; crash detection via `cmd.Wait`; restart
with exponential backoff + max retries; **stderr draining** (a full stderr pipe
blocks the child). Test process-group behaviour on macOS (dev) and Linux.

### 6. stdio framing & large payloads

MCP stdio is newline-delimited JSON; tool results can be multi-MB. Do **not**
use the default `bufio.Scanner` (64KB line cap) — use `bufio.Reader.ReadBytes`
or a scanner with an enlarged buffer. Size channel buffers so a slow upstream
applies backpressure rather than unbounded memory growth.

### 7. SQLite concurrency

`modernc.org/sqlite` (pure Go, no CGo). WAL mode. **One dedicated writer**
connection; a small **separate read pool** if reads must not block on the
writer (don't claim concurrent reads while pinning `MaxOpenConns(1)` on a
single `*sql.DB`). Batch audit writes (flush every ~1s or ~100 rows). Cache
prepared statements for hot queries.

### 8. Security posture

The daemon holds every upstream's credentials — treat it accordingly:
- **Redact on the write path:** secrets in audit payloads (`env`/`headers`
  values, and configurable body patterns: `password`, `token`, `apiKey`,
  `authorization`).
- **Mask in UI:** `env`/`headers` shown as `****`.
- **Bind `127.0.0.1`** by default; `0.0.0.0` only with an explicit flag + warning.
- **Parameterized SQL**; CSRF tokens on mutation endpoints.
- Note: arbitrary command execution is inherent to registering MCP servers —
  "validate the command path" is theatre; the real controls are local-bind,
  credential redaction, and UI masking.

### 9. Benchmarking (right-sized)

Measure aggregator overhead per request and RSS/heap with `pprof`; report
p50/p95/p99. **No sub-millisecond budget** — end-to-end latency is dominated by
upstream and model time. Goal: "no surprising overhead + no goroutine/memory
leaks," verified with a leak-checking `TestMain` and a synthetic-traffic client.

---

## DECIDED — do not re-litigate

| Topic | Decision |
|-------|----------|
| Language | **Go.** Single binary, goroutines, high velocity. The Rust comparison is closed — micro-latency is irrelevant for a local LLM tool. |
| HTTP router | **stdlib `net/http` mux** (Go 1.22+). No Chi/Gin. |
| Config format | **JSONC** (JSON + comment strip). Matches the ecosystem's JSON MCP configs. |
| SQLite driver | **`modernc.org/sqlite`** (pure Go, no CGo). |
| UI stack | **HTMX + Templ**, embedded assets, single binary. |
| Live log transport (UI) | **WebSocket** (server→UI). Distinct from the client-facing MCP transports. |
| Client-facing transports | **stdio (primary) + Streamable HTTP (secondary).** |
| Upstream transports | **stdio + Streamable HTTP.** |
| Name collisions | **`server___tool` prefixing** (AgentCore pattern). |
| Server→client callbacks | **Deferred in v1**; session model keeps the back-ref so it's a later extension, not a rewrite. |

---

## DROPPED — removed from scope

- **Legacy HTTP+SSE transport (2024-11-05).** Deprecated in favour of Streamable
  HTTP (2025-03-26). Not implemented on either face. If a back-compat SSE shim
  is ever needed for an old client, it is an isolated optional adapter — not
  part of the core.
- **JSON-RPC batching.** Removed from MCP (spec 2025-06-18). GarmX does not need
  batch request handling for MCP clients.
- **Namespace-in-method routing** (`server/method`). Based on a misreading of
  MCP: methods are fixed protocol verbs (`tools/call`), the `/` is a category,
  not a server namespace. Replaced entirely by tool-name routing + prefixing.
- **Sub-millisecond latency target / Rust rewrite / byte-pool micro-opt as a
  headline goal.** Wrong axis for a local LLM gateway. Keep normal Go hygiene;
  measure, don't chase microseconds.
- **Naming note — "registry":** GarmX's catalog is a *local server catalog*, not
  the public **MCP Registry** (registry.modelcontextprotocol.io). Keep the
  distinction in docs/UI copy. (Optional future idea: use the public registry as
  a discovery source when adding servers.)
