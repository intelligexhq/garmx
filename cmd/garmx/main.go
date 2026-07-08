// Command garmx is the entrypoint for the GarmX MCP aggregating gateway.
//
// It runs as a single binary that presents itself to an AI client as one MCP
// server while fanning requests out to registered upstream MCP servers.
// `garmx serve --stdio` fronts one or more stdio upstreams and audits every
// routed call to a shared SQLite database; `garmx ui` opens that database
// read-only and serves a minimal dashboard on :9735. The daemon/shim split and
// HTTP faces arrive in later phases.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/intelligexhq/garmx/internal/aggregator"
	"github.com/intelligexhq/garmx/internal/api"
	"github.com/intelligexhq/garmx/internal/audit"
	"github.com/intelligexhq/garmx/internal/config"
	"github.com/intelligexhq/garmx/internal/frontend"
	"github.com/intelligexhq/garmx/internal/upstream"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// main dispatches the subcommand and translates errors into an exit code. A
// -h/-help request is a quiet, successful exit.
func main() {
	err := run(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		slog.Error("garmx exited with error", "err", err)
		os.Exit(1)
	}
}

// run selects a subcommand. Only `serve` exists today; anything else prints
// usage. It is separated from main so it can return errors instead of exiting.
func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "ui":
		return runUI(args[1:])
	case "-h", "-help", "--help":
		usage()
		return flag.ErrHelp
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// usage prints the top-level command summary to stderr.
func usage() {
	// Usage output goes to stderr; a write error there is not actionable.
	_, _ = fmt.Fprintf(os.Stderr, "garmx %s — local MCP aggregating gateway\n\nUsage:\n  garmx serve --stdio --upstream-command <cmd> [flags]\n  garmx ui [--addr 127.0.0.1:9735] [--audit-db <path>]\n\nRun `garmx serve -h` or `garmx ui -h` for flags.\n", version)
}

// serveConfig is the parsed configuration for the serve subcommand.
type serveConfig struct {
	stdio           bool
	configPath      string
	profile         string
	upstreamName    string
	upstreamCommand string
	upstreamArgs    stringSlice
	upstreamEnv     stringSlice

	auditDB      string
	auditPayload string
	auditScope   string
	noAudit      bool
}

