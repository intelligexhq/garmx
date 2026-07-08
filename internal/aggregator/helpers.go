package aggregator

import (
	"context"
	"encoding/json"

	"github.com/intelligexhq/garmx/internal/upstream"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// forward sends a request to an upstream and maps the outcome onto a client
// response, echoing the client's id. Result and rpc-error pass through; a
// transport failure becomes an internal error.
func forward(ctx context.Context, t upstream.Transport, id json.RawMessage, method string, params json.RawMessage) *mcp.Response {
	result, rpcErr, err := t.Send(ctx, method, params)
	if err != nil {
		return mcp.NewErrorResponse(id, mcp.CodeInternalError, "upstream "+method+" failed")
	}
	if rpcErr != nil {
		return &mcp.Response{JSONRPC: mcp.Version, ID: id, Error: rpcErr}
	}
	return mcp.NewResponse(id, result)
}

// marshalResult encodes v as a successful response for id.
func marshalResult(id json.RawMessage, v any) *mcp.Response {
	raw, err := json.Marshal(v)
	if err != nil {
		return mcp.NewErrorResponse(id, mcp.CodeInternalError, "encode result")
	}
	return mcp.NewResponse(id, raw)
}

// listResponse builds a *_/list result object placing items under itemsKey,
// normalizing a nil slice to an empty array and omitting any client cursor.
func listResponse(id json.RawMessage, itemsKey string, items []json.RawMessage) *mcp.Response {
	if items == nil {
		items = []json.RawMessage{}
	}
	out, err := json.Marshal(map[string]any{itemsKey: items})
	if err != nil {
		return mcp.NewErrorResponse(id, mcp.CodeInternalError, "encode list result")
	}
	return mcp.NewResponse(id, out)
}

// prefixItemName rewrites only the "name" field of a list item to its exposed
// (prefixed) form, preserving every other field byte-for-byte, and returns the
// exposed name so the caller can apply profile filtering.
func prefixItemName(server string, item json.RawMessage) (exposed string, rewritten json.RawMessage, err error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(item, &fields); err != nil {
		return "", nil, err
	}
	var name string
	if err := json.Unmarshal(fields["name"], &name); err != nil {
		return "", nil, err
	}
	exposed = Prefix(server, name)
	fields["name"], _ = json.Marshal(exposed)
	rewritten, err = json.Marshal(fields)
	if err != nil {
		return "", nil, err
	}
	return exposed, rewritten, nil
}

// itemURI extracts the "uri" field from a resource list item, or "" if absent.
func itemURI(item json.RawMessage) string {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(item, &p); err != nil {
		return ""
	}
	return p.URI
}

// decodeList extracts the items under itemsKey and the nextCursor from a
// *_/list result, leaving items raw so their unrelated fields are untouched.
func decodeList(result json.RawMessage, itemsKey string) (items []json.RawMessage, nextCursor string, err error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil, "", err
	}
	if raw, ok := obj[itemsKey]; ok {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, "", err
		}
	}
	if raw, ok := obj["nextCursor"]; ok {
		if err := json.Unmarshal(raw, &nextCursor); err != nil {
			return nil, "", err
		}
	}
	return items, nextCursor, nil
}

// listParams builds upstream *_/list params, including a cursor only when
// continuing a drain.
func listParams(cursor string) json.RawMessage {
	if cursor == "" {
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"cursor": cursor})
	return raw
}

// hasCursor reports whether client params carry a non-empty cursor, which GarmX
// rejects because it issues none.
func hasCursor(params json.RawMessage) bool {
	if len(params) == 0 {
		return false
	}
	var p struct {
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return false
	}
	return p.Cursor != ""
}
