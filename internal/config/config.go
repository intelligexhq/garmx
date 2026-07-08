package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk declaration of upstream servers and profiles used to
// seed the daemon. In this phase it is read directly at startup (SQLite as the
// authoritative store arrives later); it is plain JSON for now (JSONC comment
// stripping is a later concern).
type Config struct {
	Servers  []Server     `json:"servers"`
	Profiles []Profile    `json:"profiles,omitempty"`
	Audit    *AuditConfig `json:"audit,omitempty"`
}

// Audit payload capture modes: how much of each transaction is persisted.
const (
	// PayloadRequestResponse stores both the request arguments and the response
	// body (each redacted and size-capped). Richest audit trail.
	PayloadRequestResponse = "request-response"
	// PayloadRequest stores only the request arguments; the response is reduced
	// to its error code and latency.
	PayloadRequest = "request"
	// PayloadMetadata stores no payload bodies at all — only server, method,
	// tool names, latency, and error.
	PayloadMetadata = "metadata"
)

// Audit emission scopes: which transactions are recorded.
const (
	// ScopeCalls audits only the arg-bearing, routed transactions
	// (tools/call, prompts/get, resources/read).
	ScopeCalls = "calls"
	// ScopeAll audits every client request, including synthesized
	// initialize/*list responses.
	ScopeAll = "all"
)

// Default audit settings applied when neither the config file nor a flag
// specifies a value.
const (
	defaultMaxPayloadBytes = 16 * 1024
)

