package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRedact verifies secrets are scrubbed at any depth while non-secret data
// and structure survive, and that non-JSON input never round-trips verbatim.
func TestRedact(t *testing.T) {
	tests := []struct {
		name       string
		extra      []string
		in         string
		wantSubstr []string // must appear in output
		wantAbsent []string // must NOT appear in output
	}{
		{
			name:       "top-level secret key",
			in:         `{"token":"sk-abc123","name":"query"}`,
			wantSubstr: []string{redactedMarker, `"name":"query"`},
			wantAbsent: []string{"sk-abc123"},
		},
		{
			name:       "nested under arguments",
			in:         `{"name":"login","arguments":{"user":"bob","password":"hunter2"}}`,
			wantSubstr: []string{redactedMarker, `"user":"bob"`},
			wantAbsent: []string{"hunter2"},
		},
		{
			name:       "case-insensitive key match",
			in:         `{"Authorization":"Bearer xyz","APIKey":"k"}`,
			wantAbsent: []string{"Bearer xyz", `"k"`},
		},
		{
			name:       "inside arrays",
			in:         `{"items":[{"secret":"s1"},{"secret":"s2","ok":1}]}`,
			wantSubstr: []string{`"ok":1`},
			wantAbsent: []string{"s1", "s2"},
		},
		{
			name:       "extra key additive",
			extra:      []string{"ssn"},
			in:         `{"ssn":"123-45-6789","keep":"yes"}`,
			wantSubstr: []string{`"keep":"yes"`},
			wantAbsent: []string{"123-45-6789"},
		},
		{
			name:       "non-JSON becomes placeholder, not verbatim",
			in:         `not json at all secret=hunter2`,
			wantAbsent: []string{"hunter2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRedactor(tt.extra)
			out := string(r.Redact(json.RawMessage(tt.in)))
			for _, s := range tt.wantSubstr {
				if !strings.Contains(out, s) {
					t.Errorf("output %q missing expected %q", out, s)
				}
			}
			for _, s := range tt.wantAbsent {
				if strings.Contains(out, s) {
					t.Errorf("output %q must not contain secret %q", out, s)
				}
			}
		})
	}
}

// TestRedactEmpty confirms empty input passes through untouched.
func TestRedactEmpty(t *testing.T) {
	r := NewRedactor(nil)
	if got := r.Redact(nil); got != nil {
		t.Errorf("Redact(nil) = %q, want nil", got)
	}
}
