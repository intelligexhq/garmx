package aggregator

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/intelligexhq/garmx/internal/upstream"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// Aggregator is the protocol core for one client session. It fans out across
// every upstream in the manager, merges their capabilities and catalogs,
// prefixes exposed names with the owning server, and routes calls back by
// splitting the prefix. A Profile scopes what this session sees.
//
// Lists are served by live fan-out (no persistent cache): a client re-fetching
// after a list_changed always gets a fresh merged view, which keeps correctness
// simple; a per-profile cache is a later optimization, not a correctness need.
type Aggregator struct {
	mgr     *upstream.Manager
	profile Profile
	version string
	logger  *slog.Logger

	session  Session
	initOnce sync.Once
	caps     mcp.ServerCapabilities

	// uriOwners maps a resource uri to its owning server, populated on
	// resources/list so resources/read (addressed by uri, not a prefixed name)
	// can route. Guarded by uriMu.
	uriMu     sync.Mutex
	uriOwners map[string]string

	notif        *notifier
	notifyMu     sync.Mutex
	clientNotify func(*mcp.Notification)
}

// New constructs an Aggregator over the manager's upstreams, scoped by profile.
// It registers the manager's notification handler immediately, so it must be
// called before the upstreams are started. logger must be non-nil.
func New(mgr *upstream.Manager, profile Profile, version string, logger *slog.Logger) *Aggregator {
	a := &Aggregator{
		mgr:       mgr,
		profile:   profile,
		version:   version,
		logger:    logger.With("component", "aggregator"),
		uriOwners: map[string]string{},
	}
	a.notif = newNotifier(notifyDebounce, a.emitToClient)
	mgr.SetNotificationHandler(a.onUpstreamNotification)
	return a
}

// SetClientNotifier registers the callback used to push server→client
// notifications (a forwarded list_changed) to this session's client.
func (a *Aggregator) SetClientNotifier(fn func(*mcp.Notification)) {
	a.notifyMu.Lock()
	a.clientNotify = fn
	a.notifyMu.Unlock()
}

// onUpstreamNotification handles an upstream notification tagged with its
// server. A list_changed from an out-of-scope server is dropped; otherwise
// list_changed is coalesced and re-emitted to the client (which then re-fetches
// the affected list). Non-list notifications are forwarded as-is.
func (a *Aggregator) onUpstreamNotification(server string, n *mcp.Notification) {
	if !a.profile.AllowsServer(server) {
		return
	}
	if isListChanged(n.Method) {
		a.notif.schedule(n.Method)
		return
	}
	a.emitRaw(n)
}

// emitToClient sends a parameter-less notification (a coalesced list_changed) to
// the client.
func (a *Aggregator) emitToClient(method string) {
	a.emitRaw(mcp.NewNotification(method, nil))
}

// emitRaw pushes a notification to the client if a notifier is registered.
func (a *Aggregator) emitRaw(n *mcp.Notification) {
	a.notifyMu.Lock()
	fn := a.clientNotify
	a.notifyMu.Unlock()
	if fn != nil {
		fn(n)
	}
}

// HandleNotification processes a client→gateway notification. initialized marks
// the session ready (GarmX already handshook its upstreams during initialize);
// everything else (e.g. OpenCode's post-call cancelled) is dropped.
func (a *Aggregator) HandleNotification(_ context.Context, env *mcp.Envelope) {
	switch env.Method {
	case mcp.MethodInitialized:
		a.session.Initialized = true
	default:
		a.logger.Debug("dropping client notification", "method", env.Method)
	}
}

// Handle dispatches one client request and always returns a response.
func (a *Aggregator) Handle(ctx context.Context, env *mcp.Envelope) *mcp.Response {
	switch env.Method {
	case mcp.MethodInitialize:
		return a.handleInitialize(ctx, env)
	case mcp.MethodPing:
		return mcp.NewResponse(env.ID, json.RawMessage(`{}`))
	case mcp.MethodToolsList:
		return a.handlePrefixedList(ctx, env, mcp.MethodToolsList, "tools", true)
	case mcp.MethodPromptsList:
		return a.handlePrefixedList(ctx, env, mcp.MethodPromptsList, "prompts", false)
	case mcp.MethodResourcesList:
		return a.handleResourcesList(ctx, env, mcp.MethodResourcesList, "resources", true)
	case mcp.MethodResourcesTemplateList:
		return a.handleResourcesList(ctx, env, mcp.MethodResourcesTemplateList, "resourceTemplates", false)
	case mcp.MethodToolsCall:
		return a.handleNamedCall(ctx, env, mcp.MethodToolsCall, true)
	case mcp.MethodPromptsGet:
		return a.handleNamedCall(ctx, env, mcp.MethodPromptsGet, false)
	case mcp.MethodResourcesRead:
		return a.handleResourcesRead(ctx, env)
	default:
		return mcp.NewErrorResponse(env.ID, mcp.CodeMethodNotFound, "method not found: "+env.Method)
	}
}

