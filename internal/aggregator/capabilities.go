package aggregator

import "github.com/intelligexhq/garmx/pkg/mcp"

// supportedClientVersions are the protocol versions GarmX echoes back verbatim
// on its client-facing initialize. A client requesting one of these gets it
// unchanged; anything else is answered with mcp.PreferredProtocolVersion and
// the client decides whether to proceed. GarmX never fails initialize on
// version alone — the captured clients negotiate leniently (see
// docs/research/client-handshakes.md).
var supportedClientVersions = map[string]struct{}{
	mcp.ProtocolVersion20251125: {},
	mcp.ProtocolVersion20250618: {},
}

// NegotiateClientVersion picks the protocolVersion GarmX returns to a client on
// initialize. It echoes the client's requested version when GarmX supports it
// on the client face, otherwise it offers PreferredProtocolVersion. It never
// returns an error: a version mismatch is not fatal for the observed clients,
// which proceed even when the server answers a different version.
func NegotiateClientVersion(requested string) string {
	if _, ok := supportedClientVersions[requested]; ok {
		return requested
	}
	return mcp.PreferredProtocolVersion
}

// NegotiateUpstreamVersion records the outcome of an upstream's initialize
// response. GarmX sends PreferredProtocolVersion and the upstream answers with
// reported. GarmX accepts whatever the upstream returns — MCP is wire-
// compatible across the known revisions for the methods GarmX uses — but a
// version it does not recognize is flagged as a mismatch so the UI can surface
// a degraded status rather than silently dropping the server. accepted is the
// value to persist in servers.protocol_version.
func NegotiateUpstreamVersion(reported string) (accepted string, mismatch bool) {
	return reported, !mcp.IsKnownProtocolVersion(reported)
}

// MergeServerCapabilities unions the capabilities of several upstreams into the
// single capability set GarmX advertises to a client. A capability is present
// if ANY upstream has it; boolean sub-flags (listChanged, subscribe) are the OR
// across the upstreams that expose that capability. This encodes the decision
// that GarmX advertises a feature whenever at least one upstream can serve it,
// and forwards the corresponding list-changed notifications accordingly.
func MergeServerCapabilities(upstreams ...mcp.ServerCapabilities) mcp.ServerCapabilities {
	var merged mcp.ServerCapabilities
	for _, u := range upstreams {
		if u.Tools != nil {
			if merged.Tools == nil {
				merged.Tools = &mcp.ToolsCapability{}
			}
			merged.Tools.ListChanged = merged.Tools.ListChanged || u.Tools.ListChanged
		}
		if u.Prompts != nil {
			if merged.Prompts == nil {
				merged.Prompts = &mcp.PromptsCapability{}
			}
			merged.Prompts.ListChanged = merged.Prompts.ListChanged || u.Prompts.ListChanged
		}
		if u.Resources != nil {
			if merged.Resources == nil {
				merged.Resources = &mcp.ResourcesCapability{}
			}
			merged.Resources.ListChanged = merged.Resources.ListChanged || u.Resources.ListChanged
			merged.Resources.Subscribe = merged.Resources.Subscribe || u.Resources.Subscribe
		}
		if u.Logging != nil && merged.Logging == nil {
			merged.Logging = &mcp.LoggingCapability{}
		}
		if u.Completions != nil && merged.Completions == nil {
			merged.Completions = &mcp.CompletionsCapability{}
		}
	}
	return merged
}
