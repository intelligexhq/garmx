package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "garmx.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadValid parses a well-formed config with servers and a profile.
func TestLoadValid(t *testing.T) {
	path := writeConfig(t, `{
      "servers": [
        {"name": "probe", "command": "/bin/probe", "args": ["--x"], "env": {"K": "V"}}
      ],
      "profiles": [
        {"name": "coding", "servers": ["probe"], "toolDeny": ["*___delete_*"]}
      ]
    }`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].Name != "probe" {
		t.Fatalf("servers = %+v", cfg.Servers)
	}
	if env := cfg.Servers[0].EnvSlice(); len(env) != 1 || env[0] != "K=V" {
		t.Fatalf("EnvSlice = %v, want [K=V]", env)
	}
	p, ok := cfg.FindProfile("coding")
	if !ok || len(p.ToolDeny) != 1 {
		t.Fatalf("profile lookup failed: %+v ok=%v", p, ok)
	}
	if _, ok := cfg.FindProfile("missing"); ok {
		t.Fatal("FindProfile returned a missing profile")
	}
}

// TestLoadRejectsInvalid pins the validation failures that must surface at
// startup rather than as a silently broken aggregate.
func TestLoadRejectsInvalid(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"no command", `{"servers":[{"name":"x"}]}`},
		{"missing name", `{"servers":[{"command":"/bin/x"}]}`},
		{"duplicate name", `{"servers":[{"name":"x","command":"/a"},{"name":"x","command":"/b"}]}`},
		{"bad transport", `{"servers":[{"name":"x","command":"/a","transport":"grpc"}]}`},
		{"bad json", `{"servers":[`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tt.content)); err == nil {
				t.Fatalf("Load(%s) = nil error, want failure", tt.name)
			}
		})
	}
}
