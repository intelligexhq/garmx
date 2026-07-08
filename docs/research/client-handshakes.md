# Client Handshake Captures

Raw, reproduced evidence of how each first-target MCP client connects to a
local **stdio** MCP server. Feeds discovery item #1 and the connection
tutorials. Captured with a throwaway stdio "probe" server that logs every raw
line and replies validly enough to complete the handshake.

Method: build a probe that logs stdin + its replies, register it in the client,
trigger a connection, read the log.

---

## OpenCode

- **Version tested:** `opencode` 1.17.13 (Homebrew, macOS).
- **Capture trigger:** `opencode mcp list` (reports per-server *status*, which
  requires a full connect + initialize + `tools/list`).

### Config format (how to register a stdio server)

Project-local `opencode.json` (also honored: `~/.config/opencode/opencode.json`,
`.jsonc`). Local (stdio) server shape:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "probe": {
      "type": "local",
      "command": ["/abs/path/to/server", "--flag"],
      "enabled": true,
      "environment": { "KEY": "value" }
    }
  }
}
```

- `type: "local"` → stdio subprocess; `command` is an **argv array** (argv[0] is
  the executable). `environment` is an object of extra env vars.
- Remote servers use `type: "remote"` with a `url` (Streamable HTTP) instead.
- `opencode mcp add` exists for interactive registration; writing the config
  file directly is equivalent and scriptable.

### Captured `initialize` request (verbatim)

```json
{"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"roots":{}},"clientInfo":{"name":"opencode","version":"1.17.13"}},"jsonrpc":"2.0","id":0}
```

### Method sequence observed

```text
→ initialize                      (id: 0)
← initialize result               (we replied protocolVersion 2025-06-18)
→ notifications/initialized       (no id)
→ tools/list                      (id: 1)
← tools list
(connection closed)
```

### Confirmed facts

| Fact | Value |
|------|-------|
| Requested `protocolVersion` | **`2025-11-25`** (current spec) |
| `clientInfo` | `{"name":"opencode","version":"1.17.13"}` |
| Client capabilities advertised | **`{"roots":{}}` only** — no `sampling`, no `elicitation` in this flow |
| JSON-RPC `id` scheme | plain integers, monotonic, **starts at 0** |
| `notifications/initialized` | sent, no `id` (correct) |
| **Version negotiation is lenient** | We replied `2025-06-18` (a downgrade from the client's `2025-11-25`); OpenCode **accepted it** and proceeded. So a server may answer with any version it supports. |
| `mcp list` fetches | **only `tools/list`** — it did *not* call `prompts/list` or `resources/list` despite our advertising those capabilities |
| `ping` | not sent during this short-lived connection |

### Not yet observed (needs an authenticated `opencode run` session)

`prompts/list`, `resources/list`, `resources/templates/list`, an actual
`tools/call`, `ping` cadence, and whether OpenCode re-fetches on a
`notifications/tools/list_changed`. `opencode run` blocks on model-provider
auth/model selection **before** it starts MCP servers (it hung with no probe
output), so capturing these requires a working model.

**Next session:** run this via the user's **local llama.cpp Qwen Coder** model
(free — point OpenCode's provider at the local endpoint), have the probe emit a
`notifications/tools/list_changed` mid-session, and confirm re-fetch behaviour.
This is the one unknown that drives GarmX's `aggregator/notify.go` path.

---

## Claude Code

- **Version tested:** `claude` 2.1.203 (Claude Code).
- **Capture trigger:** `claude mcp add probe … -s local` (auto-approved local
  scope) then `claude mcp list` (health-checks approved servers = full connect +
  initialize + `tools/list`), then `claude mcp remove`.

### Config format (how to register a stdio server)

Either `claude mcp add <name> <command> [args…] -e KEY=val -s local|user|project`,
or a project `.mcp.json` (project-scope servers show as **⏸ Pending approval**
until approved; `claude mcp add -s local` is auto-approved). `.mcp.json` shape:

```json
{
  "mcpServers": {
    "garmx": {
      "command": "garmx",
      "args": ["--stdio"],
      "env": { "KEY": "value" }
    }
  }
}
```

### Captured `initialize` request (verbatim)

```json
{"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"roots":{"listChanged":true},"elicitation":{}},"clientInfo":{"name":"claude-code","title":"Claude Code","version":"2.1.203","description":"Anthropic's agentic coding tool","websiteUrl":"https://claude.com/claude-code"}},"jsonrpc":"2.0","id":0}
```

### Method sequence observed

Identical to OpenCode: `initialize` (id 0) → `notifications/initialized` →
`tools/list` (id 1) → close. Also accepted our `2025-06-18` downgrade.

### Confirmed facts

| Fact | Value |
|------|-------|
| Requested `protocolVersion` | **`2025-11-25`** |
| `clientInfo` | **rich**: `name:"claude-code"`, `title:"Claude Code"`, `version:"2.1.203"`, `description`, `websiteUrl` |
| Client capabilities advertised | **`roots:{listChanged:true}` + `elicitation:{}`** — no `sampling` |
| `id` scheme | integers from 0 |
| Version negotiation | lenient (accepted server downgrade) |
| Health-check (`mcp list`) fetches | **only `tools/list`** |

---

## OpenCode vs Claude Code — differences that matter

| | OpenCode 1.17.13 | Claude Code 2.1.203 |
|---|---|---|
| `protocolVersion` | 2025-11-25 | 2025-11-25 |
| `clientInfo` fields | name, version | name, **title, description, websiteUrl**, version |
| `roots` | `{}` (no listChanged) | `{listChanged:true}` |
| `elicitation` | **not advertised** | **advertised** `{}` |
| `sampling` | not advertised | not advertised |
| Version negotiation | lenient | lenient |
| Status path pulls | tools/list only | tools/list only |

Both advertise `roots`; **only Claude Code advertises `elicitation`**; neither
advertises `sampling`. This validates deferring server→client callbacks in v1
(no client here even asks GarmX to sample), while confirming the session model
must **record per-client advertised capabilities** (elicitation/roots differ by
client) so the deferred features can light up per-session later.

---

## Implications for GarmX (client-facing side)

1. **Advertise/accept `2025-11-25`.** OpenCode requests the current spec version.
   GarmX's client-facing `initialize` should support it and, when it can, echo
   the client's requested version; lenient downgrade is tolerated but matching
   is cleaner.
2. **Clients may consume only tools.** OpenCode's status path pulls only
   `tools/list`. GarmX must still aggregate prompts/resources for clients that
   use them, but tool aggregation is the must-have hot path.
3. **`roots` is client-advertised.** A client may offer `roots`; GarmX's deferred
   server→client story means it won't call `roots/list` yet — fine, but the
   session model should record the client's advertised capabilities.
4. **Config surface for tutorials:** the OpenCode "connect GarmX" tutorial is a
   one-block `opencode.json` with `type:"local"`, `command:["garmx","--stdio"]`.

---

## Appendix — the capture probe (reproducible)

Throwaway stdio MCP server used for all captures above. Self-contained (stdlib
only). Rebuild with `go build -o probe .` in an empty dir containing this as
`main.go` (and a `go mod init mcpprobe`). Logs every inbound line and every
reply to `$MCPPROBE_LOG` (default `./mcpprobe.log`).

Reproduce a capture:

- **OpenCode:** put `{"$schema":"https://opencode.ai/config.json","mcp":{"probe":
  {"type":"local","command":["/abs/probe"],"enabled":true,"environment":
  {"MCPPROBE_LOG":"/abs/probe.log"}}}}` as `opencode.json` in a dir, run
  `opencode mcp list` there.
- **Claude Code:** `claude mcp add probe /abs/probe -e MCPPROBE_LOG=/abs/probe.log
  -s local` then `claude mcp list`, then `claude mcp remove probe -s local`.

```go
// Command mcpprobe is a throwaway stdio MCP server that logs the client
// handshake. It replies just enough (initialize, tools/list, empty
// prompts/resources, ping) to complete a connection, and appends every inbound
// line and every reply to $MCPPROBE_LOG (default ./mcpprobe.log).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	logPath := os.Getenv("MCPPROBE_LOG")
	if logPath == "" {
		logPath = "mcpprobe.log"
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpprobe: cannot open log:", err)
		os.Exit(1)
	}
	defer lf.Close()

	logline := func(dir, s string) {
		fmt.Fprintf(lf, "%s %s %s\n", time.Now().Format(time.RFC3339Nano), dir, s)
	}
	logline("META", "==== mcpprobe started, args="+fmt.Sprint(os.Args[1:]))

	out := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		b, _ := json.Marshal(v)
		logline("SENT", string(b))
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		logline("RECV", line)

		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			logline("META", "unmarshal error: "+err.Error())
			continue
		}
		hasID := len(msg.ID) > 0 && string(msg.ID) != "null"

		switch msg.Method {
		case "initialize":
			send(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(msg.ID),
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities": map[string]any{
						"tools":     map[string]any{"listChanged": true},
						"prompts":   map[string]any{"listChanged": true},
						"resources": map[string]any{"listChanged": true, "subscribe": false},
						"logging":   map[string]any{},
					},
					"serverInfo":   map[string]any{"name": "mcpprobe", "version": "0.0.1"},
					"instructions": "Probe server for handshake capture.",
				},
			})
		case "tools/list":
			send(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(msg.ID),
				"result": map[string]any{"tools": []any{map[string]any{
					"name": "echo", "description": "Echoes its input back.",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
				}}},
			})
		case "prompts/list":
			send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{"prompts": []any{}}})
		case "resources/list":
			send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{"resources": []any{}}})
		case "resources/templates/list":
			send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{"resourceTemplates": []any{}}})
		case "tools/call":
			// Echo back so a real session completes a full call round-trip.
			send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "echo ok"}},
			}})
		case "ping":
			send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{}})
		default:
			if hasID {
				send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "error": map[string]any{"code": -32601, "message": "method not found: " + msg.Method}})
			}
		}
	}
	if err := sc.Err(); err != nil {
		logline("META", "scanner error: "+err.Error())
	}
	logline("META", "==== stdin closed, exiting")
}
```

> To capture `list_changed` next session, add a goroutine that, ~2s after
> `notifications/initialized`, emits
> `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}` and then
> serves a *different* `tools/list` — observe whether the client re-requests.
