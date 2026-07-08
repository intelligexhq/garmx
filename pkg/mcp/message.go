package mcp

import (
	"encoding/json"
	"fmt"
)

// Version is the JSON-RPC protocol version string carried by every MCP message.
const Version = "2.0"

// JSON-RPC 2.0 error codes used by GarmX. The reserved range −32768..−32000 is
// defined by the JSON-RPC spec; MCP adds no new codes in this range.
const (
	// CodeParseError signals invalid JSON was received.
	CodeParseError = -32700
	// CodeInvalidRequest signals the payload was not a valid Request object.
	CodeInvalidRequest = -32600
	// CodeMethodNotFound signals the method does not exist or is not supported.
	CodeMethodNotFound = -32601
	// CodeInvalidParams signals invalid method parameters.
	CodeInvalidParams = -32602
	// CodeInternalError signals an internal JSON-RPC error.
	CodeInternalError = -32603
)

// Error is a JSON-RPC 2.0 error object. It doubles as a Go error so upstream
// failures can flow through normal error handling while preserving the wire
// code and any structured data.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface, rendering code and message.
func (e *Error) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// NewError builds an *Error with the given code and message and no data.
func NewError(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Request is a JSON-RPC request or (when ID is empty) a notification. Params is
// kept raw so the hot path can route on Method/ID without decoding a payload it
// may only forward. ID is raw to preserve the client's exact form (integer vs
// string) for correlation and echoing.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response. Exactly one of Result or Error is set. ID
// echoes the originating request's raw ID verbatim.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// NewResponse builds a successful Response echoing id with the given raw result.
func NewResponse(id, result json.RawMessage) *Response {
	return &Response{JSONRPC: Version, ID: id, Result: result}
}

// NewErrorResponse builds a failed Response echoing id with a code and message.
func NewErrorResponse(id json.RawMessage, code int, message string) *Response {
	return &Response{JSONRPC: Version, ID: id, Error: NewError(code, message)}
}

// Notification is a JSON-RPC message with no ID: fire-and-forget, no reply
// expected. GarmX both receives these (e.g. notifications/initialized) and
// emits them (e.g. forwarding notifications/tools/list_changed to clients).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// NewNotification builds a Notification with the given method and raw params.
func NewNotification(method string, params json.RawMessage) *Notification {
	return &Notification{JSONRPC: Version, Method: method, Params: params}
}
