package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/server"
	"github.com/spf13/cobra"
)

// VersionInfo describes build metadata printed by the version command.
type VersionInfo struct {
	Version string
	Commit  string
	Date    string
}

// NewRootCommand creates the MCM CLI root command and all initial subcommands.
func NewRootCommand(version VersionInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcm",
		Short: "Mosquitto Control Manager",
		Long:  "MCM is a lightweight control plane for Eclipse Mosquitto.",
	}

	cmd.AddCommand(
		newServerCommand(),
		newDoctorCommand(),
		newStatusCommand(),
		newConfigCommand(),
		newVersionCommand(version),
	)

	return cmd
}

func newServerCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the MCM API and web UI server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			addr := fmt.Sprintf("%s:%d", cfg.HTTP.BindAddress, cfg.HTTP.Port)
			srv := &http.Server{
				Addr:    addr,
				Handler: server.NewHandler(acl.NewMemoryStore()),
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			errCh := make(chan error, 1)
			go func() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcm server listening on http://%s\n", addr)
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					errCh <- err
					return
				}
				errCh <- nil
			}()

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				return srv.Shutdown(context.Background())
			}
		},
	}

	addConfigFlag(cmd, &configPath)
	return cmd
}

func newDoctorCommand() *cobra.Command {
	return placeholderCommand("doctor", "Run diagnostics against MCM and Mosquitto")
}

func newStatusCommand() *cobra.Command {
	return placeholderCommand("status", "Show MCM and Mosquitto runtime status")
}

func newVersionCommand(version VersionInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			printVersion(out, version)
			return nil
		},
	}
}

func placeholderCommand(use string, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: not implemented yet\n", cmd.CommandPath())
			return err
		},
	}
}

func printVersion(out io.Writer, version VersionInfo) {
	fmt.Fprintf(out, "MCM\n")
	fmt.Fprintf(out, "version: %s\n", defaultValue(version.Version, "dev"))
	fmt.Fprintf(out, "commit: %s\n", defaultValue(version.Commit, "none"))
	fmt.Fprintf(out, "date: %s\n", defaultValue(version.Date, "unknown"))
}

func defaultValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
