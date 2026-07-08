package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/intelligexhq/garmx/internal/upstream"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// Aggregator is the protocol core for one client session. In this phase it
// fronts exactly one upstream; the dispatch seam (prefix on the way out, split
// on the way in, per-upstream routing) is the same one multi-server aggregation
// will extend. It synthesizes initialize and every *_/list response and passes
// through calls/reads with name rewriting.
type Aggregator struct {
	serverName string // the upstream's registered name; the exposed-name prefix
	version    string // GarmX version, reported as serverInfo
	up         upstream.Transport
	logger     *slog.Logger

	session Session

	// upstream initialize is performed lazily, once, on the first client
	// initialize; upInit guards it and upCaps/upInfo cache the result.
	upInit sync.Once
	upErr  error
	upCaps mcp.ServerCapabilities
	upInfo mcp.Implementation

	notifyMu     sync.Mutex
	clientNotify func(*mcp.Notification)
}

// New constructs an Aggregator fronting a single upstream registered as
// serverName. It registers the upstream notification handler immediately, so it
// must be called before the transport is started. logger must be non-nil.
func New(serverName, version string, up upstream.Transport, logger *slog.Logger) *Aggregator {
	a := &Aggregator{
		serverName: serverName,
		version:    version,
		up:         up,
		logger:     logger.With("component", "aggregator"),
	}
	up.SetHandlers(upstream.Handlers{OnNotification: a.onUpstreamNotification})
	return a
}

// SetClientNotifier registers the callback used to push server→client
// notifications (e.g. a forwarded list_changed) to this session's client. The
// frontend sets it once it can write to the client.
func (a *Aggregator) SetClientNotifier(fn func(*mcp.Notification)) {
	a.notifyMu.Lock()
	a.clientNotify = fn
	a.notifyMu.Unlock()
}

// onUpstreamNotification forwards an upstream notification to the client. In
// this phase it is a straight passthrough; the rebuild-merged-view-then-emit
// and debounce logic (notify.go) arrives with multi-server aggregation. A
// tools/list_changed still reaches the client, which — per the captures — will
// re-fetch tools/list.
func (a *Aggregator) onUpstreamNotification(n *mcp.Notification) {
	a.notifyMu.Lock()
	fn := a.clientNotify
	a.notifyMu.Unlock()
	if fn != nil {
		fn(n)
	}
}

// HandleNotification processes a client→gateway notification. The client's
// notifications/initialized only marks the session ready: GarmX already
// completed its own upstream handshake during initialize, so it is not
// re-forwarded. Everything else (e.g. OpenCode's post-call
// notifications/cancelled for an already-finished call) is logged and dropped.
func (a *Aggregator) HandleNotification(_ context.Context, env *mcp.Envelope) {
	switch env.Method {
	case mcp.MethodInitialized:
		a.session.Initialized = true
	default:
		a.logger.Debug("dropping client notification", "method", env.Method)
	}
}

// Handle dispatches one client request and returns the response to send back.
// It never returns nil: every request gets a response (a JSON-RPC error at
// worst), so the frontend can write unconditionally.
func (a *Aggregator) Handle(ctx context.Context, env *mcp.Envelope) *mcp.Response {
	switch env.Method {
	case mcp.MethodInitialize:
		return a.handleInitialize(ctx, env)
	case mcp.MethodPing:
		return mcp.NewResponse(env.ID, json.RawMessage(`{}`))
	case mcp.MethodToolsList:
		return a.handleList(ctx, env, mcp.MethodToolsList, "tools", true)
	case mcp.MethodPromptsList:
		return a.handleList(ctx, env, mcp.MethodPromptsList, "prompts", true)
	case mcp.MethodResourcesList:
		return a.handleList(ctx, env, mcp.MethodResourcesList, "resources", false)
	case mcp.MethodResourcesTemplateList:
		return a.handleList(ctx, env, mcp.MethodResourcesTemplateList, "resourceTemplates", false)
	case mcp.MethodToolsCall:
		return a.handleNamedCall(ctx, env, mcp.MethodToolsCall)
	case mcp.MethodPromptsGet:
		return a.handleNamedCall(ctx, env, mcp.MethodPromptsGet)
	case mcp.MethodResourcesRead:
		return a.passthrough(ctx, env, mcp.MethodResourcesRead)
	default:
		return mcp.NewErrorResponse(env.ID, mcp.CodeMethodNotFound, "method not found: "+env.Method)
	}
}

