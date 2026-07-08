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

**Phase 2 — aggregation: many upstreams, prefixing, profiles** (see
`docs/implementation.md` Phase 2). The Phase 1 core is done and the seam is
transport-agnostic. Next: an `upstream.Manager` holding N upstreams (lifecycle,
restart/backoff), fan-out+merge in the aggregator keyed by profile, the
`exposedName → (server, original)` route map (replacing the single-server
Split), `profile.go` (server subset + tool allow/deny), and `notify.go`
(rebuild affected per-profile views + debounced client emit). Acceptance:
Claude Code sees tools from **two** real MCP servers at once, scoped by profile.

Carry-over decisions already coded and ready to extend:

- `server___tool` split + `MergeServerCapabilities` (union) in
  `internal/aggregator`; eager page-merge + client-cursor rejection live in
  `handleList`. Multi-server just adds the route map + per-profile cache.
- Both clients re-fetch `tools/list` on `list_changed` within ms → `notify.go`
  fan-out lands; add debounce.
- OpenCode's post-call `notifications/cancelled` is already dropped as a no-op
  in the aggregator.

**Also pending:** the daemon/shim split (discovery #4b) — deferred past Phase 1,
which runs the aggregator in-process. Revisit when multi-client sharing or the
UI daemon needs it.

The probe + `opencode.json` provider template live in the scratchpad; full
source is in the appendix of `docs/research/client-handshakes.md`.

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
5. [ ] **Phase 2** — multi-upstream aggregation, per-profile merged views,
   `notify.go` propagation. (Now the "start here" item above.)

## Later

- Short blog / X posts: the goal (unified local MCP gateway) and the "why."
- First two tutorials: connecting Claude Code and OpenCode to GarmX.
