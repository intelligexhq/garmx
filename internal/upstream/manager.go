package upstream

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// Manager owns the set of registered upstream transports and their lifecycle.
// It is the single place that knows every upstream, so the aggregator can fan
// out over Names/Get without holding transports itself. Notifications are
// tagged with their originating server name before reaching the aggregator, so
// the aggregator can decide per-server (e.g. profile scoping) what to forward.
//
// Restart-on-crash with backoff is intentionally not here yet (a later phase);
// this Manager handles start, stop, lookup, and notification routing.
type Manager struct {
	logger *slog.Logger

	mu         sync.Mutex
	transports map[string]Transport
	order      []string // registration order, for deterministic fan-out
	notify     func(server string, n *mcp.Notification)
}

// NewManager constructs an empty Manager. logger must be non-nil.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{logger: logger.With("component", "upstream/manager"), transports: map[string]Transport{}}
}

// Add registers a transport under name. It is an error to add a duplicate name.
// If a notification handler is already set, it is wired to the transport now, so
// Add must occur before StartAll (matching the Transport SetHandlers contract).
func (m *Manager) Add(name string, t Transport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.transports[name]; exists {
		return fmt.Errorf("duplicate upstream name %q", name)
	}
	m.transports[name] = t
	m.order = append(m.order, name)
	m.wire(name, t)
	return nil
}

// SetNotificationHandler registers the callback for upstream notifications,
// tagged with the originating server name. It (re)wires all registered
// transports, so it may be called after Add but must be called before StartAll.
func (m *Manager) SetNotificationHandler(fn func(server string, n *mcp.Notification)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notify = fn
	for name, t := range m.transports {
		m.wire(name, t)
	}
}

// wire connects one transport's notification handler to the manager's tagged
// callback. Caller holds m.mu.
func (m *Manager) wire(name string, t Transport) {
	if m.notify == nil {
		return
	}
	server := name // capture per transport
	t.SetHandlers(Handlers{OnNotification: func(n *mcp.Notification) {
		m.notify(server, n)
	}})
}

// Get returns the transport registered as name.
func (m *Manager) Get(name string) (Transport, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.transports[name]
	return t, ok
}

// Names returns the registered server names in registration order, so fan-out
// and merged lists are deterministic.
func (m *Manager) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.order...)
}

// StartAll starts every registered transport. On the first failure it stops the
// transports already started and returns the error, so a partial start never
// leaks running children.
func (m *Manager) StartAll(ctx context.Context) error {
	for _, name := range m.Names() {
		t, _ := m.Get(name)
		if err := t.Start(ctx); err != nil {
			m.stopStarted(ctx, name)
			return fmt.Errorf("start upstream %q: %w", name, err)
		}
	}
	return nil
}

// stopStarted stops every transport registered before failedName (i.e. the ones
// already started) during a failed StartAll.
func (m *Manager) stopStarted(ctx context.Context, failedName string) {
	for _, name := range m.Names() {
		if name == failedName {
			return
		}
		if t, ok := m.Get(name); ok {
			_ = t.Stop(ctx)
		}
	}
}

// StopAll stops every registered transport, aggregating nothing: shutdown is
// best-effort and each transport's Stop is idempotent.
func (m *Manager) StopAll(ctx context.Context) {
	for _, name := range m.Names() {
		if t, ok := m.Get(name); ok {
			_ = t.Stop(ctx)
		}
	}
}
