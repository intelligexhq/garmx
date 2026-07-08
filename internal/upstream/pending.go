package upstream

import (
	"sync"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// pending correlates in-flight requests to a single upstream with their
// responses by JSON-RPC id. A transport has one read loop but may have many
// requests outstanding concurrently, so responses MUST be matched by id — never
// by "next message off the read loop", which would misdeliver under
// concurrency. The map key is the string form of the id GarmX allocated.
type pending struct {
	mu sync.Mutex
	m  map[string]chan *mcp.Response
	// closed guards against registering new waiters after the transport stops,
	// so a late Send fails fast instead of blocking forever.
	closed bool
}

// newPending constructs an empty pending map ready for use.
func newPending() *pending {
	return &pending{m: make(map[string]chan *mcp.Response)}
}

// register reserves a delivery channel for id. The channel is buffered so
// resolve never blocks on a waiter that has already given up (ctx cancelled).
// ok is false if the transport has been closed, in which case the caller must
// not wait. A duplicate id reuses no slot — ids are unique per transport.
func (p *pending) register(id string) (ch chan *mcp.Response, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, false
	}
	c := make(chan *mcp.Response, 1)
	p.m[id] = c
	return c, true
}

// resolve delivers resp to the waiter registered for id and removes the entry.
// It reports whether a waiter was found; an unmatched id (e.g. a late response
// after cancellation) is dropped by the caller.
func (p *pending) resolve(id string, resp *mcp.Response) bool {
	p.mu.Lock()
	ch, found := p.m[id]
	if found {
		delete(p.m, id)
	}
	p.mu.Unlock()
	if !found {
		return false
	}
	ch <- resp
	return true
}

// cancel discards the waiter for id without delivering a response. Called when
// the requester's context is cancelled so a later response is treated as
// unmatched rather than delivered to a gone caller.
func (p *pending) cancel(id string) {
	p.mu.Lock()
	delete(p.m, id)
	p.mu.Unlock()
}

// closeAll marks the map closed and delivers err to every outstanding waiter so
// their Send calls return promptly on transport shutdown. After closeAll,
// register refuses new waiters.
func (p *pending) closeAll(err *mcp.Error) {
	p.mu.Lock()
	waiters := p.m
	p.m = make(map[string]chan *mcp.Response)
	p.closed = true
	p.mu.Unlock()
	for _, ch := range waiters {
		ch <- &mcp.Response{JSONRPC: mcp.Version, Error: err}
	}
}
