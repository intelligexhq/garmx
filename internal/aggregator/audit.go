package aggregator

import "encoding/json"

// Event is one audited transaction produced by the aggregator. It is a plain
// data record with no persistence concern of its own: the aggregator emits it
// and a Sink decides what (if anything) to store. Keeping the type here — in the
// producer — is what lets the aggregator stay free of any storage/SQLite import.
//
// RequestParams is the client's params as received (before name rewriting);
// ResponseResult is the upstream's result. Either may be empty. ErrorCode is nil
// on success and otherwise carries the JSON-RPC error code returned to the
// client.
type Event struct {
	SessionID     string
	ClientName    string
	ClientVersion string

	Method       string
	Server       string
	ToolExposed  string
	ToolOriginal string
	RPCID        string

	RequestParams  json.RawMessage
	ResponseResult json.RawMessage
	ErrorCode      *int
	ErrorMessage   string
	LatencyMS      int64
}

// Sink receives audit events. Implementations must be safe for concurrent use
// and must not block the caller: Record is invoked on the request hot path, so
// the sink is expected to hand off (e.g. a buffered channel) and return
// immediately, dropping rather than blocking under back-pressure.
type Sink interface {
	Record(Event)
}
