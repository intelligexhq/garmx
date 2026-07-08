package aggregator

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateServerName pins the registration-time contract: lowercase
// alphanumerics + hyphens, alphanumeric first char, no underscores, 1..32 long.
// The no-underscore rule is what keeps the "___" Split unambiguous.
func TestValidateServerName(t *testing.T) {
	tests := []struct {
		name    string
		server  string
		wantErr bool
	}{
		{"simple", "pg", false},
		{"with digits", "github2", false},
		{"digit first", "9pg", false},
		{"hyphenated", "my-server", false},
		{"single char", "a", false},
		{"max length", strings.Repeat("a", maxServerNameLen), false},
		{"empty", "", true},
		{"uppercase", "Postgres", true},
		{"underscore", "a_b", true},
		{"triple underscore", "a___b", true},
		{"leading hyphen", "-a", true},
		{"space", "a b", true},
		{"dot", "a.b", true},
		{"too long", strings.Repeat("a", maxServerNameLen+1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServerName(tt.server)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateServerName(%q) = nil, want error", tt.server)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateServerName(%q) = %v, want nil", tt.server, err)
			}
			if tt.wantErr && err != nil && !errors.Is(err, ErrInvalidServerName) {
				t.Fatalf("ValidateServerName(%q) error = %v, want wrapping ErrInvalidServerName", tt.server, err)
			}
		})
	}
}

// TestSplit pins the reverse mapping, including the two subtle cases: a tool
// name with a single underscore is left intact, and an upstream name that
// itself contains "___" round-trips because Split cuts on the FIRST delimiter.
func TestSplit(t *testing.T) {
	tests := []struct {
		name       string
		exposed    string
		wantServer string
		wantName   string
		wantOK     bool
	}{
		{"plain", "pg___query", "pg", "query", true},
		{"single underscore in tool", "github___create_issue", "github", "create_issue", true},
		{"delimiter inside original name", "pg___we___ird", "pg", "we___ird", true},
		{"no delimiter", "query", "", "", false},
		{"empty server", "___query", "", "", false},
		{"empty name", "pg___", "", "", false},
		{"invalid server (underscore)", "a_b___query", "", "", false},
		{"invalid server (uppercase)", "PG___query", "", "", false},
		{"empty string", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, name, ok := Split(tt.exposed)
			if ok != tt.wantOK || server != tt.wantServer || name != tt.wantName {
				t.Fatalf("Split(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.exposed, server, name, ok, tt.wantServer, tt.wantName, tt.wantOK)
			}
		})
	}
}

// TestPrefixSplitRoundTrip asserts Prefix and Split are inverses for any valid
// server name and non-empty original name — including originals containing the
// delimiter or single underscores.
func TestPrefixSplitRoundTrip(t *testing.T) {
	cases := []struct{ server, name string }{
		{"pg", "query"},
		{"github", "create_issue"},
		{"my-server", "do_thing"},
		{"pg", "we___ird"},
		{"a", "b"},
	}
	for _, c := range cases {
		exposed := Prefix(c.server, c.name)
		server, name, ok := Split(exposed)
		if !ok || server != c.server || name != c.name {
			t.Fatalf("round-trip %q/%q via %q = (%q, %q, %v)", c.server, c.name, exposed, server, name, ok)
		}
	}
}

// TestExceedsLengthBudget pins the advisory threshold: exactly LengthBudget is
// fine; one over trips the warning.
func TestExceedsLengthBudget(t *testing.T) {
	atBudget := strings.Repeat("x", LengthBudget)
	overBudget := strings.Repeat("x", LengthBudget+1)
	if ExceedsLengthBudget(atBudget) {
		t.Fatalf("ExceedsLengthBudget(len=%d) = true, want false", LengthBudget)
	}
	if !ExceedsLengthBudget(overBudget) {
		t.Fatalf("ExceedsLengthBudget(len=%d) = false, want true", LengthBudget+1)
	}
}
