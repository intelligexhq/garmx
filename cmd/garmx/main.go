// Command garmx is the entrypoint for the GarmX MCP aggregating gateway.
//
// It runs as a single binary that presents itself to an AI client as one MCP
// server while fanning requests out to many registered upstream MCP servers.
// This skeleton parses flags and logs startup; the aggregator, transports, and
// web UI are not yet wired.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

// config holds the parsed command-line configuration for the daemon.
type config struct {
	// addr is the address the management HTTP server and Streamable HTTP MCP
	// endpoint bind to. It defaults to loopback because the daemon holds every
	// upstream server's credentials and must not be exposed by default.
	addr string
	// stdio enables the client-facing stdio MCP endpoint, used by clients such
	// as Claude Code and OpenCode that launch garmx as a subprocess.
	stdio bool
}

// main parses flags, constructs a logger, and starts the daemon. It is kept
// deliberately thin: all real logic lives in internal packages.
func main() {
	err := run(os.Args[1:])
	// A -h/-help request prints usage and is a successful, quiet exit.
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		slog.Error("garmx exited with error", "err", err)
		os.Exit(1)
	}
}

// run wires up configuration and starts the daemon. It is separated from main
// so it can return an error (and one day be exercised by tests) rather than
// calling os.Exit directly.
func run(args []string) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("garmx starting",
		"version", version,
		"addr", cfg.addr,
		"stdio", cfg.stdio,
	)

	// Nothing to serve yet: the aggregator, upstream transports, and
	// HTTP/stdio frontends are wired in here as they are implemented.
	logger.Info("garmx scaffold ready (no services wired yet)")
	return nil
}

// parseFlags interprets the command-line arguments into a config. It returns a
// usage error rather than exiting so callers control process termination.
func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("garmx", flag.ContinueOnError)
	var cfg config
	fs.StringVar(&cfg.addr, "addr", "127.0.0.1:9735", "address for the web UI and Streamable HTTP MCP endpoint")
	fs.BoolVar(&cfg.stdio, "stdio", false, "serve the client-facing MCP endpoint over stdio")
	fs.Usage = func() {
		// Write errors to the usage stream (stderr) are not actionable here.
		_, _ = fmt.Fprintf(fs.Output(), "garmx %s — local MCP aggregating gateway\n\nUsage:\n  garmx [flags]\n\nFlags:\n", version)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return cfg, nil
}
