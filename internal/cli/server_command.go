package cli

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/server"
	"github.com/spf13/cobra"
)

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

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "starting MCM server on %s:%d\n", cfg.HTTP.BindAddress, cfg.HTTP.Port)
			if err != nil {
				return err
			}

			return server.Run(ctx, cfg)
		},
	}

	addConfigFlag(cmd, &configPath)
	return cmd
}
