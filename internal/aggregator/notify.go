package aggregator

import (
	"sync"
	"time"
)

// notifyDebounce is the coalescing window for upstream list-changed
// notifications. Captured clients re-fetch within milliseconds of a
// notification, so a short window collapses an upstream restart's burst of
// list_changed events into a single client-facing emit without adding
// noticeable latency.
const notifyDebounce = 150 * time.Millisecond

// notifier coalesces list-changed notifications by method over a fixed window
// and emits each distinct method once. The window is fixed from the first event
// (not reset on each), so continuous upstream churn cannot postpone delivery
// indefinitely.
type notifier struct {
	debounce time.Duration
	emit     func(method string)

	mu      sync.Mutex
	pending map[string]struct{}
	timer   *time.Timer
}

// newNotifier builds a notifier that calls emit(method) once per coalescing
// window for each distinct scheduled method.
func newNotifier(debounce time.Duration, emit func(method string)) *notifier {
	return &notifier{debounce: debounce, emit: emit, pending: map[string]struct{}{}}
}

// schedule queues method for emission. The first queued method opens the
// coalescing window; subsequent methods within it are merged.
func (n *notifier) schedule(method string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.pending[method] = struct{}{}
	if n.timer == nil {
		n.timer = time.AfterFunc(n.debounce, n.flush)
	}
}

// flush emits every queued method once and closes the window.
func (n *notifier) flush() {
	n.mu.Lock()
	methods := make([]string, 0, len(n.pending))
	for m := range n.pending {
		methods = append(methods, m)
	}
	n.pending = map[string]struct{}{}
	n.timer = nil
	n.mu.Unlock()

	for _, m := range methods {
		n.emit(m)
	}
}
