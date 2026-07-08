package aggregator

import (
	"sync"
	"testing"
	"time"
)

// TestNotifierCoalesces asserts that repeated schedules of the same method
// within the window produce a single emit, and distinct methods each emit once.
func TestNotifierCoalesces(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	n := newNotifier(30*time.Millisecond, func(method string) {
		mu.Lock()
		counts[method]++
		mu.Unlock()
	})

	n.schedule(mcpToolsListChanged)
	n.schedule(mcpToolsListChanged)
	n.schedule(mcpPromptsListChanged)

	time.Sleep(90 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if counts[mcpToolsListChanged] != 1 {
		t.Fatalf("tools list_changed emitted %d times, want 1", counts[mcpToolsListChanged])
	}
	if counts[mcpPromptsListChanged] != 1 {
		t.Fatalf("prompts list_changed emitted %d times, want 1", counts[mcpPromptsListChanged])
	}
}

// Local aliases keep the test readable without importing pkg/mcp just for two
// notification method strings.
const (
	mcpToolsListChanged   = "notifications/tools/list_changed"
	mcpPromptsListChanged = "notifications/prompts/list_changed"
)
