package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the on-disk declaration of upstream servers and profiles used to
// seed the daemon. In this phase it is read directly at startup (SQLite as the
// authoritative store arrives later); it is plain JSON for now (JSONC comment
// stripping is a later concern).
type Config struct {
	Servers  []Server  `json:"servers"`
	Profiles []Profile `json:"profiles,omitempty"`
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
	return nil
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
