# Testing GarmX

How to build and exercise the current build (Phase 3: aggregation + SQLite
audit + read-only UI).

## Mental model

Two processes share one SQLite file — no daemon:

- **`garmx serve --stdio`** is the **writer**, normally launched *by* an AI
  client (Claude Code / OpenCode spawn it and speak JSON-RPC over its
  stdin/stdout). It fronts your upstream MCP servers and audits every call.
- **`garmx ui`** is the **reader** you run yourself; it opens the same database
  read-only and serves a dashboard on `http://127.0.0.1:9735`.

Both default to the same path (`~/.local/share/garmx/audit.db`), so they agree
with no configuration.

## Prerequisites

- Go 1.26+ and `make`.
- At least one stdio MCP server to front — `garmx serve` will not start without
  one. Use a real server you already run, or the tiny echo upstream below.

## Build

```text
make build      # produces ./bin/garmx
```

## 1. Run the gateway

`garmx serve` needs upstreams, supplied one of two ways.

Single upstream via flags:

```text
./bin/garmx serve --stdio \
  --upstream-name fs \
  --upstream-command npx \
  --upstream-arg -y --upstream-arg @modelcontextprotocol/server-filesystem \
  --upstream-arg /tmp
```

Multiple upstreams + profiles via a config file:

```text
./bin/garmx serve --stdio --config garmx.json --profile coding
```

```jsonc
// garmx.json
{
  "servers": [
    { "name": "fs", "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"] }
  ],
  "profiles": [
    { "name": "coding", "servers": ["fs"], "toolDeny": ["*___delete_*"] }
  ],
  "audit": {
    "payload": "request-response",
    "scope": "calls"
  }
}
```

Exposed tool names are prefixed with the server name: `fs`'s `read_file` becomes
`fs___read_file`.

## 2. Connect a client (Claude Code)

Register GarmX as an MCP server. The `command` must be an **absolute path**:

```jsonc
// .mcp.json
{
  "mcpServers": {
    "garmx": {
      "command": "/ABS/PATH/bin/garmx",
      "args": ["serve", "--stdio", "--config", "/ABS/PATH/garmx.json"]
    }
  }
}
```

Then start the client and call one of the exposed tools. Every call is written
to the audit DB.

## Connecting OpenCode to GarmX

A self-contained walk-through using a mock upstream, so you can see traffic flow
without a real MCP server. Everything below lives outside the repo (e.g. under
`/Users/you/tmp/garmx-testkit/`) so it stays separate from the build.

### A mock upstream

`garmx serve` needs at least one upstream to front. This mock exposes two tools
(`echo`, `add`) over stdio and depends only on the Go toolchain:

```text
mkdir -p garmx-testkit/mockmcp && cd garmx-testkit/mockmcp && go mod init mockmcp
# save the program below as main.go, then:
go build -o mockmcp .
```

```go
// main.go — tiny stdio MCP upstream: echo(message) and add(a,b).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func main() {
	r := bufio.NewReaderSize(os.Stdin, 1<<20)
	w := bufio.NewWriter(os.Stdout)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			handle(w, line)
		}
		if err != nil {
			return
		}
	}
}

func handle(w *bufio.Writer, line []byte) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return
	}
	if len(m.ID) == 0 { // notification (initialized, cancelled) — ignore
		return
	}
	var result string
	switch m.Method {
	case "initialize":
		result = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mockmcp","version":"1.0.0"}}`
	case "tools/list":
		result = `{"tools":[` +
			`{"name":"echo","description":"Echo back the given message","inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}},` +
			`{"name":"add","description":"Add two integers a and b","inputSchema":{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]}}` +
			`]}`
	case "tools/call":
		result = callResult(m.Params.Name, m.Params.Arguments)
	case "ping":
		result = `{}`
	default:
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found: %s"}}`+"\n", m.ID, m.Method)
		w.Flush()
		return
	}
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", m.ID, result)
	w.Flush()
}

func callResult(name string, args map[string]any) string {
	text := ""
	switch name {
	case "echo":
		msg, _ := args["message"].(string)
		text = "echo: " + msg
	case "add":
		text = "sum: " + strconv.FormatFloat(num(args["a"])+num(args["b"]), 'f', -1, 64)
	default:
		text = "unknown tool: " + name
	}
	b, _ := json.Marshal(text)
	return `{"content":[{"type":"text","text":` + string(b) + `}],"isError":false}`
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}
```

### Register the mock in GarmX

`garmx-testkit/garmx.json` (use **absolute** paths):

```jsonc
{
  "servers": [
    { "name": "mock", "command": "/ABS/PATH/garmx-testkit/mockmcp/mockmcp" }
  ],
  "audit": { "payload": "request-response", "scope": "calls" }
}
```

Registering an upstream in this build **is** this `servers[]` entry (or the
`--upstream-*` flags). GarmX exposes the mock's tools prefixed as `mock___echo`
and `mock___add`. The live SQLite registry and `garmx import`/`export` are a
later phase.

### Point OpenCode at GarmX

OpenCode reads a project-local `opencode.json` from the directory you launch it
in (also honored: `~/.config/opencode/opencode.json`). Use the project-local
form so GarmX is scoped to this test project and does not affect your other
sessions — it only adds an `mcp` block, leaving your model/provider untouched.

`garmx-testkit/opencode.json`:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "garmx": {
      "type": "local",
      "command": [
        "/ABS/PATH/bin/garmx",
        "serve", "--stdio",
        "--config", "/ABS/PATH/garmx-testkit/garmx.json",
        "--audit-scope", "all"
      ],
      "enabled": true
    }
  }
}
```

