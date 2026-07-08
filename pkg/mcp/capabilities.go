package mcp

// Protocol version identifiers GarmX recognizes. MCP versions are ISO date
// strings; a lexicographically greater string is a newer revision. These are
// the revisions whose wire behaviour GarmX has validated against real clients
// and servers (see docs/research/client-handshakes.md).
const (
	// ProtocolVersion20241105 is the legacy revision (HTTP+SSE era). GarmX does
	// not implement that transport but still accepts the version from upstreams.
	ProtocolVersion20241105 = "2024-11-05"
	// ProtocolVersion20250326 introduced the Streamable HTTP transport.
	ProtocolVersion20250326 = "2025-03-26"
	// ProtocolVersion20250618 dropped JSON-RPC batching.
	ProtocolVersion20250618 = "2025-06-18"
	// ProtocolVersion20251125 is the current revision; both first-target clients
	// (Claude Code, OpenCode) request it on initialize.
	ProtocolVersion20251125 = "2025-11-25"
)

// PreferredProtocolVersion is the version GarmX advertises on its client face
// and sends when initializing upstreams. Chosen from captured evidence: both
// Claude Code and OpenCode request 2025-11-25.
const PreferredProtocolVersion = ProtocolVersion20251125

// knownProtocolVersions is the set of revisions GarmX recognizes. An upstream
// reporting a version outside this set is accepted but flagged as a mismatch
// (never silently dropped) so the difference is visible — see
// aggregator.NegotiateUpstreamVersion.
var knownProtocolVersions = map[string]struct{}{
	ProtocolVersion20241105: {},
	ProtocolVersion20250326: {},
	ProtocolVersion20250618: {},
	ProtocolVersion20251125: {},
}

// IsKnownProtocolVersion reports whether v is a protocol revision GarmX
// recognizes. An unknown version is the trigger for a visible mismatch status
// rather than a silent failure.
func IsKnownProtocolVersion(v string) bool {
	_, ok := knownProtocolVersions[v]
	return ok
}

// ServerCapabilities is the subset of MCP server capabilities GarmX merges
// across upstreams and advertises to a client. Each field is a pointer so that
// "absent" (nil) is distinct from "present with no sub-flags" (a non-nil empty
// struct): under the MCP wire contract the mere presence of a capability object
// is itself meaningful.
type ServerCapabilities struct {
	Tools       *ToolsCapability       `json:"tools,omitempty"`
	Prompts     *PromptsCapability     `json:"prompts,omitempty"`
	Resources   *ResourcesCapability   `json:"resources,omitempty"`
	Logging     *LoggingCapability     `json:"logging,omitempty"`
	Completions *CompletionsCapability `json:"completions,omitempty"`
}

// ToolsCapability advertises tool support. ListChanged signals the server emits
// notifications/tools/list_changed when its tool set changes.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability advertises prompt support. ListChanged is as for tools.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability advertises resource support. Subscribe signals per-
// resource update subscriptions; ListChanged is as for tools.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// LoggingCapability is a presence-only capability (the server can emit log
// notifications); it carries no sub-fields.
type LoggingCapability struct{}

// CompletionsCapability is a presence-only capability (the server supports
// completion/complete); it carries no sub-fields.
type CompletionsCapability struct{}
