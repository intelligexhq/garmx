package upstream

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// notifyFake is a minimal Transport that records only what the manager tests
// need: notification handler wiring and start/stop calls.
type notifyFake struct {
	mu       sync.Mutex
	handlers Handlers
	started  bool
	stopped  bool
}

func (f *notifyFake) Start(context.Context) error {
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
	return nil
}

func (f *notifyFake) Send(context.Context, string, json.RawMessage) (json.RawMessage, *mcp.Error, error) {
	return json.RawMessage(`{}`), nil, nil
}
func (f *notifyFake) Notify(context.Context, string, json.RawMessage) error { return nil }
func (f *notifyFake) SetHandlers(h Handlers)                                { f.mu.Lock(); f.handlers = h; f.mu.Unlock() }
func (f *notifyFake) Stop(context.Context) error {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
	return nil
}
func (f *notifyFake) Status() Status { return StatusOnline }
func (f *notifyFake) emit(n *mcp.Notification) {
	f.mu.Lock()
	h := f.handlers
	f.mu.Unlock()
	if h.OnNotification != nil {
		h.OnNotification(n)
	}
}

func testManager() *Manager {
	return NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestManagerNamesOrdered asserts Names reflects registration order.
func TestManagerNamesOrdered(t *testing.T) {
	m := testManager()
	_ = m.Add("beta", &notifyFake{})
	_ = m.Add("alpha", &notifyFake{})
	names := m.Names()
	if len(names) != 2 || names[0] != "beta" || names[1] != "alpha" {
		t.Fatalf("Names = %v, want [beta alpha]", names)
	}
}

// TestManagerDuplicateName asserts a duplicate registration is rejected.
func TestManagerDuplicateName(t *testing.T) {
	m := testManager()
	if err := m.Add("dup", &notifyFake{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("dup", &notifyFake{}); err == nil {
		t.Fatal("duplicate Add should error")
	}
}

// TestManagerTagsNotifications asserts an upstream notification reaches the
// handler tagged with its server name.
func TestManagerTagsNotifications(t *testing.T) {
	m := testManager()
	f := &notifyFake{}
	_ = m.Add("probe", f)

	got := make(chan string, 1)
	m.SetNotificationHandler(func(server string, _ *mcp.Notification) { got <- server })
	f.emit(mcp.NewNotification(mcp.NotifyToolsListChanged, nil))

	select {
	case server := <-got:
		if server != "probe" {
			t.Fatalf("tagged server = %q, want probe", server)
		}
	default:
		t.Fatal("notification not delivered")
	}
}

// TestManagerStartStopAll asserts lifecycle calls reach every transport.
func TestManagerStartStopAll(t *testing.T) {
	m := testManager()
	a, b := &notifyFake{}, &notifyFake{}
	_ = m.Add("a", a)
	_ = m.Add("b", b)
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.StopAll(context.Background())
	if !a.started || !b.started || !a.stopped || !b.stopped {
		t.Fatalf("lifecycle not propagated: a=%+v b=%+v", a, b)
	}
}
