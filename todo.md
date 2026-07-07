# todo

Tasks to progress GarmX. Design docs now live in `docs/`
(`architecture.md`, `implementation.md`, `discovery.md`).

## Done
- [x] Run the plan through review; split and rewrite into
      `docs/architecture.md`, `docs/discovery.md`, `docs/implementation.md`.
- [x] Correct the core model: aggregator (not byte pipe), `server___tool`
      prefixing, Streamable HTTP (drop legacy SSE), stdio-first client face.
- [x] Reconcile `AGENTS.md` with the new package layout.

## Next 3 steps
1. **Phase 0 scaffolding — start now, no blockers.** `go mod init`, package
   dirs, Makefile (`check`/`build`/`test`/`lint`/`templ`), `.golangci.yml`,
   CI running `make check`, thin `cmd/garmx/main.go`.
2. **Research spike — capture the real client handshake** (discovery #1).
   Register a trivial stdio server in **Claude Code** and **OpenCode**; record
   their `initialize` payloads (clientInfo, capabilities, protocolVersion) and
   how each handles `tools/list_changed`. Needed to lock the `pkg/mcp` typed
   surface before Phase 1.
3. **Design spike — pin aggregation + version rules** (discovery #2, #4).
   Decide: prefix length-budget threshold, eager page-merge vs cursor proxy,
   the single supported client-side protocol version, and upstream-mismatch
   behaviour. Encode as `aggregator/naming` + `capabilities` test cases.

## Later
- Short blog / X posts: the goal (unified local MCP gateway) and the "why."
- First two tutorials: connecting Claude Code and OpenCode to GarmX.
