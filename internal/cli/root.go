package cli

import (
	"fmt"
	"io"

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
	return placeholderCommand("doctor", "Run diagnostics against MCM and Mosquitto")
}

func newStatusCommand() *cobra.Command {
	return placeholderCommand("status", "Show MCM and Mosquitto runtime status")
}

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage and validate MCM configuration",
	}
	cmd.AddCommand(placeholderCommand("validate", "Validate the MCM configuration file"))
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
