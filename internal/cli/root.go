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
		newBackupCommand(),
		newVersionCommand(version),
	)

	return cmd
}

func newStatusCommand() *cobra.Command {
	var configPath string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Show MCM and Mosquitto runtime status",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			result := diagnostics.CheckMQTTConnectivity(ctx, cfg.Mosquitto)
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "MCM status\n")
			fmt.Fprintf(out, "Mosquitto target: %s\n", result.Address)
			fmt.Fprintf(out, "Mosquitto TLS: %t\n", cfg.Mosquitto.TLS.Enabled)
			if result.OK {
				_, err := fmt.Fprintf(out, "Broker: connected (%s)\n", result.Message)
				return err
			}

			_, writeErr := fmt.Fprintf(out, "Broker: disconnected (%s)\n", result.Message)
			if writeErr != nil {
				return writeErr
			}
			return fmt.Errorf("Mosquitto connectivity check failed")
		},
	}

	addConfigFlag(cmd, &configPath)
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "Maximum time to wait for Mosquitto status check")
	return cmd
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
