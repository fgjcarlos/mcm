package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/fgjcarlos/mcm/internal/agent"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "", "path to config file (required)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mcm-agent %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "error: -config flag is required")
		flag.Usage()
		os.Exit(1)
	}

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	a, err := agent.New(cfg, version, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create agent: %v\n", err)
		os.Exit(1)
	}

	logger.Info("mcm-agent starting",
		"version", version,
		"commit", commit,
		"site_id", cfg.Site.ID,
		"site_name", cfg.Site.Name,
	)

	if err := a.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: agent run: %v\n", err)
		os.Exit(1)
	}

	logger.Info("mcm-agent stopped")
}
