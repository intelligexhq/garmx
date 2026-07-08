package aggregator

import (
	"encoding/json"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// Session is the state of one client conversation (one stdio connection, or one
// Streamable HTTP session id). It records what was negotiated at initialize so
// later requests are handled in that client's terms.
//
// Client capabilities are stored verbatim: they differ per client (only Claude
// Code advertises elicitation; roots varies), and recording them per session is
// what lets deferred server→client features light up later without a rewrite.
type Session struct {
	// ProtocolVersion is the version GarmX returned to this client.
	ProtocolVersion string
	// ClientInfo identifies the connected client (from initialize).
	ClientInfo mcp.Implementation
	// ClientCapabilities is the client's advertised capability object, verbatim.
	ClientCapabilities json.RawMessage
	// Initialized is set once the client's notifications/initialized arrives.
	Initialized bool
}
