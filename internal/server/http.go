package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// HTTPServer exposes snapshot and websocket endpoints for the dashboard.
type HTTPServer struct {
	logger   *slog.Logger
	addr     string
	store    *StateStore
	hub      *Hub
	upgrader websocket.Upgrader
}

// NewHTTPServer creates the HTTP runtime.
func NewHTTPServer(logger *slog.Logger, addr string, store *StateStore, hub *Hub) *HTTPServer {
	return &HTTPServer{
		logger: logger,
		addr:   addr,
		store:  store,
		hub:    hub,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
	}
}

// Run starts the HTTP server until the context is cancelled.
func (s *HTTPServer) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/snapshot", s.handleSnapshot)
	mux.HandleFunc("/api/ws", s.handleWebSocket)

	server := &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("starting http server", "addr", s.addr)
		err := server.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	}
}

func (s *HTTPServer) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.store.Snapshot())
}

func (s *HTTPServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Warn("websocket upgrade failed", "error", err)
		return
	}

	ch := s.hub.Subscribe()
	defer s.hub.Unsubscribe(ch)
	defer conn.Close()

	if err := conn.WriteJSON(Event{
		Type:     SnapshotEventType,
		Snapshot: ptr(s.store.Snapshot()),
	}); err != nil {
		return
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-readDone:
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		}
	}
}

func ptr[T any](value T) *T {
	return &value
}