// handleInitialize handshakes every in-scope upstream (once), records the
// session, and returns the negotiated version plus the union of upstream
// capabilities.
func (a *Aggregator) handleInitialize(ctx context.Context, env *mcp.Envelope) *mcp.Response {
	var params mcp.InitializeParams
	if err := json.Unmarshal(env.Params, &params); err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "invalid initialize params")
	}
	a.ensureUpstreams(ctx)

	a.session = Session{
		ProtocolVersion:    NegotiateClientVersion(params.ProtocolVersion),
		ClientInfo:         params.ClientInfo,
		ClientCapabilities: params.Capabilities,
	}
	result := mcp.InitializeResult{
		ProtocolVersion: a.session.ProtocolVersion,
		Capabilities:    a.caps,
		ServerInfo:      mcp.Implementation{Name: "garmx", Title: "GarmX", Version: a.version},
		Instructions:    "GarmX aggregating MCP gateway.",
	}
	return marshalResult(env.ID, result)
}

// ensureUpstreams performs GarmX's client-side handshake with each in-scope
// upstream exactly once, unioning their capabilities. A single upstream failing
// to initialize is logged and skipped (its catalog simply won't appear), so one
// broken server never blocks the whole aggregate.
func (a *Aggregator) ensureUpstreams(ctx context.Context) {
	a.initOnce.Do(func() {
		reqParams := mcp.InitializeParams{
			ProtocolVersion: mcp.PreferredProtocolVersion,
			Capabilities:    json.RawMessage(`{}`),
			ClientInfo:      mcp.Implementation{Name: "garmx", Version: a.version},
		}
		raw, _ := json.Marshal(reqParams)
		for _, server := range a.servers() {
			t, ok := a.mgr.Get(server)
			if !ok {
				continue
			}
			result, rpcErr, err := t.Send(ctx, mcp.MethodInitialize, raw)
			if err != nil || rpcErr != nil {
				a.logger.Warn("upstream initialize failed", "server", server, "err", err, "rpcErr", rpcErr)
				continue
			}
			var res mcp.InitializeResult
			if err := json.Unmarshal(result, &res); err != nil {
				a.logger.Warn("malformed upstream initialize result", "server", server, "err", err)
				continue
			}
			a.caps = MergeServerCapabilities(a.caps, res.Capabilities)
			if accepted, mismatch := NegotiateUpstreamVersion(res.ProtocolVersion); mismatch {
				a.logger.Warn("upstream reported unrecognized protocol version", "server", server, "version", accepted)
			}
			if err := t.Notify(ctx, mcp.MethodInitialized, nil); err != nil {
				a.logger.Warn("failed notifications/initialized to upstream", "server", server, "err", err)
			}
		}
	})
}

// handlePrefixedList merges a name-addressed list (tools, prompts) across
// upstreams: each item's name is rewritten to its exposed (prefixed) form, and
// for tools the profile's allow/deny filter is applied. No client cursor is
// emitted; a client-supplied cursor is rejected.
func (a *Aggregator) handlePrefixedList(ctx context.Context, env *mcp.Envelope, method, itemsKey string, toolFilter bool) *mcp.Response {
	if hasCursor(env.Params) {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "cursor pagination is not supported; GarmX returns the full list")
	}
	var items []json.RawMessage
	for _, server := range a.servers() {
		t, ok := a.mgr.Get(server)
		if !ok {
			continue
		}
		raw, err := a.drain(ctx, t, method, itemsKey)
		if err != nil {
			a.logger.Warn("skipping upstream in list", "server", server, "method", method, "err", err)
			continue
		}
		for _, item := range raw {
			exposed, rewritten, e := prefixItemName(server, item)
			if e != nil {
				a.logger.Warn("skipping item with unrewritable name", "server", server, "method", method, "err", e)
				continue
			}
			if toolFilter && !a.profile.AllowsTool(exposed) {
				continue
			}
			items = append(items, rewritten)
		}
	}
	return listResponse(env.ID, itemsKey, items)
}