// AuditConfig is the on-disk `audit` block. Every field is optional: a nil
// Enabled, an empty string, or a zero MaxPayloadBytes means "unset", and
// ResolveAudit fills it from the built-in default (which a CLI flag/env can then
// override). Pointer/empty-value semantics are what let "omitted" differ from an
// explicit value.
type AuditConfig struct {
	Enabled         *bool    `json:"enabled,omitempty"`
	DBPath          string   `json:"dbPath,omitempty"`
	Payload         string   `json:"payload,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	MaxPayloadBytes int      `json:"maxPayloadBytes,omitempty"`
	RedactKeys      []string `json:"redactKeys,omitempty"`
}

// ResolvedAudit is the fully-defaulted audit configuration the rest of the
// program consumes: no optionals, no "unset" states.
type ResolvedAudit struct {
	Enabled         bool
	DBPath          string
	Payload         string
	Scope           string
	MaxPayloadBytes int
	// RedactKeys are extra secret-key names, additive to the redactor's built-in
	// set.
	RedactKeys []string
}

// AuditOverride carries per-invocation overrides sourced from CLI flags and env
// vars. An empty string means "not overridden"; Disable forces auditing off
// regardless of the file.
type AuditOverride struct {
	DBPath  string
	Payload string
	Scope   string
	Disable bool
}

// Server declares one upstream MCP server. Only stdio is supported in this
// phase; Transport defaults to "stdio".
type Server struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// Profile declares a named subset of the aggregate: a server subset plus tool
// allow/deny globs on exposed names.
type Profile struct {
	Name      string   `json:"name"`
	Servers   []string `json:"servers,omitempty"`
	ToolAllow []string `json:"toolAllow,omitempty"`
	ToolDeny  []string `json:"toolDeny,omitempty"`
}

// Load reads and validates a config file. It fails on unreadable files, invalid
// JSON, and structural problems (missing/duplicate server names, a stdio server
// without a command) so misconfiguration surfaces at startup rather than as a
// silently empty aggregate.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %q: %w", path, err)
	}
	return &cfg, nil
}

// validate enforces the structural invariants the daemon relies on.
func (c *Config) validate() error {
	seen := map[string]struct{}{}
	for i, s := range c.Servers {
		if s.Name == "" {
			return fmt.Errorf("servers[%d]: missing name", i)
		}
		if _, dup := seen[s.Name]; dup {
			return fmt.Errorf("duplicate server name %q", s.Name)
		}
		seen[s.Name] = struct{}{}
		transport := s.Transport
		if transport == "" {
			transport = "stdio"
		}
		if transport == "stdio" && s.Command == "" {
			return fmt.Errorf("server %q: stdio transport requires a command", s.Name)
		}
		if transport != "stdio" {
			return fmt.Errorf("server %q: unsupported transport %q (only stdio in this phase)", s.Name, transport)
		}
	}
	profiles := map[string]struct{}{}
	for i, p := range c.Profiles {
		if p.Name == "" {
			return fmt.Errorf("profiles[%d]: missing name", i)
		}
		if _, dup := profiles[p.Name]; dup {
			return fmt.Errorf("duplicate profile name %q", p.Name)
		}
		profiles[p.Name] = struct{}{}
	}
	if c.Audit != nil {
		if err := validatePayload(c.Audit.Payload); err != nil {
			return err
		}
		if err := validateScope(c.Audit.Scope); err != nil {
			return err
		}
	}
	return nil
}

// validatePayload accepts the empty string (unset) or a known payload mode.
func validatePayload(p string) error {
	switch p {
	case "", PayloadRequestResponse, PayloadRequest, PayloadMetadata:
		return nil
	default:
		return fmt.Errorf("audit.payload %q: want one of %q, %q, %q", p,
			PayloadRequestResponse, PayloadRequest, PayloadMetadata)
	}
}

// validateScope accepts the empty string (unset) or a known scope.
func validateScope(s string) error {
	switch s {
	case "", ScopeCalls, ScopeAll:
		return nil
	default:
		return fmt.Errorf("audit.scope %q: want %q or %q", s, ScopeCalls, ScopeAll)
	}
}

// ResolveAudit layers the built-in defaults, the config file's audit block, and
// per-invocation overrides (flags/env, which win) into one fully-defaulted
// value. It validates the final payload/scope and expands a leading ~ in the DB
// path so writer and reader resolve the same file.
func ResolveAudit(file *AuditConfig, ov AuditOverride) (ResolvedAudit, error) {
	r := ResolvedAudit{
		Enabled:         true,
		DBPath:          DefaultAuditDBPath(),
		Payload:         PayloadRequestResponse,
		Scope:           ScopeCalls,
		MaxPayloadBytes: defaultMaxPayloadBytes,
	}
	if file != nil {
		if file.Enabled != nil {
			r.Enabled = *file.Enabled
		}
		if file.DBPath != "" {
			r.DBPath = file.DBPath
		}
		if file.Payload != "" {
			r.Payload = file.Payload
		}
		if file.Scope != "" {
			r.Scope = file.Scope
		}
		if file.MaxPayloadBytes > 0 {
			r.MaxPayloadBytes = file.MaxPayloadBytes
		}
		r.RedactKeys = file.RedactKeys
	}
	if ov.DBPath != "" {
		r.DBPath = ov.DBPath
	}
	if ov.Payload != "" {
		r.Payload = ov.Payload
	}
	if ov.Scope != "" {
		r.Scope = ov.Scope
	}
	if ov.Disable {
		r.Enabled = false
	}
	if err := validatePayload(r.Payload); err != nil {
		return ResolvedAudit{}, err
	}
	if err := validateScope(r.Scope); err != nil {
		return ResolvedAudit{}, err
	}
	r.DBPath = expandHome(r.DBPath)
	return r, nil
}

// DefaultAuditDBPath is the built-in location of the shared audit database:
// $XDG_DATA_HOME/garmx/audit.db, else ~/.local/share/garmx/audit.db. It falls
// back to the OS temp dir only if the home directory cannot be determined, so a
// path is always returned.
func DefaultAuditDBPath() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "garmx", "audit.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "garmx", "audit.db")
	}
	return filepath.Join(home, ".local", "share", "garmx", "audit.db")
}

// expandHome replaces a leading ~ with the user's home directory; other paths
// pass through unchanged.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// EnvSlice converts a Server's env map to the "KEY=VALUE" slice exec expects.
func (s Server) EnvSlice() []string {
	out := make([]string, 0, len(s.Env))
	for k, v := range s.Env {
		out = append(out, k+"="+v)
	}
	return out
}

// FindProfile returns the profile with the given name, or false if absent.
func (c *Config) FindProfile(name string) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}
