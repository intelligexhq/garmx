package ui

import (
	"embed"
	"fmt"
	"html/template"
	"io"

	"github.com/intelligexhq/garmx/internal/audit"
)

//go:embed templates/*.html
var templatesFS embed.FS

// dashboardTmpl is parsed once at package load; the template set is small and
// embedded, so a parse failure is a programming error surfaced eagerly.
var dashboardTmpl = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

// DashboardData is the view model for the audit dashboard: the aggregate stats,
// the most recent transactions, and presentation hints.
type DashboardData struct {
	Stats          audit.Stats
	Recent         []audit.LogEntry
	RefreshSeconds int
	// ErrorPct is Stats.ErrorRate as a whole percentage, precomputed so the
	// template needs no arithmetic.
	ErrorPct float64
}

// RenderDashboard writes the dashboard page for data to w.
func RenderDashboard(w io.Writer, data DashboardData) error {
	data.ErrorPct = data.Stats.ErrorRate * 100
	if err := dashboardTmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		return fmt.Errorf("render dashboard: %w", err)
	}
	return nil
}

// DetailData is the view model for a single transaction's detail page: the row
// plus its request/response bodies pretty-printed by the caller (the store keeps
// them compact).
type DetailData struct {
	Entry          audit.LogEntry
	RequestPretty  string
	ResponsePretty string
}

// RenderDetail writes the per-transaction detail page for data to w.
func RenderDetail(w io.Writer, data DetailData) error {
	if err := dashboardTmpl.ExecuteTemplate(w, "detail.html", data); err != nil {
		return fmt.Errorf("render detail: %w", err)
	}
	return nil
}
