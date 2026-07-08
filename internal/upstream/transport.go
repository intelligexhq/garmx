package upstream

import (
	"context"
	"encoding/json"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// Status is a coarse upstream liveness state surfaced to the UI and health
// checks.
type Status string

// Upstream status values.
const (
	// StatusUnknown means the transport has not yet been started or probed.
	StatusUnknown Status = "unknown"
	// StatusOnline means the upstream is running and responsive.
	StatusOnline Status = "online"
	// StatusOffline means the upstream process/connection is gone.
	StatusOffline Status = "offline"
)

// Handlers carries the callbacks a transport invokes for messages that are not
// responses to a GarmX request: upstream-initiated notifications, and
// upstream→client requests (server→client calls). Both are optional; a nil
// handler means "drop" for notifications and "reply method-not-found" for
// requests (the v1 deferred-callback behaviour).
type Handlers struct {
	// OnNotification handles an upstream notification (e.g.
	// notifications/tools/list_changed). Must not block the read loop.
	OnNotification func(*mcp.Notification)
}

// Transport is the upstream-facing side of one registered MCP server. GarmX is
// the client on this side: it owns the request id space (Send allocates ids and
// correlates replies), and it treats an inbound message with an id it did not
// send as a server→client request.
//
// Implementations must be safe for concurrent Send/Notify calls; the pending
// demux (matched by id) is what keeps concurrent in-flight requests correct.
type Transport interface {
	// Start launches the transport and its read loop. It returns once the
	// upstream is ready to accept Send/Notify, or an error if it cannot start.
	Start(ctx context.Context) error

	// Send issues a request and blocks until the correlated response arrives,
	// ctx is cancelled, or the transport stops. It returns the raw result, a
	// non-nil rpcErr if the upstream replied with a JSON-RPC error, or a
	// transport-level err. Exactly one of (result/rpcErr) or err is meaningful.
	Send(ctx context.Context, method string, params json.RawMessage) (result json.RawMessage, rpcErr *mcp.Error, err error)

	// Notify sends a fire-and-forget notification to the upstream (no reply
	// expected), e.g. notifications/initialized.
	Notify(ctx context.Context, method string, params json.RawMessage) error

	// SetHandlers registers callbacks for upstream-initiated traffic. It must be
	// called before Start so no message is missed.
	SetHandlers(h Handlers)

	// Stop terminates the transport, failing any outstanding Send calls. It is
	// idempotent.
	Stop(ctx context.Context) error

	// Status reports the current liveness of the upstream.
	Status() Status
}
