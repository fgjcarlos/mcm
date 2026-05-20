package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// Run initializes persistence and serves the HTTP API.
func Run(ctx context.Context, cfg config.Config) error {
	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	app, err := New(cfg, store)
	if err != nil {
		return err
	}
	if err := app.BootstrapAdmin(ctx, cfg); err != nil {
		return err
	}
	go app.StartBrokerMonitor(ctx, cfg.Mosquitto)

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.HTTP.BindAddress, cfg.HTTP.Port),
		Handler: app.Handler(),
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}
