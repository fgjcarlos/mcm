package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	var configPath string
	var timeout time.Duration

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

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			result := diagnostics.CheckMQTTConnectivity(ctx, cfg.Mosquitto)
			out := cmd.OutOrStdout()
			if result.OK {
				_, err := fmt.Fprintf(out, "OK: %s\n", result.Message)
				return err
			}

			_, writeErr := fmt.Fprintf(out, "FAIL: %s\n", result.Message)
			if writeErr != nil {
				return writeErr
			}
			return fmt.Errorf("Mosquitto connectivity check failed")
		},
	}

	addConfigFlag(cmd, &configPath)
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "Maximum time to wait for Mosquitto connectivity")
	return cmd
}

func addConfigFlag(cmd *cobra.Command, target *string) {
	defaultPath, err := config.DefaultPath()
	if err != nil {
		defaultPath = filepath.Join(".", "mcm.yaml")
	}

	cmd.Flags().StringVar(target, "config", defaultPath, "Path to the MCM config file")
}
