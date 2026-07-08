// Command garmx is the entrypoint for the GarmX MCP aggregating gateway.
//
// It runs as a single binary that presents itself to an AI client as one MCP
// server while fanning requests out to registered upstream MCP servers. In this
// phase `garmx serve --stdio` fronts a single stdio upstream over a full
// initialize → tools/list → tools/call round-trip; the daemon/shim split, HTTP
// faces, persistence, and UI are wired in later phases.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/intelligexhq/garmx/internal/aggregator"
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
	_, _ = fmt.Fprintf(os.Stderr, "garmx %s — local MCP aggregating gateway\n\nUsage:\n  garmx serve --stdio --upstream-command <cmd> [flags]\n\nRun `garmx serve -h` for flags.\n", version)
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
}

// serve parses flags, builds the upstream set (from --config or the
// single-upstream flags), wires manager → aggregator → stdio frontend, and
// serves until the client disconnects or a signal arrives.
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

	mgr := upstream.NewManager(logger)
	profile, err := buildUpstreams(mgr, cfg, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	agg := aggregator.New(mgr, profile, version, logger)

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

// buildUpstreams registers upstreams on the manager from either a config file
// or the single-upstream flags, and resolves the requested profile. A config
// file takes precedence when both are supplied.
func buildUpstreams(mgr *upstream.Manager, cfg serveConfig, logger *slog.Logger) (aggregator.Profile, error) {
	if cfg.configPath != "" {
		return buildFromConfig(mgr, cfg, logger)
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

// buildFromConfig registers every server from the config file and resolves the
// named profile (empty --profile means expose everything).
func buildFromConfig(mgr *upstream.Manager, cfg serveConfig, logger *slog.Logger) (aggregator.Profile, error) {
	conf, err := config.Load(cfg.configPath)
	if err != nil {
		return aggregator.Profile{}, err
	}
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
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "garmx serve — run the client-facing MCP endpoint\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return serveConfig{}, err
	}
	return cfg, nil
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