`command` is an argv array (argv[0] is the executable — an absolute path).
OpenCode launches `garmx serve` itself; you do not run it by hand.

### See traffic flow

1. Leave `garmx ui` running (`:9735`) — it reads the same default database the
   OpenCode-launched `garmx serve` writes to, so no path wiring is needed.
2. From the testkit directory:

   ```text
   cd garmx-testkit
   opencode mcp list      # health-check: connects + lists tools → already logs rows
   ```

3. Start a session and ask the model to use a tool, e.g. *"use the echo tool to
   echo 'hello garmx'"* or *"use the add tool to add 2 and 40"*.
4. Refresh `http://127.0.0.1:9735` — the `mock___echo` / `mock___add` calls
   appear with latency and status.

Why rows show up immediately: the command sets `--audit-scope all`, so the
`initialize` + `tools/list` that OpenCode issues on connect are recorded — even
`opencode mcp list` alone lights up the UI, before the model calls anything. For
the realistic view (actual tool calls only), drop `--audit-scope all`; the
`garmx.json` above already sets `scope: "calls"`.

Note: OpenCode consumes **tools only** — it does not fetch prompts or resources —
so expect `tools/list` + `tools/call` traffic and nothing else.

## 3. Watch the audit UI

In a separate terminal:

```text
./bin/garmx ui                 # http://127.0.0.1:9735
```

It shows stat tiles (total calls, errors, error rate, p50/p95 latency), a
per-server breakdown, and a recent-calls table that refreshes every 2s. Click a
row's timestamp to open its **detail page** (`/logs/{id}`) — a static view with
the full (redacted, still size-capped) request and response bodies
pretty-printed, plus the client, session, timing, and, for a failed call, the
error code and message. `GET /api/logs` returns the rows as JSON;
`GET /health` reports liveness.

The UI resolves the audit path the same way `serve` does, so with defaults no
flag is needed. If you point `serve` at a custom DB, point the UI at it too:

```text
GARMX_AUDIT_DB=/path/audit.db ./bin/garmx ui --addr 127.0.0.1:9735
```

## Audit configuration

Precedence: built-in default → `audit` block in the config file → flag / env.

| Setting | Flag | Env | Config key | Default |
|---------|------|-----|------------|---------|
| Database path | `--audit-db` | `GARMX_AUDIT_DB` | `dbPath` | `~/.local/share/garmx/audit.db` |
| Capture | `--audit-payload` | — | `payload` | `request-response` |
| Scope | `--audit-scope` | — | `scope` | `calls` |
| Disable | `--no-audit` | — | `enabled: false` | enabled |

- **payload:** `request-response` (args + result) · `request` (args only) ·
  `metadata` (no bodies).
- **scope:** `calls` (tools/call, prompts/get, resources/read) · `all` (also
  initialize and the `*/list` methods).
- Secret-ish fields (`token`, `password`, `apiKey`, `authorization`, …) are
  replaced with `[REDACTED]` before storage; add more with `redactKeys`.
- Payloads over 16 KiB are truncated to a marker; the original size is recorded.
- If the database cannot be opened, `serve` logs a warning and runs **with audit
  disabled** — the gateway never depends on its own audit trail.

## Quick smoke test (no client)

Front any stdio MCP command and pipe a session into it, then open the UI. A
minimal echo upstream (no external deps):

```text
mkdir /tmp/echoup && cd /tmp/echoup && go mod init echoup
cat > main.go <<'EOF'
package main

import ("bufio"; "encoding/json"; "fmt"; "os")

func main() {
	r := bufio.NewReaderSize(os.Stdin, 1<<20)
	w := bufio.NewWriter(os.Stdout)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var m struct{ ID json.RawMessage `json:"id"`; Method string `json:"method"` }
			_ = json.Unmarshal(line, &m)
			if len(m.ID) == 0 { if err == nil { continue } }
			res := `{}`
			switch m.Method {
			case "initialize":
				res = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"echoup","version":"1"}}`
			case "tools/list":
				res = `{"tools":[{"name":"echo","inputSchema":{"type":"object"}}]}`
			case "tools/call":
				res = `{"content":[{"type":"text","text":"echoed"}]}`
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", m.ID, res)
			w.Flush()
		}
		if err != nil { return }
	}
}
EOF
go build -o echoup .
```

Drive one call through GarmX into it:

```text
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"smoke","version":"1"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"upstream___echo","arguments":{"msg":"hi","token":"sk-secret"}}}' \
  | ./bin/garmx serve --stdio --upstream-command /tmp/echoup/echoup
```

You should see the prefixed `upstream___echo` in the `tools/list` reply and an
`echoed` result. Then `./bin/garmx ui` and open the dashboard — the call
appears, with `token` shown as `[REDACTED]`.

## Reset

Delete the database (and its WAL sidecars) to start clean:

```text
rm -f ~/.local/share/garmx/audit.db*
```
