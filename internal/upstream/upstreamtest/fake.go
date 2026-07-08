// Package upstreamtest provides a fake upstream.Transport so aggregator and
// frontend tests can drive canned upstream behaviour in-process, without
// launching a subprocess.
package upstreamtest

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/intelligexhq/garmx/internal/upstream"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// Call records one Send/Notify the aggregator made to the upstream, for
// assertions on what was forwarded (method and rewritten params).
type Call struct {
	Method string
	Params json.RawMessage
}

// Fake is a programmable in-memory upstream.Transport. Respond supplies the
// reply for each Send; Sent and Notified capture the forwarded traffic. It is
// safe for concurrent use.
type Fake struct {
	// Respond returns the reply for a Send. If nil, Send returns an empty result.
	Respond func(method string, params json.RawMessage) (json.RawMessage, *mcp.Error, error)

	mu       sync.Mutex
	handlers upstream.Handlers
	sent     []Call
	notified []Call
}

// Start is a no-op; the fake needs no process.
func (f *Fake) Start(context.Context) error { return nil }

// Send records the call and returns Respond's reply (or an empty result).
func (f *Fake) Send(_ context.Context, method string, params json.RawMessage) (json.RawMessage, *mcp.Error, error) {
	f.mu.Lock()
	f.sent = append(f.sent, Call{Method: method, Params: params})
	f.mu.Unlock()
	if f.Respond == nil {
		return json.RawMessage(`{}`), nil, nil
	}
	return f.Respond(method, params)
}

// Notify records a fire-and-forget notification to the upstream.
func (f *Fake) Notify(_ context.Context, method string, params json.RawMessage) error {
	f.mu.Lock()
	f.notified = append(f.notified, Call{Method: method, Params: params})
	f.mu.Unlock()
	return nil
}

// SetHandlers stores the upstream→client callbacks.
func (f *Fake) SetHandlers(h upstream.Handlers) {
	f.mu.Lock()
	f.handlers = h
	f.mu.Unlock()
}

// Stop is a no-op.
func (f *Fake) Stop(context.Context) error { return nil }

// Status always reports online.
func (f *Fake) Status() upstream.Status { return upstream.StatusOnline }

// Emit delivers an upstream notification to the registered handler, simulating
// an upstream-initiated message such as tools/list_changed.
func (f *Fake) Emit(n *mcp.Notification) {
	f.mu.Lock()
	h := f.handlers
	f.mu.Unlock()
	if h.OnNotification != nil {
		h.OnNotification(n)
	}
}

// Sent returns a copy of the requests forwarded to the upstream.
func (f *Fake) Sent() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.sent...)
}

// Notified returns a copy of the notifications forwarded to the upstream.
func (f *Fake) Notified() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.notified...)
}
