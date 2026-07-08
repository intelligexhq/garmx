// Package audit records MCP transactions to a shared SQLite database on the
// write path: redaction of secret-valued fields, per-payload size capping, and
// an asynchronous, batched, best-effort writer that never blocks or crashes the
// gateway it observes. A read-only Store serves the minimal web UI. Live
// WebSocket streaming and OTLP export are later phases.
package audit
