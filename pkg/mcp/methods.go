package mcp

import "encoding/json"

// MCP method names GarmX handles or forwards. Kept as constants so the dispatch
// switch and the transports agree on exact spelling.
const (
	MethodInitialize            = "initialize"
	MethodInitialized           = "notifications/initialized"
	MethodPing                  = "ping"
	MethodToolsList             = "tools/list"
	MethodToolsCall             = "tools/call"
	MethodPromptsList           = "prompts/list"
	MethodPromptsGet            = "prompts/get"
	MethodResourcesList         = "resources/list"
	MethodResourcesRead         = "resources/read"
	MethodResourcesTemplateList = "resources/templates/list"

	// NotifyToolsListChanged and friends are emitted by upstreams when their
	// catalog changes; GarmX forwards them to affected clients.
	NotifyToolsListChanged     = "notifications/tools/list_changed"
	NotifyPromptsListChanged   = "notifications/prompts/list_changed"
	NotifyResourcesListChanged = "notifications/resources/list_changed"
	NotifyCancelled            = "notifications/cancelled"
)

// Implementation identifies a party in the handshake (clientInfo / serverInfo).
// Only Name and Version are required by the spec; the richer fields are
// preserved when present (Claude Code sends title/description/websiteUrl).
type Implementation struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	WebsiteURL  string `json:"websiteUrl,omitempty"`
}

// InitializeParams is the client's initialize request payload. Capabilities is
// kept raw: GarmX records the client's advertised capabilities verbatim on the
// session (they differ per client — e.g. only Claude Code advertises
// elicitation) without needing a typed model of every sub-capability in v1.
type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
	ClientInfo      Implementation  `json:"clientInfo"`
}

// InitializeResult is GarmX's initialize response: the negotiated version, the
// merged server capabilities, GarmX's own identity, and optional instructions.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}