// handleInitialize completes GarmX's upstream handshake (once), records the
// session, and returns the negotiated version plus the merged capabilities.
func (a *Aggregator) handleInitialize(ctx context.Context, env *mcp.Envelope) *mcp.Response {
	var params mcp.InitializeParams
	if err := json.Unmarshal(env.Params, &params); err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "invalid initialize params")
	}

	if err := a.ensureUpstream(ctx); err != nil {
		a.logger.Error("upstream initialize failed", "err", err)
		return mcp.NewErrorResponse(env.ID, mcp.CodeInternalError, "upstream initialize failed")
	}

	a.session = Session{
		ProtocolVersion:    NegotiateClientVersion(params.ProtocolVersion),
		ClientInfo:         params.ClientInfo,
		ClientCapabilities: params.Capabilities,
	}

	result := mcp.InitializeResult{
		ProtocolVersion: a.session.ProtocolVersion,
		Capabilities:    MergeServerCapabilities(a.upCaps),
		ServerInfo:      mcp.Implementation{Name: "garmx", Title: "GarmX", Version: a.version},
		Instructions:    "GarmX aggregating MCP gateway.",
	}
	return marshalResult(env.ID, result)
}

// ensureUpstream performs GarmX's client-side handshake with the upstream
// exactly once: initialize, record capabilities/version, then send
// notifications/initialized. Subsequent client sessions reuse the cached result.
func (a *Aggregator) ensureUpstream(ctx context.Context) error {
	a.upInit.Do(func() {
		reqParams := mcp.InitializeParams{
			ProtocolVersion: mcp.PreferredProtocolVersion,
			Capabilities:    json.RawMessage(`{}`),
			ClientInfo:      mcp.Implementation{Name: "garmx", Version: a.version},
		}
		raw, _ := json.Marshal(reqParams)
		result, rpcErr, err := a.up.Send(ctx, mcp.MethodInitialize, raw)
		if err != nil {
			a.upErr = err
			return
		}
		if rpcErr != nil {
			a.upErr = rpcErr
			return
		}
		var res mcp.InitializeResult
		if err := json.Unmarshal(result, &res); err != nil {
			a.upErr = fmt.Errorf("decode upstream initialize result: %w", err)
			return
		}
		a.upCaps = res.Capabilities
		a.upInfo = res.ServerInfo
		accepted, mismatch := NegotiateUpstreamVersion(res.ProtocolVersion)
		if mismatch {
			a.logger.Warn("upstream reported unrecognized protocol version", "version", accepted)
		}
		a.logger.Info("upstream initialized",
			"server", a.upInfo.Name, "serverVersion", a.upInfo.Version, "protocol", accepted)
		if err := a.up.Notify(ctx, mcp.MethodInitialized, nil); err != nil {
			a.logger.Warn("failed to send notifications/initialized to upstream", "err", err)
		}
	})
	return a.upErr
}

// handleList synthesizes a *_/list response by eagerly draining every upstream
// page (following nextCursor to exhaustion) and, for tools/prompts, rewriting
// each item's name to its prefixed form. No client-facing cursor is emitted; a
// client-supplied cursor is rejected, since GarmX issues none.
func (a *Aggregator) handleList(ctx context.Context, env *mcp.Envelope, method, itemsKey string, prefixNames bool) *mcp.Response {
	if hasCursor(env.Params) {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "cursor pagination is not supported; GarmX returns the full list")
	}

	var items []json.RawMessage
	cursor := ""
	for {
		params := listParams(cursor)
		result, rpcErr, err := a.up.Send(ctx, method, params)
		if err != nil {
			return mcp.NewErrorResponse(env.ID, mcp.CodeInternalError, "upstream "+method+" failed")
		}
		if rpcErr != nil {
			return &mcp.Response{JSONRPC: mcp.Version, ID: env.ID, Error: rpcErr}
		}
		page, next, err := decodeList(result, itemsKey)
		if err != nil {
			return mcp.NewErrorResponse(env.ID, mcp.CodeInternalError, "malformed upstream "+method+" result")
		}
		for _, item := range page {
			if prefixNames {
				rewritten, err := a.prefixItemName(item)
				if err != nil {
					a.logger.Warn("skipping item with unrewritable name", "method", method, "err", err)
					continue
				}
				item = rewritten
			}
			items = append(items, item)
		}
		if next == "" {
			break
		}
		cursor = next
	}

	if items == nil {
		items = []json.RawMessage{}
	}
	out, err := json.Marshal(map[string]any{itemsKey: items})
	if err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInternalError, "encode list result")
	}
	return mcp.NewResponse(env.ID, out)
}

