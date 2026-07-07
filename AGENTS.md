# AGENTS.md — Agent Instructions & Project Standards

## Validation — run after EVERY change

After every code change, before considering the work done, run:

```
make check
```

This runs, in order and failing fast: `fmt` (gofumpt) → `lint`
(golangci-lint) → `vet` (go vet) → `test` (`go test -race -count=1 ./...`) →
`build`. It must pass with zero errors. Do **not** move on, commit, or report a
task complete while `make check` is red.

Individual targets (use while iterating; `make check` is the gate):

| Command | What it validates |
|---------|-------------------|
| `make fmt`   | Formatting is canonical (`gofumpt`). |
| `make lint`  | Static analysis passes (`golangci-lint`). |
| `make vet`   | `go vet` finds no suspicious constructs. |
| `make test`  | All tests pass under the race detector. |
| `make build` | The binary compiles. |
| `make templ` | `.templ` files regenerate (run before build once UI exists). |

If you change `.templ` files, run `make templ` before `make check`.

Rule: **a change is not finished until `make check` is green.**

## Commenting Standards

**Add a meaningful doc comment to every Go function and method** — exported or
not. The comment states *why the code exists*, its invariants, preconditions,
and side effects — not a restatement of what the code plainly does. Details and
examples below.

Every exported Go identifier **must** have a doc comment following the Go
style:

```go
// Package upstream manages transports to registered MCP servers:
// subprocess (stdio) and Streamable HTTP lifecycle and I/O.
package upstream

// stdioTransport manages the lifecycle and I/O of a single MCP server
// subprocess over stdio transport.
type stdioTransport struct { ... }

// Start launches the subprocess and begins reading/writing on its
// stdio streams. Returns an error if the process cannot be started.
func (t *stdioTransport) Start() error { ... }
```

Rules:
- **First word** of the comment is the identifier name being documented
  (Go convention).
- **Exported** types, funcs, consts, vars, and package declarations require
  doc comments.
- **Unexported** but non-trivial functions also get a comment explaining
  *why* (not *what* — the code shows what).
- One-line comments for simple accessors/helpers. Multi-line for complex
  logic.
- Comments explain rationale, edge cases, and invariants — not "this calls X"
  (the code is the source of truth for what).
- Comments use proper English sentences (capitalize, punctuate).
- **Never reference roadmap phase numbers or transient milestones** (e.g.
  "Phase 0", "later phases") in code comments — they go stale. Describe the
  code's current behavior and intent. The roadmap lives in
  `docs/implementation.md` and `todo.md`.

### Comment quality checklist
- [ ] Does this comment explain *why* this code exists, not just *what* it
      does?
- [ ] Does it mention any invariants, preconditions, or side effects?
- [ ] Would I understand this code 6 months from now reading only the
      comments?
- [ ] Is there any commented-out code? (Never commit commented-out code.)
- [ ] Are there any `TODO` or `FIXME` markers? (Only acceptable in active
      development branches, never on main.)

---

## Go Project Best Practices

### Project Layout
- Follow the standard Go project layout: `cmd/` for binaries, `internal/`
  for private packages, `pkg/` for shared/public packages.
- Keep `main.go` thin — parse flags, load config, wire dependencies, start
  server. Logic lives in `internal/`.
- One package per directory. Short package names (lowercase, no underscores).
- The concrete package split (`aggregator`, `upstream`, `frontend`,
  `registry`, `audit`, `pkg/mcp`, …) is defined in `docs/architecture.md`.
  Keep the three core concerns separate: **aggregator** (protocol logic),
  **upstream** (transports to real servers), **frontend** (client-facing
  endpoints).

### Code Style
- Run `gofumpt` (stricter version of `go fmt`) before every build.
- Use `go vet` as a pre-flight check — catches subtle bugs (e.g.,
  `*errors.New` misuses).
- Imports grouped: stdlib → third-party → internal. Use `goimports` to
  manage this.
- Prefer `any` over `interface{}` (Go 1.18+).
- Prefer `errors.New` for simple errors, `fmt.Errorf` with `%w` for wrapped
  errors.
- Use `slog` (Go 1.21+) for structured logging. No global loggers — pass
  `*slog.Logger` through constructors.
