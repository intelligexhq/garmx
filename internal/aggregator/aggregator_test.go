package aggregator

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/intelligexhq/garmx/internal/upstream/upstreamtest"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// discardLogger is a logger that drops output, keeping test logs quiet.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// req builds a client request envelope with a fixed id.
func req(method string, params any) *mcp.Envelope {
	var raw json.RawMessage
	if params != nil {
		b, _ := json.Marshal(params)
		raw = b
	}
	return &mcp.Envelope{JSONRPC: mcp.Version, ID: json.RawMessage(`1`), Method: method, Params: raw}
}

// probeFake returns a Fake upstream that answers initialize, a two-page
// tools/list, tools/call (echoing the received name), and ping.
func probeFake() *upstreamtest.Fake {
	return &upstreamtest.Fake{
		Respond: func(method string, params json.RawMessage) (json.RawMessage, *mcp.Error, error) {
			switch method {
			case mcp.MethodInitialize:
				return json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"probe","version":"9"}}`), nil, nil
			case mcp.MethodToolsList:
				var p struct {
					Cursor string `json:"cursor"`
				}
				_ = json.Unmarshal(params, &p)
				if p.Cursor == "" {
					return json.RawMessage(`{"tools":[{"name":"echo","description":"e"}],"nextCursor":"c2"}`), nil, nil
				}
				return json.RawMessage(`{"tools":[{"name":"ping","description":"p"}]}`), nil, nil
			case mcp.MethodToolsCall:
				// Echo the (already rewritten) params back so the test can assert
				// the name was stripped to the upstream's original.
				return params, nil, nil
			default:
				return json.RawMessage(`{}`), nil, nil
			}
		},
	}
}

// TestInitialize asserts version negotiation, capability surfacing, GarmX's
// serverInfo, and that GarmX completed its own upstream handshake.
func TestInitialize(t *testing.T) {
	fake := probeFake()
	agg := New("probe", "test", fake, discardLogger())

	resp := agg.Handle(context.Background(), req(mcp.MethodInitialize,
		mcp.InitializeParams{ProtocolVersion: "2025-11-25", ClientInfo: mcp.Implementation{Name: "c", Version: "1"}}))
	if resp.Error != nil {
		t.Fatalf("initialize error: %v", resp.Error)
	}
	var res mcp.InitializeResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocolVersion = %q, want 2025-11-25", res.ProtocolVersion)
	}
	if res.Capabilities.Tools == nil || !res.Capabilities.Tools.ListChanged {
		t.Errorf("merged tools capability not surfaced: %+v", res.Capabilities.Tools)
	}
	if res.ServerInfo.Name != "garmx" {
		t.Errorf("serverInfo.name = %q, want garmx", res.ServerInfo.Name)
	}
	// GarmX must have completed its upstream handshake (initialize + initialized).
	var sawInitialized bool
	for _, c := range fake.Notified() {
		if c.Method == mcp.MethodInitialized {
			sawInitialized = true
		}
	}
	if !sawInitialized {
		t.Error("GarmX did not send notifications/initialized to the upstream")
	}
}

// TestToolsListDrainAndPrefix asserts eager page-merge (both pages drained) and
// that every tool name is prefixed with the server name, with no client cursor.
func TestToolsListDrainAndPrefix(t *testing.T) {
	agg := New("probe", "test", probeFake(), discardLogger())
	resp := agg.Handle(context.Background(), req(mcp.MethodToolsList, nil))
	if resp.Error != nil {
		t.Fatalf("tools/list error: %v", resp.Error)
	}
	var out struct {
		Tools      []struct{ Name string } `json:"tools"`
		NextCursor string                  `json:"nextCursor"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := []string{}
	for _, tool := range out.Tools {
		got = append(got, tool.Name)
	}
	want := []string{"probe___echo", "probe___ping"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tools = %v, want %v (both pages, prefixed)", got, want)
	}
	if out.NextCursor != "" {
		t.Errorf("client-facing nextCursor should be empty, got %q", out.NextCursor)
	}
}

// TestToolsListRejectsClientCursor asserts a client-supplied cursor is refused,
// since GarmX issues none.
func TestToolsListRejectsClientCursor(t *testing.T) {
	agg := New("probe", "test", probeFake(), discardLogger())
	resp := agg.Handle(context.Background(), req(mcp.MethodToolsList, map[string]string{"cursor": "x"}))
	if resp.Error == nil || resp.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("want invalid-params for client cursor, got %+v", resp.Error)
	}
}

// TestToolsCallStripsPrefix asserts the exposed name is split back to the
// upstream original before forwarding, and the result passes through.
func TestToolsCallStripsPrefix(t *testing.T) {
	fake := probeFake()
	agg := New("probe", "test", fake, discardLogger())
	resp := agg.Handle(context.Background(), req(mcp.MethodToolsCall,
		map[string]any{"name": "probe___echo", "arguments": map[string]string{"text": "hi"}}))
	if resp.Error != nil {
		t.Fatalf("tools/call error: %v", resp.Error)
	}
	// The fake echoes the forwarded params; assert the name was stripped.
	var fwd struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(resp.Result, &fwd); err != nil {
		t.Fatalf("decode forwarded params: %v", err)
	}
	if fwd.Name != "echo" {
		t.Fatalf("forwarded name = %q, want echo (prefix stripped)", fwd.Name)
	}
	// And the last Send to the upstream was tools/call with the stripped name.
	sent := fake.Sent()
	last := sent[len(sent)-1]
	if last.Method != mcp.MethodToolsCall {
		t.Fatalf("last upstream call = %q, want tools/call", last.Method)
	}
}

// TestToolsCallUnknownServer asserts a name whose prefix is not this server's is
// rejected rather than forwarded.
func TestToolsCallUnknownServer(t *testing.T) {
	agg := New("probe", "test", probeFake(), discardLogger())
	resp := agg.Handle(context.Background(), req(mcp.MethodToolsCall,
		map[string]any{"name": "other___echo"}))
	if resp.Error == nil || resp.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("want invalid-params for unknown server, got %+v", resp.Error)
	}
}

// TestPing asserts ping is answered locally.
func TestPing(t *testing.T) {
	agg := New("probe", "test", probeFake(), discardLogger())
	resp := agg.Handle(context.Background(), req(mcp.MethodPing, nil))
	if resp.Error != nil || string(resp.Result) != "{}" {
		t.Fatalf("ping = %+v / %s, want empty result", resp.Error, resp.Result)
	}
}

// TestUnknownMethod asserts an unhandled method yields method-not-found.
func TestUnknownMethod(t *testing.T) {
	agg := New("probe", "test", probeFake(), discardLogger())
	resp := agg.Handle(context.Background(), req("does/not/exist", nil))
	if resp.Error == nil || resp.Error.Code != mcp.CodeMethodNotFound {
		t.Fatalf("want method-not-found, got %+v", resp.Error)
	}
}

// TestNotificationPassthrough asserts an upstream notification reaches the
// client via the registered notifier.
func TestNotificationPassthrough(t *testing.T) {
	fake := probeFake()
	agg := New("probe", "test", fake, discardLogger())

	got := make(chan *mcp.Notification, 1)
	agg.SetClientNotifier(func(n *mcp.Notification) { got <- n })

	fake.Emit(mcp.NewNotification(mcp.NotifyToolsListChanged, nil))
	select {
	case n := <-got:
		if n.Method != mcp.NotifyToolsListChanged {
			t.Fatalf("forwarded notification method = %q", n.Method)
		}
	default:
		t.Fatal("upstream notification was not forwarded to the client")
	}
}
