// Command mcm is the Mosquitto Control Manager.
//
// After the docker-first pivot (issue #226) the binary has no subcommands:
// it always starts the HTTP server. Configuration is loaded from the path
// passed via --config (default ./mcm.yaml) and from MCM_* environment
// variables (see internal/config).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/server"
)

// defaultConfigPath is empty so the binary starts without a YAML file:
// Load("") skips the YAML step and resolves config purely from MCM_*
// environment variables + Default(). Compose deployments that want a
// starter YAML can pass --config explicitly or set MCM_CONFIG_FILE.
const defaultConfigPath = ""

// Build info. Set via -ldflags at build time (see Dockerfile). Defaults are
// the literal strings shown so a plain `go build` produces a usable banner
// instead of empty fields.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", defaultConfigPath, "path to the MCM YAML config file")
	flag.Usage = func() {
		//nolint:errcheck // flag usage writes to stderr; nothing useful to do with a write error
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: mcm [--config <path>]\n\nStarts the MCM HTTP server. Configuration is loaded from --config and\noverridden by MCM_* environment variables (see internal/config).\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("mcm %s (commit %s, built %s)\n", version, commit, buildDate)
		os.Exit(0)
	}

	if flag.NArg() > 0 {
		//nolint:errcheck // flag usage writes to stderr; nothing useful to do with a write error
		fmt.Fprintf(flag.CommandLine.Output(), "unexpected arguments: %v\n", flag.Args())
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config %q: %w", configPath, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	//nolint:errcheck // startup banner to stdout; nothing useful to do with a write error
	fmt.Fprintf(os.Stdout, "starting MCM server on %s:%d\n", cfg.HTTP.BindAddress, cfg.HTTP.Port)
	return server.Run(ctx, cfg)
}
