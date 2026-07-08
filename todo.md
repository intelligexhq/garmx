# todo

Tasks to progress GarmX. Design docs now live in `docs/`
(`architecture.md`, `implementation.md`, `discovery.md`).

## Done

- [x] Run the plan through review; split and rewrite into
      `docs/architecture.md`, `docs/discovery.md`, `docs/implementation.md`.
- [x] Correct the core model: aggregator (not byte pipe), `server___tool`
      prefixing, Streamable HTTP (drop legacy SSE), stdio-first client face.
- [x] Reconcile `AGENTS.md` with the new package layout.
- [x] Capture **registration flow** (SQLite as source of truth + `garmx import`
      to adopt existing client configs) and **access scoping** (static,
      curation-first `--profile` subsets) in `docs/architecture.md`; decisions in
      `docs/discovery.md` (DECIDED) with remaining mechanics as OPEN #4a.
- [x] Reconcile `docs/implementation.md` with the daemon/shim, registration,
      profiles, and observability decisions; scrub historical references from
      the docs (snapshot of current thinking only). Add an in-repo Markdown
      linter (`tools/mdlint`, no Node) wired into `make check` via `lint-md`;
      `make fmt-md` auto-fixes. All docs normalized.
- [x] Decide **process model** (one shared daemon; `--stdio` is a thin shim) and
      the **observability & export plane** (raw audit in SQLite + minimal UI +
      OTLP export to the Grafana family; emit-don't-rebuild; redact-before-fork;
      tiered export; size-capped payloads). Folded into `docs/architecture.md`
      ("Process model", "Observability & export") and `docs/discovery.md`
      (DECIDED + OPEN #4b/#4c).

## Start here next session

**Phase 4 — registry/catalog as SQLite source of truth + import/export.**
Make SQLite authoritative for the catalog: `internal/registry` (`store.go`
WAL + read pool, `registry.go` CRUD that starts/restarts upstreams via the
manager), `garmx import`/`export` (adopt Claude Code `.mcp.json` /
OpenCode `opencode.json`; `--config` seeds once), `schema.go` capability cache,
and `internal/health` liveness. See `docs/implementation.md` "Phase 4".

**Reconcile the audit schema deviation while here:** `audit_logs.server_name`
is free text with no FK because the `servers` table did not exist in Phase 3;
add the `server_id` FK (and revisit `tool_exposed`/`tool_original`) now that the
registry lands.

**Coordination note:** Phase 3 shipped option A (shared SQLite file, no daemon;
`garmx ui` reads it read-only). The daemon (option B, discovery #4b) still waits
for a live stream or shared upstreams to justify it.

The probe + `opencode.json` provider template + a two-upstream `garmx.json` live
in the scratchpad; probe source is in `docs/research/client-handshakes.md`.

## Next steps

1. [x] **Phase 0 scaffolding.** Module, package dirs + `doc.go`, Makefile
   (`check` gate), `.golangci.yml`, CI, thin `cmd/garmx/main.go`. `make check`
   green.
2. [x] **Handshake capture — done** (discovery #1). Both the status path
   (OpenCode 1.17.x, Claude Code 2.1.x initialize + `tools/list`) and a real
   tool-calling session (OpenCode on the local Qwen model; Claude Code on the
   real model) captured in `docs/research/client-handshakes.md`. Confirmed:
   lenient `2025-11-25` negotiation; bare-name upstream `tools/call`; both
   re-fetch `tools/list` on `list_changed`; Claude Code pulls prompts+resources
   per session (OpenCode tools-only); OpenCode's post-call `notifications/cancelled`.
3. [x] **Design spike — pinned aggregation + version rules** (discovery #2, #4).
   Decided and encoded with table tests: `server___tool` split (server names
   `[a-z0-9-]`, 1..32); exposed-name warn >60, never truncate; **eager
   page-merge** (no client cursor); client versions `{2025-11-25, 2025-06-18}`
   pref `2025-11-25`; lenient upstream accept + visible mismatch; **union**
   capability merge. In `internal/aggregator/{naming,capabilities}.go` +
   `pkg/mcp/capabilities.go`. Details in `docs/discovery.md` #2/#4 (DECIDED).
4. [x] **Phase 1 — MCP core, one stdio upstream, stdio client.** `pkg/mcp`
   (envelope + methods + parse + framing), `upstream` (Transport, pending demux,
   StdioTransport), `aggregator` dispatch (prefix/split/drain/`_meta`
   passthrough/notify forward), `frontend/stdio`, `cmd/garmx serve --stdio`.
   Unit + subprocess-correlation + end-to-end tests; `make check` green.
   **Acceptance passed:** real Claude Code called `mcp__garmx__probe___echo`
   through GarmX. Daemon/shim split deferred (in-process for now; discovery #4b).
5. [x] **Phase 2 — multi-upstream aggregation, profiles, notify.** `upstream.Manager`;
   aggregator live fan-out merge + prefix + `Split` routing + `uri` ownership;
   `profile.go` (subset + allow/deny globs); `notify.go` (debounced, scoped);
   `internal/config` (servers + profiles) + `serve --config/--profile`. Tests +
   `make check` green. **Acceptance passed:** Claude Code called tools from two
   upstreams through one GarmX, each routed correctly; profiles verified.
6. [x] **Phase 3 (REVISED)** — SQLite audit + minimal read-only UI. `internal/audit`
   (redact → `modernc.org/sqlite` WAL writer, async batched, size-capped,
   best-effort), aggregator `Event`/`Sink` seam, `audit` config block, and a
   read-only `garmx ui` on `:9735` (stat tiles + recent-calls). Option A (shared
   file, no daemon). `make check` green; scripted stdio acceptance passed
   (redaction verified, unwritable-DB path degrades gracefully).
7. [ ] **Phase 4** — registry/catalog as SQLite source of truth + import/export
   (the "start here" item above). Reconcile the audit `server_name` no-FK
   deviation here.
8. [ ] **Daemon/shim split** (discovery #4b) — when the live stream or
   upstream-sharing justifies it (folds into Phase 3 option B, or later).

## Later

- Short blog / X posts: the goal (unified local MCP gateway) and the "why."
- First two tutorials: connecting Claude Code and OpenCode to GarmX.
