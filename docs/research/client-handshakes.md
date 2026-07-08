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

### Real session observed

An authenticated `opencode run` session (OpenCode 1.17.15, driven by the local
llama.cpp `qwen3-coder-next` model on `:8005`) was captured — see
[Real session captures](#real-session-captures-authenticated-tool-calling)
below. Summary: OpenCode pulls **only `tools/list`** even in a real session
(never `prompts/list` / `resources/list`), completes real `tools/call`
round-trips with the **bare** tool name, and **does re-fetch `tools/list`** on
`notifications/tools/list_changed`.

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

## Real session captures (authenticated, tool-calling)

Beyond the status/health path, an authenticated agent session was captured for
both clients using the same stdio probe (extended to emit a mid-session
`notifications/tools/list_changed` ~2s after `initialized`, then serve a second
tool `echo_v2`). Each session asked the model to echo five words via separate
sequential `echo` calls, keeping the connection open well past the notification.

- **OpenCode 1.17.15** driven by the local **llama.cpp `qwen3-coder-next`**
  model (`:8005`, OpenAI API). Free; no hosted budget.
- **Claude Code 2.1.197** driven normally (real Anthropic model, Max
  subscription).

### What a real session pulls at startup

| List call | OpenCode (real session) | Claude Code (real session) |
|-----------|-------------------------|----------------------------|
| `tools/list` | ✅ | ✅ |
| `prompts/list` | ❌ never | ✅ |
| `resources/list` | ❌ never | ✅ |
| `resources/templates/list` | ❌ | ❌ |

**Key divergence:** OpenCode consumes **tools only** — even a full session never
calls `prompts/list` or `resources/list`. **Claude Code eagerly discovers all
three** primitive types at session start (tools + prompts + resources, but not
`resources/templates/list`). This is *more* than its health-check path, which
pulls only `tools/list`.

### `tools/call` wire shape (verbatim)

OpenCode:

```json
{"method":"tools/call","params":{"name":"echo","arguments":{"text":"alpha"},"_meta":{"progressToken":2}},"jsonrpc":"2.0","id":2}
```

Claude Code:

```json
{"method":"tools/call","params":{"name":"echo","arguments":{"text":"alpha"},"_meta":{"claudecode/toolUseId":"toolu_01JQ…","progressToken":5}},"jsonrpc":"2.0","id":5}
```

- **Bare tool name on the wire.** Both clients display a *prefixed* name to the
  model (OpenCode shows `probe_echo` — single `_`, `server_tool`; Claude Code
  shows `mcp__probe__echo`) but **strip their own prefix and send the bare
  `echo`** to the upstream server. This is exactly the aggregator round-trip
  GarmX implements: prefix client-facing, strip before forwarding upstream.
- **`_meta.progressToken`** is attached by both, equal to the JSON-RPC request
  `id`. Claude Code additionally attaches a namespaced **`claudecode/toolUseId`**
  (its Anthropic `tool_use` id). GarmX must forward `_meta` transparently.
- **Single monotonic `id` counter** across *all* request kinds (initialize,
  every `list`, every `call`, and the re-fetch), starting at 0.

### `notifications/tools/list_changed` — both clients RE-FETCH ✅

This was the one genuine unknown driving `aggregator/notify.go`. **Both clients
re-fetch immediately:**

| | Latency: notification → `tools/list` re-fetch | Re-fetches what |
|---|---|---|
| OpenCode 1.17.15 | ~2 ms | `tools/list` only |
| Claude Code 2.1.197 | ~6 ms | `tools/list` only |

- Both re-requested `tools/list` within milliseconds and received the new
  `echo`+`echo_v2` set — so an upstream tool-set change **does** propagate live
  to a running client if GarmX forwards the notification.
- The re-fetch is a **pure protocol-layer reaction**, independent of the agent
  loop: Claude Code re-fetched *before the model had made any tool call*.
- The notification is **tool-scoped**: neither client also re-pulled
  `prompts/list` / `resources/list` on `tools/list_changed`.

### Client quirks that GarmX's upstream/frontend must tolerate

- **OpenCode sends `notifications/cancelled` after every *completed* call.**
  Right after receiving each `tools/call` result, OpenCode emits
  `{"method":"notifications/cancelled","params":{"requestId":N,"reason":"AbortError: The operation was aborted."}}`.
  The request already succeeded — so GarmX must treat a cancellation for an
  already-finished request id as a **no-op**, never as an error or a reason to
  tear down the upstream call. Claude Code sends **no** such cancellation.
- **No `ping`** was sent by either client during these sessions.

## Implications for GarmX (client-facing side)

1. **Advertise/accept `2025-11-25`.** OpenCode requests the current spec version.
   GarmX's client-facing `initialize` should support it and, when it can, echo
   the client's requested version; lenient downgrade is tolerated but matching
   is cleaner.
2. **Tool aggregation is the hot path; prompts/resources are real too.** In a
   full session OpenCode consumes **only** `tools/list`, but Claude Code eagerly
   pulls `tools/list` + `prompts/list` + `resources/list` at startup. So tools
   are the must-have, but GarmX **must** aggregate prompts and resources — at
   least one first-target client discovers all three every session.
3. **Forward `list_changed` — clients act on it.** Both clients re-fetch
   `tools/list` within milliseconds of `notifications/tools/list_changed`
   (tool-scoped; they do not re-pull prompts/resources). GarmX's
   `aggregator/notify.go` propagation path is worth building: an upstream tool
   change reaches a live client if GarmX forwards the notification. Debounce to
   avoid storms, but the fan-out lands.
4. **Pass `_meta` through and no-op stale cancellations.** `tools/call` carries
   `_meta` (`progressToken`, plus Claude Code's `claudecode/toolUseId`); forward
   it transparently upstream. OpenCode also emits `notifications/cancelled` for
   *already-completed* calls — GarmX must treat a cancel for an unknown/finished
   request id as a no-op.
5. **`roots` is client-advertised.** A client may offer `roots`; GarmX's deferred
   server→client story means it won't call `roots/list` yet — fine, but the
   session model should record the client's advertised capabilities.
6. **Config surface for tutorials:** the OpenCode "connect GarmX" tutorial is a
   one-block `opencode.json` with `type:"local"`, `command:["garmx","--stdio"]`.

---

## Appendix — the capture probe (reproducible)

Throwaway stdio MCP server used for all captures above. Self-contained (stdlib
only). Rebuild with `go build -o probe .` in an empty dir containing this as
`main.go` (and a `go mod init mcpprobe`). Logs every inbound line and every
reply to `$MCPPROBE_LOG` (default `./mcpprobe.log`). It replies enough to
complete a connection and a full `tools/call`, and — ~2s after
`notifications/initialized` — emits `notifications/tools/list_changed` and then
serves a second tool `echo_v2`, so a client re-fetch is visible as a materially
different `tools/list` response.

### Reproduce the status/health capture (initialize + `tools/list` only)

- **OpenCode:** put `{"$schema":"https://opencode.ai/config.json","mcp":{"probe":
  {"type":"local","command":["/abs/probe"],"enabled":true,"environment":
  {"MCPPROBE_LOG":"/abs/probe.log"}}}}` as `opencode.json` in a dir, run
  `opencode mcp list` there.
- **Claude Code:** `claude mcp add probe /abs/probe -e MCPPROBE_LOG=/abs/probe.log
  -s local` then `claude mcp list`, then `claude mcp remove probe -s local`.

### Reproduce the real tool-calling session (prompts/resources, `tools/call`, `list_changed`)

The session must stay open past the 2s notification timer — ask for several
sequential tool calls.

- **OpenCode against the local llama.cpp model.** Add a provider block pointing
  at the OpenAI-compatible endpoint alongside the `mcp` block in `opencode.json`:

  ```json
  {
    "provider": {
      "llamacpp": {
        "npm": "@ai-sdk/openai-compatible",
        "options": { "baseURL": "http://localhost:8005/v1", "apiKey": "dummy" },
        "models": { "qwen3-coder-next": { "name": "Qwen3 Coder Next" } }
      }
    }
  }
  ```

  Then run (in that dir):
  `opencode run "echo alpha, bravo, charlie, delta, foxtrot one at a time via the echo tool" --model llamacpp/qwen3-coder-next`.

- **Claude Code, run normally** (real Anthropic model): after
  `claude mcp add … -s local`, run headless with the tool pre-approved:
  `claude -p "echo alpha, bravo, charlie, delta, foxtrot one at a time via the probe echo tool" --allowedTools mcp__probe__echo`,
  then `claude mcp remove probe -s local`.

```go
// Command mcpprobe is a throwaway stdio MCP server used to capture a real
// client session. It replies just enough to complete a connection and a full
// tools/call round-trip, and — a short time after notifications/initialized —
// emits notifications/tools/list_changed and then begins serving a *different*
// tools/list. That lets us observe whether a real client re-fetches tools/list
// on the notification.
//
// Every inbound line and every reply is appended to $MCPPROBE_LOG
// (default ./mcpprobe.log).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// state tracks whether the mid-session tools/list_changed has fired yet, so
// tools/list can serve a different set before and after. Guarded by mu, which
// also serializes stdout writes across the scanner loop and the timer
// goroutine.
type state struct {
	mu           sync.Mutex
	toolsChanged bool
}

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

	st := &state{}

	logline := func(dir, s string) {
		fmt.Fprintf(lf, "%s %s %s\n", time.Now().Format(time.RFC3339Nano), dir, s)
	}
	logline("META", "==== mcpprobe started, args="+fmt.Sprint(os.Args[1:]))

	out := bufio.NewWriter(os.Stdout)
	// send serializes all stdout writes and their logging under st.mu so the
	// list_changed timer goroutine cannot interleave with the scanner loop.
	send := func(v any) {
		b, _ := json.Marshal(v)
		st.mu.Lock()
		logline("SENT", string(b))
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
		st.mu.Unlock()
	}

	// toolsList returns the current tool set. Before list_changed only "echo"
	// exists; after, a second tool "echo_v2" appears so a re-fetch is visible
	// in the log as a materially different response.
	toolsList := func() []any {
		st.mu.Lock()
		changed := st.toolsChanged
		st.mu.Unlock()
		tools := []any{map[string]any{
			"name": "echo", "description": "Echoes its input back.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []any{"text"}},
		}}
		if changed {
			tools = append(tools, map[string]any{
				"name": "echo_v2", "description": "Echoes input back, twice (added mid-session).",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []any{"text"}},
			})
		}
		return tools
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
					"instructions": "Probe server for handshake capture. Call the echo tool to test.",
				},
			})
		case "notifications/initialized":
			// Fire list_changed ~2s later, then flip the served tool set. A real
			// agent session usually stays open long enough to see this.
			go func() {
				time.Sleep(2 * time.Second)
				st.mu.Lock()
				st.toolsChanged = true
				st.mu.Unlock()
				logline("META", "emitting notifications/tools/list_changed")
				send(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
			}()
		case "tools/list":
			send(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(msg.ID),
				"result": map[string]any{"tools": toolsList()},
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
