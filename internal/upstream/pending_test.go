package upstream

import (
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// TestPendingConcurrentCorrelation is the core correctness check for the demux:
// with many waiters registered and responses delivered in a shuffled order from
// another goroutine, each waiter must receive exactly the response bearing its
// own id. Run with -race, this catches misdelivery and data races.
func TestPendingConcurrentCorrelation(t *testing.T) {
	const n = 200
	p := newPending()

	chans := make([]chan *mcp.Response, n)
	for i := range n {
		ch, ok := p.register(strconv.Itoa(i))
		if !ok {
			t.Fatalf("register(%d) not ok", i)
		}
		chans[i] = ch
	}

	// Resolve from several goroutines in interleaved order.
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			for i := off; i < n; i += 4 {
				id := strconv.Itoa(i)
				resp := mcp.NewResponse(json.RawMessage(id), json.RawMessage(strconv.Itoa(i)))
				if !p.resolve(id, resp) {
					t.Errorf("resolve(%s) found no waiter", id)
				}
			}
		}(w)
	}
	wg.Wait()

	for i := range n {
		resp := <-chans[i]
		if string(resp.Result) != strconv.Itoa(i) {
			t.Fatalf("waiter %d got result %s, want %d", i, resp.Result, i)
		}
	}
}

// TestPendingResolveUnknown asserts resolving an id with no waiter is a safe
// no-op returning false (a late reply after cancellation).
func TestPendingResolveUnknown(t *testing.T) {
	p := newPending()
	if p.resolve("nope", mcp.NewResponse(nil, nil)) {
		t.Fatal("resolve of unknown id returned true")
	}
}

// TestPendingCancel asserts a cancelled waiter is removed and a subsequent
// resolve does not deliver.
func TestPendingCancel(t *testing.T) {
	p := newPending()
	ch, _ := p.register("1")
	p.cancel("1")
	if p.resolve("1", mcp.NewResponse(json.RawMessage(`1`), nil)) {
		t.Fatal("resolve after cancel delivered")
	}
	select {
	case <-ch:
		t.Fatal("cancelled channel received a value")
	default:
	}
}

// TestPendingCloseAll asserts every outstanding waiter receives an error
// response and that register refuses new waiters afterward.
func TestPendingCloseAll(t *testing.T) {
	p := newPending()
	ch, _ := p.register("1")
	p.closeAll(mcp.NewError(mcp.CodeInternalError, "stopped"))
	resp := <-ch
	if resp.Error == nil {
		t.Fatal("closeAll did not deliver an error response")
	}
	if _, ok := p.register("2"); ok {
		t.Fatal("register succeeded after closeAll")
	}
}
