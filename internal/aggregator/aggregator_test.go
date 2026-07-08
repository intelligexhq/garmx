package aggregator

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/intelligexhq/garmx/internal/upstream"
	"github.com/intelligexhq/garmx/internal/upstream/upstreamtest"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// discardLogger drops output to keep test logs quiet.
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// req builds a client request envelope with a fixed id.
func req(method string, params any) *mcp.Envelope {
	var raw json.RawMessage
	if params != nil {
		b, _ := json.Marshal(params)
		raw = b
	}
	return &mcp.Envelope{JSONRPC: mcp.Version, ID: json.RawMessage(`1`), Method: method, Params: raw}
}

// toolFake returns a Fake exposing the given tool names and advertising caps.
// tools/call echoes the forwarded params so a test can assert the stripped name.
func toolFake(caps string, tools ...string) *upstreamtest.Fake {
	return &upstreamtest.Fake{
		Respond: func(method string, params json.RawMessage) (json.RawMessage, *mcp.Error, error) {
			switch method {
			case mcp.MethodInitialize:
				return json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":` + caps + `,"serverInfo":{"name":"x","version":"1"}}`), nil, nil
			case mcp.MethodToolsList:
				items := make([]string, len(tools))
				for i, name := range tools {
					items[i] = `{"name":"` + name + `"}`
				}
				return json.RawMessage(`{"tools":[` + join(items) + `]}`), nil, nil
			case mcp.MethodToolsCall:
				return params, nil, nil
			default:
				return json.RawMessage(`{}`), nil, nil
			}
		},
	}
}

// join concatenates JSON array elements with commas.
func join(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// twoServerAgg wires an aggregator over servers "alpha" and "beta" with the
// given profile.
func twoServerAgg(t *testing.T, profile Profile) (*Aggregator, *upstreamtest.Fake, *upstreamtest.Fake) {
	t.Helper()
	alpha := toolFake(`{"tools":{"listChanged":true}}`, "echo", "shared")
	beta := toolFake(`{"resources":{"subscribe":true}}`, "ping", "shared")
	mgr := upstream.NewManager(discardLogger())
	if err := mgr.Add("alpha", alpha); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Add("beta", beta); err != nil {
		t.Fatal(err)
	}
	return New(mgr, profile, "test", discardLogger()), alpha, beta
}

// toolNames extracts and sorts the tool names from a tools/list response.
func toolNames(t *testing.T, resp *mcp.Response) []string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("list error: %v", resp.Error)
	}
	var out struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := make([]string, len(out.Tools))
	for i, tool := range out.Tools {
		names[i] = tool.Name
	}
	sort.Strings(names)
	return names
}

// TestInitializeUnionsCapabilities asserts the merged capabilities are the union
// across upstreams (alpha's tools.listChanged and beta's resources.subscribe).
func TestInitializeUnionsCapabilities(t *testing.T) {
	agg, _, _ := twoServerAgg(t, Profile{})
	resp := agg.Handle(context.Background(), req(mcp.MethodInitialize,
		mcp.InitializeParams{ProtocolVersion: "2025-11-25", ClientInfo: mcp.Implementation{Name: "c", Version: "1"}}))
	var res mcp.InitializeResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Capabilities.Tools == nil || !res.Capabilities.Tools.ListChanged {
		t.Errorf("tools.listChanged not unioned: %+v", res.Capabilities.Tools)
	}
	if res.Capabilities.Resources == nil || !res.Capabilities.Resources.Subscribe {
		t.Errorf("resources.subscribe not unioned: %+v", res.Capabilities.Resources)
	}
}

// TestToolsListMergesAndPrefixes asserts tools from both servers appear, each
// prefixed, and that a same-named tool ("shared") is visible from both.
func TestToolsListMergesAndPrefixes(t *testing.T) {
	agg, _, _ := twoServerAgg(t, Profile{})
	got := toolNames(t, agg.Handle(context.Background(), req(mcp.MethodToolsList, nil)))
	want := []string{"alpha___echo", "alpha___shared", "beta___ping", "beta___shared"}
	if len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tools = %v, want %v", got, want)
		}
	}
}

