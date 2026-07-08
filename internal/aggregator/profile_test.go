package aggregator

import "testing"

// TestProfileAllowsServer pins the server-subset rule: empty means all.
func TestProfileAllowsServer(t *testing.T) {
	all := Profile{}
	if !all.AllowsServer("anything") {
		t.Fatal("empty profile should allow all servers")
	}
	subset := Profile{Servers: []string{"github", "postgres"}}
	if !subset.AllowsServer("github") || subset.AllowsServer("filesystem") {
		t.Fatal("server subset not enforced")
	}
}

// TestProfileAllowsTool pins allow/deny semantics: deny wins, an allow list is a
// whitelist, and globs span the whole exposed name including the delimiter.
func TestProfileAllowsTool(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		exposed string
		want    bool
	}{
		{"empty allows all", Profile{}, "github___create_issue", true},
		{"deny wins over allow", Profile{ToolAllow: []string{"github___*"}, ToolDeny: []string{"*___delete_*"}}, "github___delete_repo", false},
		{"allow whitelist matches", Profile{ToolAllow: []string{"github___*"}}, "github___create_issue", true},
		{"allow whitelist excludes others", Profile{ToolAllow: []string{"github___*"}}, "postgres___query", false},
		{"deny across servers", Profile{ToolDeny: []string{"*___delete_*"}}, "postgres___delete_row", false},
		{"deny miss allows", Profile{ToolDeny: []string{"*___delete_*"}}, "postgres___query", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.profile.AllowsTool(tt.exposed); got != tt.want {
				t.Fatalf("AllowsTool(%q) = %v, want %v", tt.exposed, got, tt.want)
			}
		})
	}
}
