package aggregator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/intelligexhq/garmx/internal/upstream"
	"github.com/intelligexhq/garmx/internal/upstream/upstreamtest"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// fakeSink collects recorded events for assertions.
type fakeSink struct {
	mu     sync.Mutex
	events []Event
}

func (f *fakeSink) Record(e Event) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
}

func (f *fakeSink) all() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Event(nil), f.events...)
}

// initAgg initializes the aggregator so ClientInfo is populated for events.
func initAgg(t *testing.T, agg *Aggregator) {
	t.Helper()
	agg.Handle(context.Background(), req(mcp.MethodInitialize,
		mcp.InitializeParams{ProtocolVersion: "2025-11-25", ClientInfo: mcp.Implementation{Name: "claude", Version: "2.1"}}))
}

// TestAuditRecordsCall asserts a tools/call emits one event with the resolved
// server and both exposed and original tool names, tagged with the session id
// and client info.
func TestAuditRecordsCall(t *testing.T) {
	agg, _, _ := twoServerAgg(t, Profile{})
	sink := &fakeSink{}
	agg.SetAudit(sink, "sess-123", false)
	initAgg(t, agg)

	resp := agg.Handle(context.Background(), req(mcp.MethodToolsCall,
		map[string]any{"name": "alpha___echo", "arguments": map[string]any{"msg": "hi"}}))
	if resp.Error != nil {
		t.Fatalf("call error: %v", resp.Error)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 (scope=calls): %+v", len(events), events)
	}
	e := events[0]
	if e.Server != "alpha" || e.ToolExposed != "alpha___echo" || e.ToolOriginal != "echo" {
		t.Errorf("event routing = %+v, want alpha/alpha___echo/echo", e)
	}
	if e.SessionID != "sess-123" || e.ClientName != "claude" || e.ClientVersion != "2.1" {
		t.Errorf("event attribution = %+v, want sess-123/claude/2.1", e)
	}
	if e.Method != mcp.MethodToolsCall || e.ErrorCode != nil {
		t.Errorf("event method/error = %q/%v, want tools/call/nil", e.Method, e.ErrorCode)
	}
}

// TestAuditScopeCallsSkipsLists confirms the default "calls" scope records only
// routed calls, not synthesized initialize/tools/list.
func TestAuditScopeCallsSkipsLists(t *testing.T) {
	agg, _, _ := twoServerAgg(t, Profile{})
	sink := &fakeSink{}
	agg.SetAudit(sink, "s", false)
	initAgg(t, agg)

	agg.Handle(context.Background(), req(mcp.MethodToolsList, nil))
	agg.Handle(context.Background(), req(mcp.MethodPing, nil))

	if got := len(sink.all()); got != 0 {
		t.Fatalf("scope=calls recorded %d non-call events, want 0", got)
	}
}

// TestAuditScopeAllRecordsLists confirms the "all" scope also records the
// synthesized methods (with no resolved server/tool).
func TestAuditScopeAllRecordsLists(t *testing.T) {
	agg, _, _ := twoServerAgg(t, Profile{})
	sink := &fakeSink{}
	agg.SetAudit(sink, "s", true)
	initAgg(t, agg) // initialize itself is recorded under scope=all

	agg.Handle(context.Background(), req(mcp.MethodToolsList, nil))

	events := sink.all()
	if len(events) != 2 {
		t.Fatalf("scope=all recorded %d events, want 2 (initialize + tools/list)", len(events))
	}
	for _, e := range events {
		if e.Server != "" || e.ToolExposed != "" {
			t.Errorf("synthesized event should have no server/tool: %+v", e)
		}
	}
}

// TestAuditRecordsErrorCode asserts a call whose upstream returns a JSON-RPC
// error captures that code on the event.
func TestAuditRecordsErrorCode(t *testing.T) {
	failing := &upstreamtest.Fake{
		Respond: func(method string, _ json.RawMessage) (json.RawMessage, *mcp.Error, error) {
			switch method {
			case mcp.MethodInitialize:
				return json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"x","version":"1"}}`), nil, nil
			case mcp.MethodToolsCall:
				return nil, &mcp.Error{Code: -32000, Message: "boom", Data: json.RawMessage(`{"why":"nope"}`)}, nil
			default:
				return json.RawMessage(`{}`), nil, nil
			}
		},
	}
	mgr := upstream.NewManager(discardLogger())
	if err := mgr.Add("alpha", failing); err != nil {
		t.Fatal(err)
	}
	agg := New(mgr, Profile{}, "test", discardLogger())
	sink := &fakeSink{}
	agg.SetAudit(sink, "s", false)
	initAgg(t, agg)

	resp := agg.Handle(context.Background(), req(mcp.MethodToolsCall,
		map[string]any{"name": "alpha___echo"}))
	if resp.Error == nil {
		t.Fatal("expected upstream rpc error to propagate")
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	e := events[0]
	if e.ErrorCode == nil || *e.ErrorCode != -32000 || e.ErrorMessage != "boom" {
		t.Errorf("error not captured: code=%v msg=%q", e.ErrorCode, e.ErrorMessage)
	}
	if string(e.ResponseResult) != `{"why":"nope"}` {
		t.Errorf("error data not routed to response body: %s", e.ResponseResult)
	}
}