// serve parses flags, builds the upstream set (from --config or the
// single-upstream flags), wires manager → aggregator → stdio frontend with the
// audit sink attached, and serves until the client disconnects or a signal
// arrives.
func serve(args []string) error {
	cfg, err := parseServeFlags(args)
	if err != nil {
		return err
	}
	if !cfg.stdio {
		return errors.New("only --stdio is implemented in this phase; pass --stdio")
	}

	// Logs go to stderr: stdout is the MCP JSON-RPC wire to the client.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var fileConf *config.Config
	if cfg.configPath != "" {
		fileConf, err = config.Load(cfg.configPath)
		if err != nil {
			return err
		}
	}

	mgr := upstream.NewManager(logger)
	profile, err := buildUpstreams(mgr, cfg, fileConf, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	agg := aggregator.New(mgr, profile, version, logger)

	closeAudit, err := attachAudit(agg, cfg, fileConf, logger)
	if err != nil {
		return err
	}
	defer closeAudit()

	if err := mgr.StartAll(ctx); err != nil {
		return fmt.Errorf("start upstreams: %w", err)
	}
	defer mgr.StopAll(context.WithoutCancel(ctx))

	logger.Info("garmx serving stdio", "version", version, "upstreams", mgr.Names(), "profile", cfg.profile)
	server := frontend.NewStdioServer(os.Stdin, os.Stdout, agg, logger)
	if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// attachAudit resolves the audit configuration (defaults ← config file ←
// flags/env) and, when enabled, opens the shared audit database and attaches a
// writer to the aggregator. A failure to open the database is non-fatal: the
// gateway must not depend on its own audit trail, so it warns and runs without
// auditing. The returned func flushes and closes the writer on shutdown.
func attachAudit(agg *aggregator.Aggregator, cfg serveConfig, fileConf *config.Config, logger *slog.Logger) (func(), error) {
	var fileAudit *config.AuditConfig
	if fileConf != nil {
		fileAudit = fileConf.Audit
	}
	resolved, err := config.ResolveAudit(fileAudit, config.AuditOverride{
		DBPath:  firstNonEmpty(cfg.auditDB, os.Getenv("GARMX_AUDIT_DB")),
		Payload: cfg.auditPayload,
		Scope:   cfg.auditScope,
		Disable: cfg.noAudit,
	})
	if err != nil {
		return nil, err
	}
	noop := func() {}
	if !resolved.Enabled {
		logger.Info("audit disabled")
		return noop, nil
	}
	store, err := audit.OpenWriter(resolved.DBPath)
	if err != nil {
		logger.Warn("audit disabled: cannot open database", "path", resolved.DBPath, "err", err)
		return noop, nil
	}
	writer := audit.NewWriter(store, audit.Options{
		Payload:         resolved.Payload,
		MaxPayloadBytes: resolved.MaxPayloadBytes,
		RedactKeys:      resolved.RedactKeys,
	}, logger)
	sessionID := newSessionID()
	agg.SetAudit(writer, sessionID, resolved.Scope == config.ScopeAll)
	logger.Info("audit enabled", "db", resolved.DBPath, "payload", resolved.Payload, "scope", resolved.Scope, "session", sessionID)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := writer.Close(ctx); err != nil {
			logger.Warn("audit close", "err", err)
		}
	}, nil
}

// buildUpstreams registers upstreams on the manager from either a loaded config
// or the single-upstream flags, and resolves the requested profile. A config
// file takes precedence when both are supplied.
func buildUpstreams(mgr *upstream.Manager, cfg serveConfig, fileConf *config.Config, logger *slog.Logger) (aggregator.Profile, error) {
	if fileConf != nil {
		return buildFromConfig(mgr, cfg, fileConf, logger)
	}
	if cfg.upstreamCommand == "" {
		return aggregator.Profile{}, errors.New("provide --config or --upstream-command")
	}
	if cfg.profile != "" {
		return aggregator.Profile{}, errors.New("--profile requires --config (profiles are declared there)")
	}
	if err := aggregator.ValidateServerName(cfg.upstreamName); err != nil {
		return aggregator.Profile{}, fmt.Errorf("invalid --upstream-name: %w", err)
	}
	t := upstream.NewStdioTransport(upstream.StdioConfig{
		Name:    cfg.upstreamName,
		Command: cfg.upstreamCommand,
		Args:    cfg.upstreamArgs,
		Env:     cfg.upstreamEnv,
	}, logger)
	if err := mgr.Add(cfg.upstreamName, t); err != nil {
		return aggregator.Profile{}, err
	}
	return aggregator.Profile{}, nil
}

// buildFromConfig registers every server from the loaded config and resolves the
// named profile (empty --profile means expose everything).
func buildFromConfig(mgr *upstream.Manager, cfg serveConfig, conf *config.Config, logger *slog.Logger) (aggregator.Profile, error) {
	for _, s := range conf.Servers {
		if err := aggregator.ValidateServerName(s.Name); err != nil {
			return aggregator.Profile{}, fmt.Errorf("server %q: %w", s.Name, err)
		}
		t := upstream.NewStdioTransport(upstream.StdioConfig{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
			Env:     s.EnvSlice(),
		}, logger)
		if err := mgr.Add(s.Name, t); err != nil {
			return aggregator.Profile{}, err
		}
	}
	if cfg.profile == "" {
		return aggregator.Profile{}, nil
	}
	p, ok := conf.FindProfile(cfg.profile)
	if !ok {
		return aggregator.Profile{}, fmt.Errorf("profile %q not found in config", cfg.profile)
	}
	return aggregator.Profile{Servers: p.Servers, ToolAllow: p.ToolAllow, ToolDeny: p.ToolDeny}, nil
}

// parseServeFlags interprets serve subcommand arguments.
func parseServeFlags(args []string) (serveConfig, error) {
	fs := flag.NewFlagSet("garmx serve", flag.ContinueOnError)
	var cfg serveConfig
	fs.BoolVar(&cfg.stdio, "stdio", false, "serve the client-facing MCP endpoint over stdio")
	fs.StringVar(&cfg.configPath, "config", "", "path to a JSON config declaring servers and profiles")
	fs.StringVar(&cfg.profile, "profile", "", "name of a profile from the config to scope this session")
	fs.StringVar(&cfg.upstreamName, "upstream-name", "upstream", "single-upstream mode: registered name (tool-name prefix)")
	fs.StringVar(&cfg.upstreamCommand, "upstream-command", "", "single-upstream mode: executable of the stdio upstream")
	fs.Var(&cfg.upstreamArgs, "upstream-arg", "single-upstream mode: argument for the upstream command (repeatable)")
	fs.Var(&cfg.upstreamEnv, "upstream-env", "single-upstream mode: extra env as KEY=VALUE (repeatable)")
	fs.StringVar(&cfg.auditDB, "audit-db", "", "audit database path (overrides config/GARMX_AUDIT_DB; default XDG data dir)")
	fs.StringVar(&cfg.auditPayload, "audit-payload", "", "audit capture: request-response | request | metadata")
	fs.StringVar(&cfg.auditScope, "audit-scope", "", "audit scope: calls | all")
	fs.BoolVar(&cfg.noAudit, "no-audit", false, "disable audit logging for this session")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "garmx serve — run the client-facing MCP endpoint\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return serveConfig{}, err
	}
	return cfg, nil
}

