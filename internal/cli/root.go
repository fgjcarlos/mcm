package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
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
	return placeholderCommand("server", "Start the MCM API and web UI server")
}

func newDoctorCommand() *cobra.Command {
	return newDoctorCommandWithCheck(diagnostics.CheckMosquittoConnection)
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

func newDoctorCommandWithCheck(check func(context.Context, config.MosquittoConfig) error) *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:          "doctor",
		Short:        "Run diagnostics against MCM and Mosquitto",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			address := fmt.Sprintf("%s:%d", cfg.Mosquitto.Host, cfg.Mosquitto.Port)
			_, _ = fmt.Fprintf(out, "Checking Mosquitto connectivity at %s...\n", address)

			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()

			if err := check(ctx, cfg.Mosquitto); err != nil {
				_, _ = fmt.Fprintf(out, "Mosquitto: unreachable (%s): %v\n", address, err)
				return fmt.Errorf("mosquitto broker is unreachable")
			}

			_, _ = fmt.Fprintf(out, "Mosquitto: reachable (%s)\n", address)
			return nil
		},
	}

	addConfigFlag(cmd, &configPath)

	return cmd
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