// handleResourcesList merges resource lists across upstreams without prefixing
// (resources are addressed by already-namespaced uri). When recordOwners is set
// it captures uri→server ownership so resources/read can route.
func (a *Aggregator) handleResourcesList(ctx context.Context, env *mcp.Envelope, method, itemsKey string, recordOwners bool) *mcp.Response {
	if hasCursor(env.Params) {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "cursor pagination is not supported; GarmX returns the full list")
	}
	var items []json.RawMessage
	for _, server := range a.servers() {
		t, ok := a.mgr.Get(server)
		if !ok {
			continue
		}
		raw, err := a.drain(ctx, t, method, itemsKey)
		if err != nil {
			a.logger.Warn("skipping upstream in resources list", "server", server, "err", err)
			continue
		}
		for _, item := range raw {
			if recordOwners {
				if uri := itemURI(item); uri != "" {
					a.setURIOwner(uri, server)
				}
			}
			items = append(items, item)
		}
	}
	return listResponse(env.ID, itemsKey, items)
}

// handleNamedCall routes tools/call and prompts/get: split the exposed name back
// to (server, original), enforce the profile, rewrite to the original name, and
// forward to the owning upstream.
func (a *Aggregator) handleNamedCall(ctx context.Context, env *mcp.Envelope, method string, toolFilter bool) *mcp.Response {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(env.Params, &fields); err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "invalid params for "+method)
	}
	var exposed string
	if err := json.Unmarshal(fields["name"], &exposed); err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "missing or invalid name for "+method)
	}
	server, original, ok := Split(exposed)
	if !ok || !a.profile.AllowsServer(server) || (toolFilter && !a.profile.AllowsTool(exposed)) {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "unknown or filtered name: "+exposed)
	}
	t, ok := a.mgr.Get(server)
	if !ok {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "unknown server: "+server)
	}
	orig, _ := json.Marshal(original)
	fields["name"] = orig
	params, err := json.Marshal(fields)
	if err != nil {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInternalError, "re-encode params")
	}
	return forward(ctx, t, env.ID, method, params)
}

// handleResourcesRead routes by uri ownership recorded during resources/list.
// An unknown uri (never listed) is rejected rather than blindly fanned out.
func (a *Aggregator) handleResourcesRead(ctx context.Context, env *mcp.Envelope) *mcp.Response {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(env.Params, &p); err != nil || p.URI == "" {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "missing uri for resources/read")
	}
	server, ok := a.uriOwner(p.URI)
	if !ok {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInvalidParams, "unknown resource uri (list resources first): "+p.URI)
	}
	t, ok := a.mgr.Get(server)
	if !ok {
		return mcp.NewErrorResponse(env.ID, mcp.CodeInternalError, "owning server gone: "+server)
	}
	return forward(ctx, t, env.ID, mcp.MethodResourcesRead, env.Params)
}

// servers returns the in-scope server names in deterministic order.
func (a *Aggregator) servers() []string {
	var out []string
	for _, name := range a.mgr.Names() {
		if a.profile.AllowsServer(name) {
			out = append(out, name)
		}
	}
	return out
}

// drain reads all pages of a *_/list from one upstream, following nextCursor to
// exhaustion, and returns the raw items concatenated.
func (a *Aggregator) drain(ctx context.Context, t upstream.Transport, method, itemsKey string) ([]json.RawMessage, error) {
	var items []json.RawMessage
	cursor := ""
	for {
		result, rpcErr, err := t.Send(ctx, method, listParams(cursor))
		if err != nil {
			return nil, err
		}
		if rpcErr != nil {
			return nil, rpcErr
		}
		page, next, derr := decodeList(result, itemsKey)
		if derr != nil {
			return nil, derr
		}
		items = append(items, page...)
		if next == "" {
			return items, nil
		}
		cursor = next
	}
}

// setURIOwner records that server owns uri.
func (a *Aggregator) setURIOwner(uri, server string) {
	a.uriMu.Lock()
	a.uriOwners[uri] = server
	a.uriMu.Unlock()
}

// uriOwner returns the server that owns uri, if known.
func (a *Aggregator) uriOwner(uri string) (string, bool) {
	a.uriMu.Lock()
	defer a.uriMu.Unlock()
	server, ok := a.uriOwners[uri]
	return server, ok
}

// isListChanged reports whether method is one of the catalog list-changed
// notifications GarmX coalesces and forwards.
func isListChanged(method string) bool {
	return strings.HasPrefix(method, "notifications/") && strings.HasSuffix(method, "/list_changed")
}
