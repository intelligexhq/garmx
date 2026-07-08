package mcp

import (
	"encoding/json"
	"testing"
)

// TestParseKind pins the classification the read loops depend on: requests have
// a method + id, notifications have a method + no id (or null id), responses
// have an id + no method.
func TestParseKind(t *testing.T) {
	tests := []struct {
		name           string
		line           string
		wantRequest    bool
		wantNotify     bool
		wantResponse   bool
		wantMethod     string
		wantHasID      bool
		wantResultNull bool
	}{
		{
			name:        "request",
			line:        `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			wantRequest: true, wantMethod: "tools/list", wantHasID: true,
		},
		{
			name:       "notification without id",
			line:       `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			wantNotify: true, wantMethod: "notifications/initialized",
		},
		{
			name:       "null id counts as notification",
			line:       `{"jsonrpc":"2.0","id":null,"method":"notifications/cancelled"}`,
			wantNotify: true, wantMethod: "notifications/cancelled",
		},
		{
			name:         "response with result",
			line:         `{"jsonrpc":"2.0","id":2,"result":{"ok":true}}`,
			wantResponse: true, wantHasID: true,
		},
		{
			name:        "string id request",
			line:        `{"jsonrpc":"2.0","id":"abc","method":"ping"}`,
			wantRequest: true,
			wantMethod:  "ping",
			wantHasID:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := Parse([]byte(tt.line))
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.line, err)
			}
			if e.IsRequest() != tt.wantRequest {
				t.Errorf("IsRequest = %v, want %v", e.IsRequest(), tt.wantRequest)
			}
			if e.IsNotification() != tt.wantNotify {
				t.Errorf("IsNotification = %v, want %v", e.IsNotification(), tt.wantNotify)
			}
			if e.IsResponse() != tt.wantResponse {
				t.Errorf("IsResponse = %v, want %v", e.IsResponse(), tt.wantResponse)
			}
			if e.HasID() != tt.wantHasID {
				t.Errorf("HasID = %v, want %v", e.HasID(), tt.wantHasID)
			}
			if tt.wantMethod != "" && e.Method != tt.wantMethod {
				t.Errorf("Method = %q, want %q", e.Method, tt.wantMethod)
			}
		})
	}
}

// TestParseInvalidJSON asserts Parse surfaces a decode error rather than
// returning a partial envelope.
func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte(`{not json`)); err == nil {
		t.Fatal("Parse of invalid JSON returned nil error")
	}
}

// TestResponseRoundTrip asserts a response echoes the raw id verbatim and
// serializes to the expected wire shape.
func TestResponseRoundTrip(t *testing.T) {
	resp := NewResponse(json.RawMessage(`7`), json.RawMessage(`{"tools":[]}`))
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	e, err := Parse(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !e.IsResponse() {
		t.Fatalf("round-tripped message is not a response: %s", b)
	}
	if string(e.ID) != "7" {
		t.Fatalf("id = %s, want 7", e.ID)
	}
}

// TestErrorResponseShape asserts an error response carries the code/message and
// no result field.
func TestErrorResponseShape(t *testing.T) {
	resp := NewErrorResponse(json.RawMessage(`1`), CodeMethodNotFound, "nope")
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	e, err := Parse(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if e.Error == nil || e.Error.Code != CodeMethodNotFound {
		t.Fatalf("error not preserved: %s", b)
	}
	if len(e.Result) != 0 {
		t.Fatalf("result should be absent on error response: %s", b)
	}
}