// TestToolsCallRoutesToOwningServer asserts a collided name routes to the server
// named in its prefix and is stripped to the upstream original.
func TestToolsCallRoutesToOwningServer(t *testing.T) {
	agg, alpha, beta := twoServerAgg(t, Profile{})
	// Initialize is not required for calls, but exercise the realistic order.
	agg.Handle(context.Background(), req(mcp.MethodInitialize, mcp.InitializeParams{ProtocolVersion: "2025-11-25"}))

	resp := agg.Handle(context.Background(), req(mcp.MethodToolsCall,
		map[string]any{"name": "beta___shared", "arguments": map[string]string{"k": "v"}}))
	if resp.Error != nil {
		t.Fatalf("call error: %v", resp.Error)
	}
	// beta received a tools/call with the stripped name; alpha received none.
	if !calledWithName(beta, "shared") {
		t.Fatal("beta did not receive tools/call with stripped name 'shared'")
	}
	if calledTool(alpha) {
		t.Fatal("alpha should not have received the call for beta___shared")
	}
}

// calledWithName reports whether the fake got a tools/call whose forwarded name
// equals want.
func calledWithName(f *upstreamtest.Fake, want string) bool {
	for _, c := range f.Sent() {
		if c.Method != mcp.MethodToolsCall {
			continue
		}
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(c.Params, &p) == nil && p.Name == want {
			return true
		}
	}
	return false
}

// calledTool reports whether the fake received any tools/call.
func calledTool(f *upstreamtest.Fake) bool {
	for _, c := range f.Sent() {
		if c.Method == mcp.MethodToolsCall {
			return true
		}
	}
	return false
}

// TestProfileScopesListAndCall asserts a profile's server subset and tool deny
// both hide tools from the list and reject the corresponding call.
func TestProfileScopesListAndCall(t *testing.T) {
	// Only alpha in scope, and its "shared" tool denied.
	agg, _, _ := twoServerAgg(t, Profile{Servers: []string{"alpha"}, ToolDeny: []string{"*___shared"}})
	got := toolNames(t, agg.Handle(context.Background(), req(mcp.MethodToolsList, nil)))
	if len(got) != 1 || got[0] != "alpha___echo" {
		t.Fatalf("scoped tools = %v, want [alpha___echo]", got)
	}
	// Denied tool call is rejected.
	deny := agg.Handle(context.Background(), req(mcp.MethodToolsCall, map[string]any{"name": "alpha___shared"}))
	if deny.Error == nil {
		t.Fatal("denied tool call should be rejected")
	}
	// Out-of-scope server call is rejected.
	oos := agg.Handle(context.Background(), req(mcp.MethodToolsCall, map[string]any{"name": "beta___ping"}))
	if oos.Error == nil {
		t.Fatal("out-of-scope server call should be rejected")
	}
}

// TestNotificationScopedByProfile asserts a list_changed from an out-of-scope
// server is dropped, while an in-scope one reaches the client.
func TestNotificationScopedByProfile(t *testing.T) {
	agg, alpha, beta := twoServerAgg(t, Profile{Servers: []string{"alpha"}})
	got := make(chan *mcp.Notification, 2)
	agg.SetClientNotifier(func(n *mcp.Notification) { got <- n })

	beta.Emit(mcp.NewNotification(mcp.NotifyToolsListChanged, nil))  // out of scope → dropped
	alpha.Emit(mcp.NewNotification(mcp.NotifyToolsListChanged, nil)) // in scope → forwarded

	select {
	case n := <-got:
		if n.Method != mcp.NotifyToolsListChanged {
			t.Fatalf("unexpected notification %q", n.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("in-scope notification was not forwarded")
	}
	// The out-of-scope one must not also arrive.
	select {
	case n := <-got:
		t.Fatalf("out-of-scope notification leaked: %q", n.Method)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestToolsCallRejectsBadName asserts an unprefixed or unknown name is rejected.
func TestToolsCallRejectsBadName(t *testing.T) {
	agg, _, _ := twoServerAgg(t, Profile{})
	resp := agg.Handle(context.Background(), req(mcp.MethodToolsCall, map[string]any{"name": "noprefix"}))
	if resp.Error == nil || resp.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("want invalid-params, got %+v", resp.Error)
	}
}

// TestToolsListRejectsClientCursor asserts a client cursor is refused.
func TestToolsListRejectsClientCursor(t *testing.T) {
	agg, _, _ := twoServerAgg(t, Profile{})
	resp := agg.Handle(context.Background(), req(mcp.MethodToolsList, map[string]string{"cursor": "x"}))
	if resp.Error == nil || resp.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("want invalid-params, got %+v", resp.Error)
	}
}
