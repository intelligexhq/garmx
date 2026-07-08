package frontend

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/intelligexhq/garmx/internal/aggregator"
	"github.com/intelligexhq/garmx/internal/upstream"
	"github.com/intelligexhq/garmx/internal/upstream/upstreamtest"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// newProbeAgg builds an aggregator over a single "probe" upstream backed by the
// fake, so exposed tools are prefixed as probe___<name>.
func newProbeAgg(t *testing.T, fake *upstreamtest.Fake) *aggregator.Aggregator {
	t.Helper()
	mgr := upstream.NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := mgr.Add("probe", fake); err != nil {
		t.Fatal(err)
	}
	return aggregator.New(mgr, aggregator.Profile{}, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// probeFake is a fake upstream that completes a handshake and answers a single
// tool plus a pass-through tools/call.
func probeFake() *upstreamtest.Fake {
	return &upstreamtest.Fake{
		Respond: func(method string, params json.RawMessage) (json.RawMessage, *mcp.Error, error) {
			switch method {
			case mcp.MethodInitialize:
				return json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"probe","version":"9"}}`), nil, nil
			case mcp.MethodToolsList:
				return json.RawMessage(`{"tools":[{"name":"echo"}]}`), nil, nil
			case mcp.MethodToolsCall:
				return json.RawMessage(`{"content":[{"type":"text","text":"echo ok"}]}`), nil, nil
			default:
				return json.RawMessage(`{}`), nil, nil
			}
		},
	}
}

// writeMsg frames and writes a JSON-RPC message to w.
func writeMsg(t *testing.T, w io.Writer, v any) {
	t.Helper()
	if err := mcp.WriteMessage(w, v); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// readEnv reads and parses one framed message, failing on timeout via the
// caller's deadline goroutine.
func readEnv(t *testing.T, r *bufio.Reader) *mcp.Envelope {
	t.Helper()
	line, err := mcp.ReadMessage(r)
	if err != nil && len(line) == 0 {
		t.Fatalf("read: %v", err)
	}
	env, err := mcp.Parse(line)
	if err != nil {
		t.Fatalf("parse %q: %v", line, err)
	}
	return env
}

// TestStdioRoundTrip drives a full initialize → tools/list → tools/call through
// the client-facing stdio server backed by a fake upstream, asserting the
// prefixed tool surface and a pass-through call result.
func TestStdioRoundTrip(t *testing.T) {
	fake := probeFake()
	agg := newProbeAgg(t, fake)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := NewStdioServer(inR, outW, agg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	out := bufio.NewReader(outR)

	// initialize
	writeMsg(t, inW, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": mcp.MethodInitialize,
		"params": mcp.InitializeParams{ProtocolVersion: "2025-11-25", ClientInfo: mcp.Implementation{Name: "c", Version: "1"}},
	})
	initResp := readEnv(t, out)
	if !initResp.IsResponse() || initResp.Error != nil {
		t.Fatalf("initialize response bad: %+v", initResp)
	}

	// tools/list → expect prefixed name
	writeMsg(t, inW, map[string]any{"jsonrpc": "2.0", "id": 2, "method": mcp.MethodToolsList})
	listResp := readEnv(t, out)
	var list struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &list); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "probe___echo" {
		t.Fatalf("tools = %+v, want [probe___echo]", list.Tools)
	}

	// tools/call → pass-through result
	writeMsg(t, inW, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": mcp.MethodToolsCall,
		"params": map[string]any{"name": "probe___echo", "arguments": map[string]string{"text": "hi"}},
	})
	callResp := readEnv(t, out)
	if callResp.Error != nil {
		t.Fatalf("tools/call error: %v", callResp.Error)
	}
	if string(callResp.ID) != "3" {
		t.Fatalf("tools/call response id = %s, want 3", callResp.ID)
	}

	// Clean EOF ends Serve.
	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil on EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after client EOF")
	}
}

// TestStdioForwardsNotification asserts an upstream notification is pushed to
// the client out-of-band (not in response to a request).
func TestStdioForwardsNotification(t *testing.T) {
	fake := probeFake()
	agg := newProbeAgg(t, fake)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := NewStdioServer(inR, outW, agg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	// Serve registers the client notifier synchronously at start; give the
	// goroutine a moment, then emit.
	out := bufio.NewReader(outR)
	emitted := make(chan struct{})
	go func() {
		// Small settle so SetClientNotifier has run.
		time.Sleep(50 * time.Millisecond)
		fake.Emit(mcp.NewNotification(mcp.NotifyToolsListChanged, nil))
		close(emitted)
	}()

	env := readEnv(t, out)
	<-emitted
	if !env.IsNotification() || env.Method != mcp.NotifyToolsListChanged {
		t.Fatalf("expected forwarded list_changed notification, got %+v", env)
	}
	_ = inW.Close()
}
