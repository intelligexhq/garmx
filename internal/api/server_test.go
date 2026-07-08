package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intelligexhq/garmx/internal/audit"
)

// seededHandler builds a handler over a reader store seeded with a couple of
// rows on a temp DB.
func seededHandler(t *testing.T) http.Handler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.db")
	ws, err := audit.OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	code := -32000
	if err := ws.InsertBatch(context.Background(), []audit.LogEntry{
		{SessionID: "s", ClientName: "claude", ServerName: "alpha", Method: "tools/call", ToolExposed: "alpha___echo", LatencyMS: 5, RequestPayload: `{"arguments":{"msg":"hi"}}`},
		{SessionID: "s", ClientName: "claude", ServerName: "beta", Method: "tools/call", ToolExposed: "beta___fail", LatencyMS: 12, ErrorCode: &code, ErrorMessage: "kaboom"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()

	rs, err := audit.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rs.Close() })
	return NewServer(rs, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
}

// TestDashboardRenders asserts the page renders with the seeded tool names and
// a non-zero call count.
func TestDashboardRenders(t *testing.T) {
	h := seededHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"alpha___echo", "beta___fail", "Total calls", "GarmX"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

// TestLogsJSON asserts the JSON endpoint returns the seeded rows newest-first.
func TestLogsJSON(t *testing.T) {
	h := seededHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/logs?limit=10", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var rows []audit.LogEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].ServerName != "beta" || rows[0].ErrorCode == nil {
		t.Errorf("row[0] = %+v, want beta with error code", rows[0])
	}
}

// TestLogDetail asserts the per-row detail page renders the pretty-printed
// request body and the captured error message, and 404s for a missing id.
func TestLogDetail(t *testing.T) {
	h := seededHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs/1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// The request body is pretty-printed inside a <pre>; html/template escapes the
	// quotes, so assert on the (indented, multi-line) content rather than raw JSON.
	if body := rec.Body.String(); !strings.Contains(body, "alpha___echo") ||
		!strings.Contains(body, "<pre>") || !strings.Contains(body, "msg") || !strings.Contains(body, "hi") {
		t.Errorf("detail page missing tool or pretty-printed request:\n%s", body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs/2", nil))
	if !strings.Contains(rec.Body.String(), "kaboom") {
		t.Errorf("detail page missing error message")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs/999", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing id status = %d, want 404", rec.Code)
	}
}

// TestHealth asserts the health endpoint responds ok.
func TestHealth(t *testing.T) {
	h := seededHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("health = %d %q", rec.Code, rec.Body.String())
	}
}

// TestUnknownPath404 asserts non-root paths are not swallowed by the dashboard.
func TestUnknownPath404(t *testing.T) {
	h := seededHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
