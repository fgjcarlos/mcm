package server

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestNewHTTPServerAppliesDefensiveLimits(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.HTTP.BindAddress = "127.0.0.1"
	cfg.HTTP.Port = 8080

	server := newHTTPServer(cfg, http.NotFoundHandler(), nil)

	if server.Addr != "127.0.0.1:8080" {
		t.Fatalf("Addr = %q, want %q", server.Addr, "127.0.0.1:8080")
	}
	if server.ReadHeaderTimeout != httpReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, httpReadHeaderTimeout)
	}
	if server.ReadTimeout != httpReadTimeout {
		t.Fatalf("ReadTimeout = %s, want %s", server.ReadTimeout, httpReadTimeout)
	}
	if server.WriteTimeout != httpWriteTimeout {
		t.Fatalf("WriteTimeout = %s, want %s", server.WriteTimeout, httpWriteTimeout)
	}
	if server.IdleTimeout != httpIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", server.IdleTimeout, httpIdleTimeout)
	}
	if server.MaxHeaderBytes != httpMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, httpMaxHeaderBytes)
	}
}

func TestShutdownHTTPServerUsesBoundedContextAndLogsError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	shutdownStarted := make(chan struct{})
	deadlineSeen := make(chan bool, 1)

	shutdownHTTPServer(func(ctx context.Context) error {
		close(shutdownStarted)
		_, ok := ctx.Deadline()
		deadlineSeen <- ok
		<-ctx.Done()
		return ctx.Err()
	}, 10*time.Millisecond, logger)

	select {
	case <-shutdownStarted:
	default:
		t.Fatal("shutdown function was not called")
	}
	select {
	case ok := <-deadlineSeen:
		if !ok {
			t.Fatal("shutdown context had no deadline")
		}
	default:
		t.Fatal("shutdown function did not report deadline")
	}

	got := logs.String()
	if !strings.Contains(got, "http server shutdown failed") {
		t.Fatalf("logs = %q, want shutdown failure message", got)
	}
	if !strings.Contains(got, context.DeadlineExceeded.Error()) {
		t.Fatalf("logs = %q, want deadline error", got)
	}
}

func TestShutdownHTTPServerAndAlertsUsesBoundedAlertContextAndLogsError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	deadlineSeen := make(chan bool, 1)

	shutdownHTTPServerAndAlerts(func(context.Context) error {
		return nil
	}, func(ctx context.Context) error {
		_, ok := ctx.Deadline()
		deadlineSeen <- ok
		<-ctx.Done()
		return ctx.Err()
	}, 10*time.Millisecond, logger)

	select {
	case ok := <-deadlineSeen:
		if !ok {
			t.Fatal("alert shutdown context had no deadline")
		}
	default:
		t.Fatal("alert shutdown function did not report deadline")
	}

	got := logs.String()
	if !strings.Contains(got, "webhook alerter shutdown failed") {
		t.Fatalf("logs = %q, want alert shutdown failure message", got)
	}
	if !strings.Contains(got, context.DeadlineExceeded.Error()) {
		t.Fatalf("logs = %q, want deadline error", got)
	}
}

func TestRunWaitsForWebhookAlertDrainBeforeReturning(t *testing.T) {
	dir := t.TempDir()
	alertStarted := make(chan struct{})
	releaseAlert := make(chan struct{})
	var closeStarted sync.Once
	var releaseOnce sync.Once
	alertServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		closeStarted.Do(func() { close(alertStarted) })
		<-releaseAlert
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseAlert) })
		alertServer.Close()
	})

	cfg := newRunTestConfig(t, dir)
	cfg.Alerting.Enabled = true
	cfg.Alerting.EndpointURL = alertServer.URL
	cfg.Alerting.Timeout = "2s"

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr := fmt.Sprintf("%s:%d", cfg.HTTP.BindAddress, cfg.HTTP.Port)
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(ctx, cfg)
	}()
	waitForHTTPReady(t, addr)

	resp, err := http.Get("http://" + addr + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET protected endpoint returned error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	select {
	case <-alertStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for webhook alert delivery to start")
	}

	cancel()
	select {
	case err := <-runErr:
		t.Fatalf("Run returned before webhook alert drain completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(releaseAlert) })
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(gracefulShutdownTimeout + brokerMonitorShutdownTimeout):
		t.Fatal("Run did not return after webhook alert drain completed")
	}
}

func TestRunWaitsForBrokerMonitorShutdownBeforeReturning(t *testing.T) {
	dir := t.TempDir()
	cfg := newRunTestConfig(t, dir)
	cfg.Mosquitto.Host = "127.0.0.1"
	cfg.Mosquitto.Port = 1 // unreachable; broker monitor will be in retry-loop, not blocking shutdown

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr := fmt.Sprintf("%s:%d", cfg.HTTP.BindAddress, cfg.HTTP.Port)
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(ctx, cfg)
	}()
	waitForHTTPReady(t, addr)

	// Sanity check: HTTP is up and serving.
	resp, err := http.Get("http://" + addr + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET protected endpoint returned error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	start := time.Now()
	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(gracefulShutdownTimeout + brokerMonitorShutdownTimeout):
		t.Fatalf("Run did not return within %s", gracefulShutdownTimeout+brokerMonitorShutdownTimeout)
	}

	elapsed := time.Since(start)
	// The broker monitor respects ctx.Done() quickly; Run should return
	// well under the upper bound. We allow up to the full budget as
	// slack so the test isn't flaky on slow CI, but flag if it's
	// close to the limit.
	if elapsed > gracefulShutdownTimeout {
		t.Fatalf("Run took %s after cancel; expected under %s", elapsed, gracefulShutdownTimeout)
	}
}

func waitForHTTPReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("HTTP server did not become ready at %s", addr)
}
