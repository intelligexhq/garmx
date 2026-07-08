package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite".
)

// schemaDDL creates the audit table and its indexes if absent. This is the
// Phase-3 shape of audit_logs from docs/architecture.md with one deliberate
// deviation: server_name is stored as free text with no foreign key, because the
// servers registry table does not exist yet (it arrives with the SQLite catalog
// in a later phase). tool_exposed/tool_original are added here since a single
// method column cannot capture both the prefixed name the client used and the
// original name forwarded upstream. Reconcile with the registry when it lands.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS audit_logs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id       TEXT NOT NULL,
    client_name      TEXT,
    client_version   TEXT,
    server_name      TEXT,
    method           TEXT NOT NULL,
    tool_exposed     TEXT,
    tool_original    TEXT,
    rpc_id           TEXT,
    request_payload  TEXT,
    response_payload TEXT,
    payload_bytes    INTEGER,
    truncated        INTEGER NOT NULL DEFAULT 0,
    latency_ms       INTEGER,
    error_code       INTEGER,
    created_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_server  ON audit_logs(server_name);
CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_logs(session_id);
`

// migrations are additive ALTER statements applied after schemaDDL. Each is
// idempotent: re-running against a DB that already has the column errors with
// "duplicate column", which is ignored. This lets an existing audit DB gain new
// columns without a full rebuild.
var migrations = []string{
	`ALTER TABLE audit_logs ADD COLUMN error_message TEXT`,
}

// logColumns is the shared SELECT column list, in the order scanLog expects.
const logColumns = `id, session_id, client_name, client_version, server_name, method,
       tool_exposed, tool_original, rpc_id, request_payload, response_payload,
       payload_bytes, truncated, latency_ms, error_code, error_message, created_at`

// Store owns a SQLite handle to the shared audit database. A writer store (from
// OpenWriter) pins a single connection so this process's inserts serialize;
// cross-process contention between multiple `serve --stdio` writers is handled
// by WAL + busy_timeout. A reader store (from OpenReader) is query-only and may
// use a small connection pool.
type Store struct {
	db *sql.DB
}

// LogEntry is one audit row. The writer fills the payload fields (already
// redacted and size-capped); queries return the same shape for the UI. ErrorCode
// is nil for a successful transaction.
type LogEntry struct {
	ID              int64
	SessionID       string
	ClientName      string
	ClientVersion   string
	ServerName      string
	Method          string
	ToolExposed     string
	ToolOriginal    string
	RPCID           string
	RequestPayload  string
	ResponsePayload string
	PayloadBytes    int64
	Truncated       bool
	LatencyMS       int64
	ErrorCode       *int
	ErrorMessage    string
	CreatedAt       string
}

// dsn builds a SQLite URI DSN for path with the given pragma settings. Using a
// file: URI lets the mode/pragma query params through modernc's parser.
func dsn(path string, pragmas map[string]string, mode string) string {
	u := url.Values{}
	for k, v := range pragmas {
		u.Add("_pragma", fmt.Sprintf("%s(%s)", k, v))
	}
	if mode != "" {
		u.Set("mode", mode)
	}
	return "file:" + path + "?" + u.Encode()
}

// OpenWriter opens (creating the parent directory and file if needed) the audit
// database for appending. It enables WAL so concurrent readers never block on
// the writer, sets a busy_timeout so lock contention between writer processes
// waits rather than fails, pins MaxOpenConns to 1 so this process's writes
// serialize on one connection, and ensures the schema exists.
func OpenWriter(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create audit dir: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path, map[string]string{
		"journal_mode": "WAL",
		"busy_timeout": "5000",
	}, ""))
	if err != nil {
		return nil, fmt.Errorf("open audit db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init audit schema: %w", err)
	}
	return &Store{db: db}, nil
}

// OpenReader opens the audit database for querying only. It first ensures the
// file and schema exist (so `garmx ui` works before any calls have been logged),
// then opens a query-only handle: query_only rejects any accidental write at the
// SQLite level, so the UI process cannot mutate the audit trail.
func OpenReader(path string) (*Store, error) {
	if err := ensureSchema(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(path, map[string]string{
		"busy_timeout": "5000",
		"query_only":   "true",
	}, "ro"))
	if err != nil {
		return nil, fmt.Errorf("open audit db (ro): %w", err)
	}
	return &Store{db: db}, nil
}

// ensureSchema creates the database file and schema if missing, using a
// short-lived writable handle. It is idempotent (CREATE ... IF NOT EXISTS).
func ensureSchema(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create audit dir: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path, map[string]string{
		"journal_mode": "WAL",
		"busy_timeout": "5000",
	}, ""))
	if err != nil {
		return fmt.Errorf("open audit db: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		return fmt.Errorf("init audit schema: %w", err)
	}
	return nil
}

// migrate creates the schema and applies additive column migrations, ignoring
// the "duplicate column" error that a migration raises once it has been applied.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaDDL); err != nil {
		return err
	}
	for _, alter := range migrations {
		if _, err := db.Exec(alter); err != nil && !isDupColumn(err) {
			return err
		}
	}
	return nil
}

// isDupColumn reports whether err is SQLite's "duplicate column name" error,
// which means the migration is already applied.
func isDupColumn(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column")
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// InsertBatch appends all entries in one transaction. A single failed statement
// rolls back the whole batch, which the caller treats as "drop this batch" —
// audit loss is preferable to crashing the gateway.
func (s *Store) InsertBatch(ctx context.Context, entries []LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO audit_logs
  (session_id, client_name, client_version, server_name, method,
   tool_exposed, tool_original, rpc_id, request_payload, response_payload,
   payload_bytes, truncated, latency_ms, error_code, error_message)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, e := range entries {
		var errCode any
		if e.ErrorCode != nil {
			errCode = *e.ErrorCode
		}
		if _, err := stmt.ExecContext(ctx,
			e.SessionID, e.ClientName, e.ClientVersion, e.ServerName, e.Method,
			e.ToolExposed, e.ToolOriginal, e.RPCID, e.RequestPayload, e.ResponsePayload,
			e.PayloadBytes, boolToInt(e.Truncated), e.LatencyMS, errCode, e.ErrorMessage,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Recent returns the newest entries first, paginated by limit/offset.
func (s *Store) Recent(ctx context.Context, limit, offset int) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+logColumns+` FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LogEntry
	for rows.Next() {
		e, err := scanLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns a single entry by id. The bool is false (with a nil error) when no
// such row exists, so the caller can render a 404 rather than an error page.
func (s *Store) Get(ctx context.Context, id int64) (LogEntry, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+logColumns+` FROM audit_logs WHERE id = ?`, id)
	e, err := scanLog(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LogEntry{}, false, nil
	}
	if err != nil {
		return LogEntry{}, false, err
	}
	return e, true, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, letting scanLog serve
// single-row and multi-row queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanLog decodes one audit row (columns in logColumns order), mapping SQL NULLs
// to Go zero values and a present error_code to a non-nil pointer.
func scanLog(s rowScanner) (LogEntry, error) {
	var e LogEntry
	var (
		clientName, clientVersion, serverName sql.NullString
		toolExposed, toolOriginal, rpcID      sql.NullString
		requestPayload, responsePayload       sql.NullString
		errorMessage                          sql.NullString
		payloadBytes, latencyMS               sql.NullInt64
		truncated                             int
		errorCode                             sql.NullInt64
	)
	if err := s.Scan(&e.ID, &e.SessionID, &clientName, &clientVersion, &serverName,
		&e.Method, &toolExposed, &toolOriginal, &rpcID, &requestPayload, &responsePayload,
		&payloadBytes, &truncated, &latencyMS, &errorCode, &errorMessage, &e.CreatedAt); err != nil {
		return LogEntry{}, err
	}
	e.ClientName = clientName.String
	e.ClientVersion = clientVersion.String
	e.ServerName = serverName.String
	e.ToolExposed = toolExposed.String
	e.ToolOriginal = toolOriginal.String
	e.RPCID = rpcID.String
	e.RequestPayload = requestPayload.String
	e.ResponsePayload = responsePayload.String
	e.ErrorMessage = errorMessage.String
	e.PayloadBytes = payloadBytes.Int64
	e.LatencyMS = latencyMS.Int64
	e.Truncated = truncated != 0
	if errorCode.Valid {
		c := int(errorCode.Int64)
		e.ErrorCode = &c
	}
	return e, nil
}

// boolToInt maps a bool to SQLite's integer boolean convention.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
