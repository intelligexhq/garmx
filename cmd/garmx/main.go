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
	upstreamName    string
	upstreamCommand string
	upstreamArgs    stringSlice
	upstreamEnv     stringSlice
}

// serve parses flags, wires the upstream transport → aggregator → stdio
// frontend, and serves until the client disconnects or a signal arrives.
func serve(args []string) error {
	cfg, err := parseServeFlags(args)
	if err != nil {
		return err
	}
	if !cfg.stdio {
		return errors.New("only --stdio is implemented in this phase; pass --stdio")
	}
	if cfg.upstreamCommand == "" {
		return errors.New("--upstream-command is required")
	}
	if err := aggregator.ValidateServerName(cfg.upstreamName); err != nil {
		return fmt.Errorf("invalid --upstream-name: %w", err)
	}

	// Logs go to stderr: stdout is the MCP JSON-RPC wire to the client.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	up := upstream.NewStdioTransport(upstream.StdioConfig{
		Name:    cfg.upstreamName,
		Command: cfg.upstreamCommand,
		Args:    cfg.upstreamArgs,
		Env:     cfg.upstreamEnv,
	}, logger)

	agg := aggregator.New(cfg.upstreamName, version, up, logger)

	if err := up.Start(ctx); err != nil {
		return fmt.Errorf("start upstream: %w", err)
	}
	defer func() { _ = up.Stop(context.WithoutCancel(ctx)) }()

	logger.Info("garmx serving stdio", "version", version, "upstream", cfg.upstreamName)
	server := frontend.NewStdioServer(os.Stdin, os.Stdout, agg, logger)
	if err := server.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// parseServeFlags interprets serve subcommand arguments.
func parseServeFlags(args []string) (serveConfig, error) {
	fs := flag.NewFlagSet("garmx serve", flag.ContinueOnError)
	var cfg serveConfig
	fs.BoolVar(&cfg.stdio, "stdio", false, "serve the client-facing MCP endpoint over stdio")
	fs.StringVar(&cfg.upstreamName, "upstream-name", "upstream", "registered name of the upstream server (used as the tool-name prefix)")
	fs.StringVar(&cfg.upstreamCommand, "upstream-command", "", "executable of the stdio upstream MCP server")
	fs.Var(&cfg.upstreamArgs, "upstream-arg", "argument for the upstream command (repeatable)")
	fs.Var(&cfg.upstreamEnv, "upstream-env", "extra environment for the upstream as KEY=VALUE (repeatable)")
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
