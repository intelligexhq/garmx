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

**Phase 1 — MCP core: daemon, one upstream, stdio client** (see
`docs/implementation.md` Phase 1). Goal: Claude Code / OpenCode launches
`garmx serve --stdio`, the shim relays to the daemon, and a full
`initialize` → `tools/list` → `tools/call` round-trip works against **one**
registered stdio upstream — no multi-server aggregation, persistence, or UI yet.
This de-risks protocol correctness, stdio framing, the response demux, and the
shim↔daemon channel.

Design spike is done — the pure aggregation/version rules are pinned and coded
in `internal/aggregator/naming.go` + `capabilities.go` (with table tests) and
`pkg/mcp/capabilities.go` (version constants + capability types). Phase 1 builds
on them. Inputs from the captures already folded in:

- Both clients strip their own display prefix and call upstream with the **bare**
  tool name → GarmX's prefix is purely client-facing; strip before forwarding.
- Both re-fetch `tools/list` on `list_changed` within ms → build `notify.go`
  propagation with debounce (Phase 2).
- Claude Code consumes prompts + resources every session (OpenCode: tools only)
  → aggregate all three primitives, not just tools.
- OpenCode sends `notifications/cancelled` for completed calls → no-op stale
  cancels in the frontend/upstream.

The probe (with mid-session `list_changed` emission) + `opencode.json` provider
template live in the scratchpad; the full source is in the appendix of
`docs/research/client-handshakes.md` — rebuild with `go build` if cleared.

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
4. [ ] **Phase 1** — `pkg/mcp` typed surface + single stdio upstream + client
   acceptance gate, informed by the captures above. (Extends the spike's
   `pkg/mcp/capabilities.go` with `message.go`/`methods.go`/`parse.go`.)

## Later

- Short blog / X posts: the goal (unified local MCP gateway) and the "why."
- First two tutorials: connecting Claude Code and OpenCode to GarmX.
