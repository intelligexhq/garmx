package aggregator

import (
	"testing"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// TestNegotiateClientVersion pins the client-facing rule: echo a supported
// requested version, otherwise offer the preferred one, never error.
func TestNegotiateClientVersion(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		want      string
	}{
		{"current echoed", mcp.ProtocolVersion20251125, mcp.ProtocolVersion20251125},
		{"prior current echoed", mcp.ProtocolVersion20250618, mcp.ProtocolVersion20250618},
		{"unsupported known -> preferred", mcp.ProtocolVersion20250326, mcp.PreferredProtocolVersion},
		{"legacy -> preferred", mcp.ProtocolVersion20241105, mcp.PreferredProtocolVersion},
		{"empty -> preferred", "", mcp.PreferredProtocolVersion},
		{"garbage -> preferred", "not-a-version", mcp.PreferredProtocolVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NegotiateClientVersion(tt.requested); got != tt.want {
				t.Fatalf("NegotiateClientVersion(%q) = %q, want %q", tt.requested, got, tt.want)
			}
		})
	}
}

// TestNegotiateUpstreamVersion pins the upstream rule: accept whatever the
// upstream reports, but flag an unrecognized version as a mismatch so the UI
// shows it rather than silently dropping the server.
func TestNegotiateUpstreamVersion(t *testing.T) {
	tests := []struct {
		name         string
		reported     string
		wantAccepted string
		wantMismatch bool
	}{
		{"current", mcp.ProtocolVersion20251125, mcp.ProtocolVersion20251125, false},
		{"prior", mcp.ProtocolVersion20250618, mcp.ProtocolVersion20250618, false},
		{"legacy known", mcp.ProtocolVersion20241105, mcp.ProtocolVersion20241105, false},
		{"streamable-http era known", mcp.ProtocolVersion20250326, mcp.ProtocolVersion20250326, false},
		{"empty -> mismatch", "", "", true},
		{"unknown future -> mismatch", "2099-01-01", "2099-01-01", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accepted, mismatch := NegotiateUpstreamVersion(tt.reported)
			if accepted != tt.wantAccepted || mismatch != tt.wantMismatch {
				t.Fatalf("NegotiateUpstreamVersion(%q) = (%q, %v), want (%q, %v)",
					tt.reported, accepted, mismatch, tt.wantAccepted, tt.wantMismatch)
			}
		})
	}
}

// TestMergeServerCapabilities pins the union semantics: present-if-any, and OR
// for the boolean sub-flags across upstreams that expose the capability.
func TestMergeServerCapabilities(t *testing.T) {
	t.Run("empty input yields empty caps", func(t *testing.T) {
		got := MergeServerCapabilities()
		if got.Tools != nil || got.Prompts != nil || got.Resources != nil || got.Logging != nil || got.Completions != nil {
			t.Fatalf("MergeServerCapabilities() = %+v, want all nil", got)
		}
	})

	t.Run("listChanged ORs across upstreams", func(t *testing.T) {
		got := MergeServerCapabilities(
			mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{ListChanged: false}},
			mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{ListChanged: true}},
		)
		if got.Tools == nil || !got.Tools.ListChanged {
			t.Fatalf("merged Tools = %+v, want ListChanged true", got.Tools)
		}
	})

	t.Run("resources subscribe and listChanged OR independently", func(t *testing.T) {
		got := MergeServerCapabilities(
			mcp.ServerCapabilities{Resources: &mcp.ResourcesCapability{Subscribe: true}},
			mcp.ServerCapabilities{Resources: &mcp.ResourcesCapability{ListChanged: true}},
		)
		if got.Resources == nil || !got.Resources.Subscribe || !got.Resources.ListChanged {
			t.Fatalf("merged Resources = %+v, want Subscribe && ListChanged", got.Resources)
		}
	})

	t.Run("presence is unioned across differing upstreams", func(t *testing.T) {
		got := MergeServerCapabilities(
			mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}, Logging: &mcp.LoggingCapability{}},
			mcp.ServerCapabilities{Prompts: &mcp.PromptsCapability{}, Completions: &mcp.CompletionsCapability{}},
		)
		if got.Tools == nil || got.Prompts == nil || got.Logging == nil || got.Completions == nil {
			t.Fatalf("merged = %+v, want Tools+Prompts+Logging+Completions present", got)
		}
		if got.Resources != nil {
			t.Fatalf("merged Resources = %+v, want nil (no upstream had resources)", got.Resources)
		}
	})
}
