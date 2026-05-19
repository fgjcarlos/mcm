package cli

import (
	"fmt"
	"path/filepath"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage and validate MCM configuration",
	}
	cmd.AddCommand(newConfigInitCommand(), newConfigValidateCommand())
	return cmd
}

func newConfigInitCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write an example MCM configuration file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.WriteExample(configPath); err != nil {
				return err
			}

			_, err := fmt.Fprintf(cmd.OutOrStdout(), "wrote example config to %s\n", configPath)
			return err
		},
	}

	addConfigFlag(cmd, &configPath)
	return cmd
}

func newConfigValidateCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the MCM configuration file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := config.Load(configPath); err != nil {
				return err
			}

			_, err := fmt.Fprintf(cmd.OutOrStdout(), "configuration is valid: %s\n", configPath)
			return err
		},
	}

	addConfigFlag(cmd, &configPath)
	return cmd
}

func addConfigFlag(cmd *cobra.Command, target *string) {
	defaultPath, err := config.DefaultPath()
	if err != nil {
		defaultPath = filepath.Join(".", "mcm.yaml")
	}

	cmd.Flags().StringVar(target, "config", defaultPath, "Path to the MCM config file")
}
