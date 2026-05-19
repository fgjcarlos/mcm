package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Runtime wires the HTTP server and MQTT bridge together.
type Runtime struct {
	httpServer *HTTPServer
	mqttBridge *MQTTBridge
}

// NewRuntime constructs the MVP dashboard runtime.
func NewRuntime(logger *slog.Logger, listenAddr string, brokerURL string) *Runtime {
	store := NewStateStore(brokerURL)
	hub := NewHub()

	return &Runtime{
		httpServer: NewHTTPServer(logger, listenAddr, store, hub),
		mqttBridge: NewMQTTBridge(logger, brokerURL, store, hub),
	}
}

// Run starts the runtime until the context is cancelled or a component fails.
func (r *Runtime) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	start := func(run func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
				cancel()
			}
		}()
	}

	start(r.httpServer.Run)
	start(r.mqttBridge.Run)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case err := <-errCh:
		<-done
		return err
	case <-done:
		return nil
	}
}
