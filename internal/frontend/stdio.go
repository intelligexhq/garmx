package frontend

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/intelligexhq/garmx/internal/aggregator"
	"github.com/intelligexhq/garmx/pkg/mcp"
)

// StdioServer presents GarmX to a client as an ordinary stdio MCP server: it
// reads newline-delimited JSON-RPC from in and writes responses to out, handing
// each message to the aggregator. Writes are serialized so aggregator responses
// (from the read loop) and pushed notifications (from the upstream, via a
// different goroutine) never interleave on out.
type StdioServer struct {
	in     io.Reader
	out    io.Writer
	agg    *aggregator.Aggregator
	logger *slog.Logger

	writeMu sync.Mutex
}

// NewStdioServer wires a client-facing stdio server over in/out backed by agg.
// logger must write to stderr; out is reserved for the MCP wire.
func NewStdioServer(in io.Reader, out io.Writer, agg *aggregator.Aggregator, logger *slog.Logger) *StdioServer {
	return &StdioServer{in: in, out: out, agg: agg, logger: logger.With("component", "frontend/stdio")}
}

// Serve reads and dispatches messages until in reaches EOF (the client
// disconnected) or ctx is cancelled. It registers the notification pusher so
// upstream notifications reach the client, and returns nil on a clean EOF.
func (s *StdioServer) Serve(ctx context.Context) error {
	s.agg.SetClientNotifier(s.pushNotification)

	// Run the blocking read loop on its own goroutine so ctx cancellation
	// (shutdown) can return promptly even though a stdin Read is in flight.
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		r := bufio.NewReaderSize(s.in, 64*1024)
		for {
			line, err := mcp.ReadMessage(r)
			if len(line) > 0 {
				// Copy: ReadMessage may reuse the underlying buffer.
				cp := make([]byte, len(line))
				copy(cp, line)
				lines <- cp
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line := <-lines:
			s.dispatch(ctx, line)
		case err := <-readErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// dispatch classifies one inbound line and routes it: a request is handled and
// its response written; a notification is handed to the aggregator; an
// unparseable line yields a JSON-RPC parse error with a null id.
func (s *StdioServer) dispatch(ctx context.Context, line []byte) {
	env, err := mcp.Parse(line)
	if err != nil {
		s.writeMessage(mcp.NewErrorResponse(nil, mcp.CodeParseError, "parse error"))
		return
	}
	switch {
	case env.IsRequest():
		resp := s.agg.Handle(ctx, env)
		s.writeMessage(resp)
	case env.IsNotification():
		s.agg.HandleNotification(ctx, env)
	default:
		// A response from the client would only answer a server→client request,
		// which GarmX does not issue in v1. Ignore it.
		s.logger.Debug("ignoring unexpected client message", "method", env.Method)
	}
}

// pushNotification writes a server→client notification to out. It is invoked
// from the upstream read-loop goroutine, so it shares the write mutex with
// response writes.
func (s *StdioServer) pushNotification(n *mcp.Notification) {
	s.writeMessage(n)
}

// writeMessage serializes v and writes it as one framed line under the write
// mutex. A write failure means the client is gone; it is logged, not fatal.
func (s *StdioServer) writeMessage(v any) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := mcp.WriteMessage(s.out, v); err != nil {
		s.logger.Debug("failed writing to client", "err", err)
	}
}
