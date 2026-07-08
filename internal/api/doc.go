// Package api implements the HTTP management plane, distinct from the
// client-facing MCP endpoints. Currently it serves the read-only audit
// dashboard and a small JSON/health surface over the audit store; server CRUD
// and HTMX handlers arrive with the SQLite registry in a later phase.
package api
