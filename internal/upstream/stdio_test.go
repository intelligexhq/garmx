package upstream

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// TestMain re-execs this test binary as a minimal stdio MCP probe when
// GARMX_TEST_PROBE=1, so the transport tests can drive a real subprocess
// without shipping a separate helper binary.
func TestMain(m *testing.M) {
	if os.Getenv("GARMX_TEST_PROBE") == "1" {
		runProbe()
		return
	}
	os.Exit(m.Run())
}

// runProbe is a tiny stdio MCP server: it answers initialize, tools/list, ping,
// and echoes each tools/call's arguments.text back in the result so a test can
// verify per-id correlation.
func runProbe() {
	r := bufio.NewReaderSize(os.Stdin, 64*1024)
	for {
		line, err := mcp.ReadMessage(r)
		if len(line) > 0 {
			if out := probeReply(line); out != nil {
				_ = mcp.WriteMessage(os.Stdout, out)
			}
		}
		if err != nil {
			return
		}
	}
}

// probeReply builds the probe's response for one inbound line, or nil for a
// notification (no reply expected).
func probeReply(line []byte) *mcp.Response {
	env, err := mcp.Parse(line)
	if err != nil || env.IsNotification() {
		return nil
	}
	switch env.Method {
	case mcp.MethodInitialize:
		return mcp.NewResponse(env.ID, json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"probe","version":"9"}}`))
	case mcp.MethodToolsList:
		return mcp.NewResponse(env.ID, json.RawMessage(`{"tools":[{"name":"echo"}]}`))
	case mcp.MethodToolsCall:
		var p struct {
			Arguments struct {
				Text string `json:"text"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(env.Params, &p)
		result, _ := json.Marshal(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": p.Arguments.Text}},
		})
		return mcp.NewResponse(env.ID, result)
	case mcp.MethodPing:
		return mcp.NewResponse(env.ID, json.RawMessage(`{}`))
	default:
		return mcp.NewErrorResponse(env.ID, mcp.CodeMethodNotFound, "method not found")
	}
}

// newProbeTransport starts a StdioTransport backed by the re-exec probe.
func newProbeTransport(t *testing.T) *StdioTransport {
	t.Helper()
	tr := NewStdioTransport(StdioConfig{
		Name:    "probe",
		Command: os.Args[0],
		Env:     []string{"GARMX_TEST_PROBE=1"},
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("start probe: %v", err)
	}
	t.Cleanup(func() { _ = tr.Stop(context.Background()) })
	return tr
}

// callText issues a tools/call echoing text and returns the echoed value.
func callText(t *testing.T, tr *StdioTransport, text string) string {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": "echo", "arguments": map[string]string{"text": text}})
	result, rpcErr, err := tr.Send(context.Background(), mcp.MethodToolsCall, params)
	if err != nil || rpcErr != nil {
		t.Fatalf("tools/call(%q): err=%v rpcErr=%v", text, err, rpcErr)
	}
	var out struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(out.Content) == 0 {
		t.Fatalf("empty content for %q", text)
	}
	return out.Content[0].Text
}

// TestStdioTransportRoundTrip exercises a real subprocess handshake and a list
// call, then a clean stop.
func TestStdioTransportRoundTrip(t *testing.T) {
	tr := newProbeTransport(t)
	result, rpcErr, err := tr.Send(context.Background(), mcp.MethodInitialize, json.RawMessage(`{}`))
	if err != nil || rpcErr != nil {
		t.Fatalf("initialize: err=%v rpcErr=%v", err, rpcErr)
	}
	var res mcp.InitializeResult
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.ServerInfo.Name != "probe" {
		t.Fatalf("serverInfo.name = %q, want probe", res.ServerInfo.Name)
	}
	if got := callText(t, tr, "solo"); got != "solo" {
		t.Fatalf("echo = %q, want solo", got)
	}
}

// TestStdioTransportConcurrentCorrelation is the real end-to-end demux check:
// many concurrent tools/call requests over one subprocess must each receive the
// response bearing their own id. Under -race this also catches write/read
// interleaving bugs.
func TestStdioTransportConcurrentCorrelation(t *testing.T) {
	tr := newProbeTransport(t)
	const n = 100
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := "val-" + strconv.Itoa(i)
			if got := callTextNoFatal(tr, want); got != want {
				errs <- got + " != " + want
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("misdelivered response: %s", e)
	}
}

// callTextNoFatal is callText without t.Fatalf (safe to call off the test
// goroutine); it returns the echoed value or an empty string on error.
func callTextNoFatal(tr *StdioTransport, text string) string {
	params, _ := json.Marshal(map[string]any{"name": "echo", "arguments": map[string]string{"text": text}})
	result, rpcErr, err := tr.Send(context.Background(), mcp.MethodToolsCall, params)
	if err != nil || rpcErr != nil {
		return ""
	}
	var out struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal(result, &out); err != nil || len(out.Content) == 0 {
		return ""
	}
	return out.Content[0].Text
}

// TestStdioTransportStop asserts Stop terminates the child and marks it offline,
// and that a subsequent Send fails rather than hanging.
func TestStdioTransportStop(t *testing.T) {
	tr := newProbeTransport(t)
	if err := tr.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if tr.Status() != StatusOffline {
		t.Fatalf("status = %v, want offline", tr.Status())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := tr.Send(ctx, mcp.MethodPing, nil); err == nil {
		t.Fatal("Send after Stop should fail")
	}
}
