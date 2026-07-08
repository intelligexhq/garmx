package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/intelligexhq/garmx/internal/audit"
	"github.com/intelligexhq/garmx/internal/ui"
)

// defaults for the read-only audit dashboard.
const (
	defaultRecentLimit    = 100
	defaultRefreshSeconds = 2
	maxLogsLimit          = 1000
)

// Server serves the read-only management plane over an audit store opened for
// reading. It never mutates the store; it only queries and renders.
type Server struct {
	store          *audit.Store
	logger         *slog.Logger
	recentLimit    int
	refreshSeconds int
}

// NewServer builds a management Server over a read-only audit store.
func NewServer(store *audit.Store, logger *slog.Logger) *Server {
	return &Server{
		store:          store,
		logger:         logger.With("component", "api"),
		recentLimit:    defaultRecentLimit,
		refreshSeconds: defaultRefreshSeconds,
	}
}

// Handler returns the HTTP routes: the dashboard page, a JSON logs endpoint, and
// a health check. Unknown paths 404 (the root pattern is an exact match).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /logs/{id}", s.handleLogDetail)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

// handleDashboard renders the audit dashboard: stat tiles plus the most recent
// calls. Query failures render a 500 rather than a partial page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stats, err := s.store.Stats(ctx)
	if err != nil {
		s.serverError(w, "stats", err)
		return
	}
	recent, err := s.store.Recent(ctx, s.recentLimit, 0)
	if err != nil {
		s.serverError(w, "recent", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.RenderDashboard(w, ui.DashboardData{
		Stats:          stats,
		Recent:         recent,
		RefreshSeconds: s.refreshSeconds,
	}); err != nil {
		// Headers/body may be partly written; just log.
		s.logger.Error("render dashboard", "err", err)
	}
}

// handleLogDetail renders a single transaction, pretty-printing its request and
// response bodies. An unparseable or unknown id is a 404.
func (s *Server) handleLogDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	entry, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		s.serverError(w, "detail", err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.RenderDetail(w, ui.DetailData{
		Entry:          entry,
		RequestPretty:  prettyJSON(entry.RequestPayload),
		ResponsePretty: prettyJSON(entry.ResponsePayload),
	}); err != nil {
		s.logger.Error("render detail", "err", err)
	}
}

// prettyJSON indents a compact JSON string for display; non-JSON or empty input
// is returned unchanged so it is never lost.
func prettyJSON(s string) string {
	if s == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

// handleLogs returns recent audit rows as JSON, honoring limit/offset query
// params (limit clamped to a sane maximum).
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit := clampAtoi(r.URL.Query().Get("limit"), s.recentLimit, 1, maxLogsLimit)
	offset := clampAtoi(r.URL.Query().Get("offset"), 0, 0, 1<<31-1)
	rows, err := s.store.Recent(r.Context(), limit, offset)
	if err != nil {
		s.serverError(w, "logs", err)
		return
	}
	if rows == nil {
		rows = []audit.LogEntry{}
	}
	s.writeJSON(w, rows)
}

// handleHealth reports the reader is up.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, map[string]string{"status": "ok"})
}

// writeJSON encodes v as the response body; an encode failure is logged (the
// status line is already committed).
func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("encode json response", "err", err)
	}
}

// serverError logs the cause and returns a generic 500 (no internal detail
// leaks to the browser).
func (s *Server) serverError(w http.ResponseWriter, what string, err error) {
	s.logger.Error("query failed", "what", what, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// clampAtoi parses s as an int, falling back to def, and clamps it to [lo, hi].
func clampAtoi(s string, def, lo, hi int) int {
	v := def
	if s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			v = n
		}
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
