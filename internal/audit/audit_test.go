package audit

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intelligexhq/garmx/internal/aggregator"
)

// silentLogger returns a logger that discards output, for tests.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// intp is a small helper for optional error codes in tests.
func intp(v int) *int { return &v }

// TestStoreInsertAndRecent checks a batch round-trips newest-first with fields
// and a nil error code preserved.
func TestStoreInsertAndRecent(t *testing.T) {
	s, err := OpenWriter(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	entries := []LogEntry{
		{SessionID: "sess", ServerName: "alpha", Method: "tools/call", ToolExposed: "alpha___echo", ToolOriginal: "echo", LatencyMS: 5},
		{SessionID: "sess", ServerName: "beta", Method: "tools/call", ToolExposed: "beta___fail", ToolOriginal: "fail", LatencyMS: 9, ErrorCode: intp(-32000)},
	}
	if err := s.InsertBatch(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	got, err := s.Recent(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	// Newest first: beta was inserted last.
	if got[0].ServerName != "beta" || got[0].ErrorCode == nil || *got[0].ErrorCode != -32000 {
		t.Errorf("row[0] = %+v, want beta with error -32000", got[0])
	}
	if got[1].ServerName != "alpha" || got[1].ErrorCode != nil {
		t.Errorf("row[1] = %+v, want alpha with nil error", got[1])
	}
}

// TestStoreGet checks single-row lookup, error_message round-trip, and the
// not-found path.
func TestStoreGet(t *testing.T) {
	s, err := OpenWriter(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	code := -32000
	if err := s.InsertBatch(context.Background(), []LogEntry{
		{SessionID: "s", ServerName: "alpha", Method: "tools/call", ToolExposed: "alpha___boom", LatencyMS: 1, ErrorCode: &code, ErrorMessage: "kaboom"},
	}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.Get(context.Background(), 1)
	if err != nil || !ok {
		t.Fatalf("Get(1) = ok %v err %v", ok, err)
	}
	if got.ErrorMessage != "kaboom" || got.ErrorCode == nil || *got.ErrorCode != -32000 {
		t.Errorf("row = %+v, want error kaboom/-32000", got)
	}

	if _, ok, err := s.Get(context.Background(), 999); err != nil || ok {
		t.Errorf("Get(999) = ok %v err %v, want false/nil", ok, err)
	}
}

// TestWriterEndToEndAndCap drives events through the async writer and asserts
// redaction and size-cap truncation on the stored rows, reading them back
// through a read-only Store on the same file.
func TestWriterEndToEndAndCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	s, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, Options{
		Payload:         payloadRequestResponse,
		MaxPayloadBytes: 100,
		FlushInterval:   10 * time.Millisecond,
	}, silentLogger())

	w.Record(aggregator.Event{
		SessionID:      "s1",
		Server:         "alpha",
		Method:         "tools/call",
		ToolExposed:    "alpha___echo",
		ToolOriginal:   "echo",
		RequestParams:  json.RawMessage(`{"arguments":{"token":"sk-secret","msg":"hi"}}`),
		ResponseResult: json.RawMessage(`{"content":"` + strings.Repeat("x", 200) + `"}`),
		LatencyMS:      3,
	})
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	rs, err := OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rs.Close() })
	got, err := rs.Recent(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	row := got[0]
	if strings.Contains(row.RequestPayload, "sk-secret") {
		t.Errorf("secret leaked into request payload: %q", row.RequestPayload)
	}
	if !strings.Contains(row.RequestPayload, "hi") {
		t.Errorf("non-secret arg dropped: %q", row.RequestPayload)
	}
	if !row.Truncated || !strings.Contains(row.ResponsePayload, "_truncated") {
		t.Errorf("large response not truncated: %+v", row)
	}
	if row.PayloadBytes == 0 {
		t.Errorf("payload_bytes not recorded")
	}
}

// TestWriterRecordNonBlocking confirms Record never blocks even when the buffer
// is saturated: a tiny buffer overflowed by many events returns promptly.
func TestWriterRecordNonBlocking(t *testing.T) {
	s, err := OpenWriter(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, Options{
		Payload:       payloadMetadata,
		BufferSize:    1,
		FlushInterval: time.Hour, // don't drain during the test
	}, silentLogger())
	t.Cleanup(func() { _ = w.Close(context.Background()) })

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			w.Record(aggregator.Event{SessionID: "s", Method: "tools/call"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record blocked under a full buffer")
	}
	if w.dropped.Load() == 0 {
		t.Errorf("expected some events dropped under back-pressure")
	}
}

// TestStats verifies counts, error rate, and nearest-rank percentiles over a
// seeded set, plus the per-server breakdown.
func TestStats(t *testing.T) {
	s, err := OpenWriter(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var entries []LogEntry
	for _, lat := range []int64{10, 20, 30, 40} {
		entries = append(entries, LogEntry{SessionID: "s", ServerName: "alpha", Method: "tools/call", LatencyMS: lat})
	}
	entries = append(entries, LogEntry{SessionID: "s", ServerName: "beta", Method: "tools/call", LatencyMS: 100, ErrorCode: intp(-32000)})
	if err := s.InsertBatch(context.Background(), entries); err != nil {
		t.Fatal(err)
	}

	st, err := s.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.TotalCalls != 5 || st.Errors != 1 {
		t.Errorf("counts = %d calls / %d errors, want 5/1", st.TotalCalls, st.Errors)
	}
	if st.ErrorRate < 0.19 || st.ErrorRate > 0.21 {
		t.Errorf("error rate = %v, want ~0.2", st.ErrorRate)
	}
	// nearest-rank over [10,20,30,40,100]: p50 at index floor(.5*4)=2 → 30;
	// p95 at index floor(.95*4)=3 → 40.
	if st.P50ms != 30 {
		t.Errorf("p50 = %d, want 30", st.P50ms)
	}
	if st.P95ms != 40 {
		t.Errorf("p95 = %d, want 40", st.P95ms)
	}
	if len(st.PerServer) != 2 || st.PerServer[0].Server != "alpha" || st.PerServer[0].Calls != 4 {
		t.Errorf("per-server = %+v, want alpha with 4 calls first", st.PerServer)
	}
}
