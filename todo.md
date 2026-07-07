# todo

Tasks to progress GarmX. Design docs now live in `docs/`
(`architecture.md`, `implementation.md`, `discovery.md`).

## Done
- [x] Run the plan through review; split and rewrite into
      `docs/architecture.md`, `docs/discovery.md`, `docs/implementation.md`.
- [x] Correct the core model: aggregator (not byte pipe), `server___tool`
      prefixing, Streamable HTTP (drop legacy SSE), stdio-first client face.
- [x] Reconcile `AGENTS.md` with the new package layout.

## Start here next session
**Deep-session handshake capture using the local llama.cpp Qwen Coder model**
(free, no hosted-API budget). Point OpenCode's provider at the local endpoint,
re-use the stdio probe pattern from `docs/research/client-handshakes.md`, and
run a real `opencode run` session that invokes the probe's `echo` tool. Capture
the still-unobserved behaviour:
- `prompts/list` and `resources/list` — are they called in a real session?
- a real `tools/call` round-trip (argument shape, result handling).
- **`notifications/tools/list_changed`** — have the probe emit it mid-session
  and confirm whether the client re-fetches `tools/list`. This is the one
  genuine unknown that drives GarmX's notify/propagation path (aggregator
  `notify.go`).
Then repeat the key check against Claude Code. Fold results into
`docs/research/client-handshakes.md` and `docs/discovery.md` #2.

Reusable assets from this session live in the scratchpad (probe source +
`opencode.json`/`.mcp.json` templates) — rebuild the probe with `go build` if
the scratchpad was cleared.

## Next steps
1. [x] **Phase 0 scaffolding.** Module, package dirs + `doc.go`, Makefile
   (`check` gate), `.golangci.yml`, CI, thin `cmd/garmx/main.go`. `make check`
   green.
2. [x] **Handshake capture — core done** (discovery #1). OpenCode 1.17.13 and
   Claude Code 2.1.203 initialize handshakes captured via a stdio probe; results
   in `docs/research/client-handshakes.md`. Both request `2025-11-25`, negotiate
   leniently, pull only `tools/list` on status. **Still open:** an authenticated
   `opencode run` / Claude Code session to observe `prompts/list`,
   `resources/list`, a real `tools/call`, and `list_changed` re-fetch.
3. [ ] **Design spike — pin aggregation + version rules** (discovery #2, #4).
   Decide: prefix length-budget threshold, eager page-merge vs cursor proxy, the
   supported client-side protocol version (evidence says default **2025-11-25**),
   and upstream-mismatch behaviour. Encode as `aggregator/naming` +
   `capabilities` test cases.
4. [ ] **Phase 1** — `pkg/mcp` typed surface + single stdio upstream + client
   acceptance gate, informed by the captures above.

## Later
- Short blog / X posts: the goal (unified local MCP gateway) and the "why."
- First two tutorials: connecting Claude Code and OpenCode to GarmX.
