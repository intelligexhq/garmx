package upstream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/intelligexhq/garmx/pkg/mcp"
)

// stopGrace is how long Stop waits after SIGTERM before escalating to SIGKILL.
const stopGrace = 5 * time.Second

// StdioConfig describes a stdio (subprocess) upstream.
type StdioConfig struct {
	// Name is the registered server name used for logging and prefixing.
	Name string
	// Command is the executable to launch.
	Command string
	// Args are the process arguments (argv[1:]).
	Args []string
	// Env is extra environment (KEY=VALUE) merged onto the daemon's own env.
	Env []string
}

// StdioTransport runs one MCP server as a child process and speaks
// newline-delimited JSON-RPC over its stdin/stdout. It owns the request id
// space and correlates responses through a pending demux, so concurrent Send
// calls are safe. stderr is drained to the logger to keep the child unblocked.
type StdioTransport struct {
	cfg    StdioConfig
	logger *slog.Logger

	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex // serializes writes to the child's stdin
	pend    *pending
	nextID  atomic.Int64

	handlers Handlers

	status   atomic.Value // Status
	reaped   chan struct{}
	downOnce sync.Once
	stopOnce sync.Once
}

// NewStdioTransport constructs a stdio transport for cfg. Start must be called
// to launch the process. logger must be non-nil; it should write to stderr
// because stdout is reserved for the MCP wire on the client-facing side.
func NewStdioTransport(cfg StdioConfig, logger *slog.Logger) *StdioTransport {
	t := &StdioTransport{
		cfg:    cfg,
		logger: logger.With("upstream", cfg.Name),
		pend:   newPending(),
		reaped: make(chan struct{}),
	}
	t.status.Store(StatusUnknown)
	return t
}

// SetHandlers registers upstream-initiated callbacks; call before Start.
func (t *StdioTransport) SetHandlers(h Handlers) { t.handlers = h }

// Status returns the current liveness state.
func (t *StdioTransport) Status() Status { return t.status.Load().(Status) }

// Start launches the child process, wiring stdin/stdout/stderr, and spawns the
// read, stderr-drain, and reaper goroutines. The child is put in its own
// process group so Stop can signal the whole group and a daemon crash does not
// orphan grandchildren.
func (t *StdioTransport) Start(_ context.Context) error {
	// Intentionally not exec.CommandContext: process lifetime is governed by
	// Stop, not by the caller's ctx (which typically outlives a single request).
	cmd := exec.Command(t.cfg.Command, t.cfg.Args...) //nolint:gosec // command is operator-registered by design
	cmd.Env = append(cmd.Environ(), t.cfg.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %q: %w", t.cfg.Command, err)
	}
	t.cmd = cmd
	t.stdin = stdin
	t.status.Store(StatusOnline)

	go t.readLoop(stdout)
	go t.drainStderr(stderr)
	go t.reap()
	return nil
}

// Send issues a request and blocks for the correlated response. It allocates a
// fresh id, registers a waiter, writes the framed request, then waits on the
// waiter or ctx. A ctx cancellation removes the waiter so a late reply is
// treated as unmatched.
func (t *StdioTransport) Send(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *mcp.Error, error) {
	id := strconv.FormatInt(t.nextID.Add(1), 10)
	ch, ok := t.pend.register(id)
	if !ok {
		return nil, nil, errors.New("upstream stopped")
	}

	req := mcp.Request{JSONRPC: mcp.Version, ID: json.RawMessage(id), Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		t.pend.cancel(id)
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}
	if err := t.writeLine(line); err != nil {
		t.pend.cancel(id)
		return nil, nil, fmt.Errorf("write request: %w", err)
	}

	select {
	case <-ctx.Done():
		t.pend.cancel(id)
		return nil, nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error, nil
		}
		return resp.Result, nil, nil
	}
}