// uiConfig is the parsed configuration for the ui subcommand.
type uiConfig struct {
	addr       string
	auditDB    string
	configPath string
}

// runUI opens the shared audit database read-only and serves the minimal
// dashboard. It resolves the same audit path serve uses (flag/env → config →
// default) so the reader and writers agree on the file. Binds 127.0.0.1 by
// default: the process holds no secrets, but the audit trail may include tool
// arguments, so it is never exposed off-host without an explicit address.
func runUI(args []string) error {
	cfg, err := parseUIFlags(args)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var fileAudit *config.AuditConfig
	if cfg.configPath != "" {
		conf, err := config.Load(cfg.configPath)
		if err != nil {
			return err
		}
		fileAudit = conf.Audit
	}
	resolved, err := config.ResolveAudit(fileAudit, config.AuditOverride{
		DBPath: firstNonEmpty(cfg.auditDB, os.Getenv("GARMX_AUDIT_DB")),
	})
	if err != nil {
		return err
	}

	store, err := audit.OpenReader(resolved.DBPath)
	if err != nil {
		return fmt.Errorf("open audit db %q: %w", resolved.DBPath, err)
	}
	defer func() { _ = store.Close() }()

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           api.NewServer(store, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Info("garmx ui serving", "addr", cfg.addr, "db", resolved.DBPath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("ui listen: %w", err)
	}
	return nil
}

// parseUIFlags interprets ui subcommand arguments.
func parseUIFlags(args []string) (uiConfig, error) {
	fs := flag.NewFlagSet("garmx ui", flag.ContinueOnError)
	var cfg uiConfig
	fs.StringVar(&cfg.addr, "addr", "127.0.0.1:9735", "address to serve the read-only dashboard on")
	fs.StringVar(&cfg.auditDB, "audit-db", "", "audit database path (overrides config/GARMX_AUDIT_DB; default XDG data dir)")
	fs.StringVar(&cfg.configPath, "config", "", "config file to read the audit block (dbPath) from")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "garmx ui — serve the read-only audit dashboard\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return uiConfig{}, err
	}
	return cfg, nil
}

// newSessionID returns a random hex session identifier, unique per process so
// the UI can group a session's transactions (option A: one stdio process is one
// session). crypto/rand avoids a UUID dependency.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read never fails on supported platforms; fall back to a marker.
		return "session-unknown"
	}
	return hex.EncodeToString(b[:])
}

// firstNonEmpty returns the first non-empty argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// stringSlice is a flag.Value that accumulates repeated string flags.
type stringSlice []string

// String renders the accumulated values (for flag help output).
func (s *stringSlice) String() string { return strings.Join(*s, ",") }

// Set appends one occurrence of the flag.
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}