// handleNamedCall routes a name-addressed pass-through method (tools/call,
// prompts/get): it splits the exposed name back to (server, original), rejects
// a name that is not this server's, rewrites the name to the upstream's
// original, and forwards. The result flows back to the client unchanged.
func (a *Aggregator) handleNamedCall(ctx context.Context, env *mcp.Envelope, method string) *mcp.Response {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(env.Params, &fields); err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "invalid params for "+method)
	}
	var exposed string
	if err := json.Unmarshal(fields["name"], &exposed); err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "missing or invalid name for "+method)
	}
	server, original, ok := Split(exposed)
	if !ok || server != a.serverName {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "unknown name: "+exposed)
	}
	orig, _ := json.Marshal(original)
	fields["name"] = orig
	params, err := json.Marshal(fields)
	if err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInternalError, "re-encode params")
	}
	return a.forward(ctx, env.ID, method, params)
}

// passthrough forwards a method whose params need no rewriting (resources/read;
// resources are addressed by already-namespaced uri, not a prefixed name).
func (a *Aggregator) passthrough(ctx context.Context, env *mcp.Envelope, method string) *mcp.Response {
	return a.forward(ctx, env.ID, method, env.Params)
}

// forward sends a request to the upstream and maps the outcome onto a client
// response, echoing the client's id.
func (a *Aggregator) forward(ctx context.Context, id json.RawMessage, method string, params json.RawMessage) *mcp.Response {
	result, rpcErr, err := a.up.Send(ctx, method, params)
	if err != nil {
		return mcp.NewErrorResponse(id, mcp.CodeInternalError, "upstream "+method+" failed")
	}
	if rpcErr != nil {
		return &mcp.Response{JSONRPC: mcp.Version, ID: id, Error: rpcErr}
	}
	return mcp.NewResponse(id, result)
}

// prefixItemName rewrites only the "name" field of a list item to its exposed
// (prefixed) form, preserving every other field (description, schemas,
// annotations) byte-for-byte.
func (a *Aggregator) prefixItemName(item json.RawMessage) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(item, &fields); err != nil {
		return nil, err
	}
	var name string
	if err := json.Unmarshal(fields["name"], &name); err != nil {
		return nil, fmt.Errorf("item has no string name: %w", err)
	}
	prefixed, err := json.Marshal(Prefix(a.serverName, name))
	if err != nil {
		return nil, err
	}
	fields["name"] = prefixed
	return json.Marshal(fields)
}

// marshalResult encodes v as the result of a successful response for id.
func marshalResult(id json.RawMessage, v any) *mcp.Response {
	raw, err := json.Marshal(v)
	if err != nil {
		return mcp.NewErrorResponse(id, mcp.CodeInternalError, "encode result")
	}
	return mcp.NewResponse(id, raw)
}

// decodeList extracts the item array under itemsKey and the nextCursor from a
// *_/list result, leaving items as raw messages so their unrelated fields are
// untouched.
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

// listParams builds the params for an upstream *_/list call, including a cursor
// only when continuing a drain.
func listParams(cursor string) json.RawMessage {
	if cursor == "" {
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"cursor": cursor})
	return raw
}

// hasCursor reports whether client params carry a non-empty cursor, which GarmX
// rejects because it issues no cursors.
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