- Avoid `init()` functions. Use explicit constructors (e.g., `NewHub`).

### Concurrency
- Use goroutines + channels for communication. Avoid `sync.Mutex` where
  channels suffice.
- When using `sync.Mutex`, document what it protects.
- Never export channels from a package. Channel ownership stays internal.
- Context propagation: first argument to any blocking function is `ctx
  context.Context`.
- Always handle goroutine lifetime — ensure they terminate on shutdown
  (use `errgroup` or `sync.WaitGroup` + context cancellation).
- Use `sync.WaitGroup` or `errgroup.Group` for tracking goroutine completion.
- Leaked goroutines are bugs. Every goroutine must have a defined shutdown
  path.
- **Correlate upstream responses by JSON-RPC `id`** via the per-upstream
  pending map (`upstream/pending.go`) — never by receive order off a shared
  channel. Concurrent in-flight requests to one upstream otherwise misdeliver.

### Error Handling
- Check errors. Never use `_` to discard an error unless you've consciously
  decided it's safe (and document why).
- Return early, nest minimally. Avoid `if err == nil { ... } else { ... }`
  patterns.
- Use sentinel errors (`var ErrNotFound = errors.New(...)`) for expected
  error values. Use error types for errors that carry additional context.
- Wrap external errors with context: `fmt.Errorf("reading config: %w", err)`.

### SQLite
- Always use parameterized queries (`?` placeholders). Never string
  interpolation for SQL values.
- Use WAL mode (`PRAGMA journal_mode=WAL`) for concurrent read/write.
- Use `modernc.org/sqlite` (pure Go, no CGo dependency).
- Use one **dedicated writer** connection (`SetMaxOpenConns(1)` on the write
  handle) and a **separate read pool** (`*sql.DB`) so reads don't serialize
  behind the writer. Don't claim concurrent reads while pinning a single
  handle to one connection.

### Testing
- **Table-driven tests** for all logic-heavy functions. Example:
  ```go
  func TestRoute(t *testing.T) {
      tests := []struct {
          name       string
          method     string
          wantServer string
          wantMethod string
          wantErr    bool
      }{ ... }
      for _, tt := range tests { t.Run(tt.name, func(t *testing.T) { ... }) }
  }
  ```
- Use `t.Cleanup()` for resource cleanup, not defer (avoids shadowing bugs).
- Name test functions `TestXxx` for unit tests. Use build tags for
  integration tests (`//go:build integration`).
- Run tests with `-race` flag to detect data races.
- Target high coverage on core packages (`pkg/mcp`, `aggregator/naming`,
  `aggregator/capabilities`, `upstream/pending`, `registry/store`).
  Integration tests cover the rest.
- Mock external dependencies with interfaces, not concrete types.
- Test error paths, not just happy paths.

### Dependencies
- Keep `go.mod` lean. Before adding a dependency, ask: "Can I write this
  myself in <100 lines?"
- Prefer stdlib solutions (`slog`, `net/http`, `database/sql`,
  `embed`, `testing`) over third-party libraries.
- Pin dependencies with `go mod tidy` and commit `go.sum`.
- Dependencies requiring CGo are strongly discouraged (cross-compilation
  complexity).

---

## Makefile Targets

Targets must be run (in order) before every push/PR:

| Command | What it does |
|---------|-------------|
| `make fmt` | Run `gofumpt` on all Go files |
| `make lint` | Run `golangci-lint` |
| `make vet` | Run `go vet ./...` |
| `make test` | Run `go test -race -count=1 ./...` |
| `make build` | Build the binary |
| `make check` | Run all of the above in sequence |

Additional targets:

| Command | What it does |
|---------|-------------|
| `make dev` | Rebuild + run on file changes (uses `entr`) |
| `make clean` | Remove build artifacts |
| `make templ` | Regenerate Templ (`.templ.go`) files |
| `make coverage` | Run tests with coverage report |

The CI pipeline must run `make check` as its main step.

---

## Pre-commit Checklist

Before committing:
1. `make check` passes (fmt → lint → vet → test → build)
2. No `TODO` or `FIXME` remain in changed files
3. No commented-out code in changed files
4. New exported identifiers have doc comments
5. Tests exist for new functionality
6. go.mod/go.sum are tidied (`go mod tidy`)