// Notify sends a fire-and-forget notification to the upstream.
func (t *StdioTransport) Notify(_ context.Context, method string, params json.RawMessage) error {
	n := mcp.Notification{JSONRPC: mcp.Version, Method: method, Params: params}
	line, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	return t.writeLine(line)
}

// writeLine writes one framed JSON-RPC message (payload + '\n') to the child's
// stdin under the write mutex so concurrent Send/Notify calls never interleave.
func (t *StdioTransport) writeLine(payload []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if _, err := t.stdin.Write(payload); err != nil {
		return err
	}
	_, err := t.stdin.Write([]byte{'\n'})
	return err
}

// readLoop consumes newline-delimited messages from the child's stdout and
// dispatches each: a response is delivered to its pending waiter by id; a
// notification goes to the handler; an upstream→client request (an id GarmX did
// not send) is answered with method-not-found (server→client calls are deferred
// in v1). It exits on EOF/read error, which triggers teardown.
func (t *StdioTransport) readLoop(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, err := mcp.ReadMessage(r)
		if len(line) > 0 {
			t.dispatch(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.logger.Warn("upstream read error", "err", err)
			}
			t.markDown()
			return
		}
	}
}

// dispatch routes a single inbound line. Parse failures are logged and skipped
// rather than tearing down the transport, since a malformed line should not
// kill an otherwise healthy upstream.
func (t *StdioTransport) dispatch(line []byte) {
	env, err := mcp.Parse(line)
	if err != nil {
		t.logger.Warn("upstream sent unparseable line", "err", err)
		return
	}
	switch {
	case env.IsResponse():
		resp := &mcp.Response{JSONRPC: env.JSONRPC, ID: env.ID, Result: env.Result, Error: env.Error}
		if !t.pend.resolve(string(env.ID), resp) {
			t.logger.Debug("dropping response with no waiter", "id", string(env.ID))
		}
	case env.IsNotification():
		if t.handlers.OnNotification != nil {
			t.handlers.OnNotification(mcp.NewNotification(env.Method, env.Params))
		}
	case env.IsRequest():
		// Server→client request: deferred in v1 — reply method-not-found.
		resp := mcp.NewErrorResponse(env.ID, mcp.CodeMethodNotFound, "server-to-client requests are not supported")
		if line, err := json.Marshal(resp); err == nil {
			_ = t.writeLine(line)
		}
	default:
		t.logger.Warn("upstream sent unclassifiable message")
	}
}

// drainStderr forwards the child's stderr to the logger. A full stderr pipe
// would otherwise block the child, so this must run for the child's lifetime.
func (t *StdioTransport) drainStderr(stderr io.Reader) {
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64*1024), mcp.MaxLineBytes)
	for sc.Scan() {
		t.logger.Debug("upstream stderr", "line", sc.Text())
	}
}

// reap waits for the child to exit and marks the transport down, closing the
// reaped channel so Stop can tell when escalation is unnecessary.
func (t *StdioTransport) reap() {
	_ = t.cmd.Wait()
	close(t.reaped)
	t.markDown()
}

// markDown transitions to offline and fails any outstanding Send calls exactly
// once, whether triggered by EOF, process exit, or Stop.
func (t *StdioTransport) markDown() {
	t.downOnce.Do(func() {
		t.status.Store(StatusOffline)
		t.pend.closeAll(mcp.NewError(mcp.CodeInternalError, "upstream stopped"))
	})
}

// Stop terminates the child: SIGTERM to the process group, wait up to
// stopGrace, then SIGKILL. It is idempotent and safe to call after the child
// has already exited.
func (t *StdioTransport) Stop(_ context.Context) error {
	t.stopOnce.Do(func() {
		if t.cmd == nil || t.cmd.Process == nil {
			t.markDown()
			return
		}
		pgid := t.cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case <-t.reaped:
		case <-time.After(stopGrace):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-t.reaped
		}
		_ = t.stdin.Close()
	})
	t.markDown()
	return nil
}
