package mcp

import (
	"bytes"
	"encoding/json"
)

// Envelope is a superset of the JSON-RPC message shapes (request, response,
// notification) used to classify an inbound line without committing to a
// direction. The read loops on both faces decode into an Envelope first, then
// dispatch on Kind. Params/Result stay raw so a forwarded payload is never
// needlessly re-encoded.
type Envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Parse decodes a single JSON-RPC line into an Envelope. It returns an error
// only when the bytes are not valid JSON; structural validity (which fields are
// present) is left to the Kind classification so the caller can respond with
// the correct JSON-RPC error.
func Parse(b []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// HasID reports whether the envelope carries a usable request/response id.
// A missing id or the literal null both count as "no id" (a notification),
// matching how real clients frame notifications/initialized and friends.
func (e *Envelope) HasID() bool {
	return len(e.ID) > 0 && !bytes.Equal(e.ID, []byte("null"))
}

// IsRequest reports whether the envelope is a method call expecting a response
// (has both a method and an id).
func (e *Envelope) IsRequest() bool {
	return e.Method != "" && e.HasID()
}

// IsNotification reports whether the envelope is a fire-and-forget message
// (has a method but no id).
func (e *Envelope) IsNotification() bool {
	return e.Method != "" && !e.HasID()
}

// IsResponse reports whether the envelope is a reply to a prior request (no
// method, but an id and a result or error).
func (e *Envelope) IsResponse() bool {
	return e.Method == "" && e.HasID()
}
