package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/fgjcarlos/mcm/frontend"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/logging"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const (
	httpReadHeaderTimeout   = 5 * time.Second
	httpReadTimeout         = 30 * time.Second
	httpWriteTimeout        = 30 * time.Second
	httpIdleTimeout         = 60 * time.Second
	httpMaxHeaderBytes      = 16 << 10
	gracefulShutdownTimeout = 15 * time.Second
)

// buildApplier constructs the appropriate mosquitto.Applier for the given deploy config.
// When deploy mode is empty (disabled), a no-op applier is returned so the service
// can still be constructed — it will reject calls via ErrDeployDisabled.
func buildApplier(cfg config.DeployConfig) mosquitto.Applier {
	switch cfg.Mode {
	case "docker":
		return mosquitto.DockerApplier{
			ACLPath:       cfg.ACLPath,
			PasswdPath:    cfg.PasswdPath,
			ContainerName: cfg.ContainerName,
			Runner:        mosquitto.ExecRunner{},
		}
	case "file":
		return mosquitto.FileApplier{
			ACLPath:    cfg.ACLPath,
			PasswdPath: cfg.PasswdPath,
			PIDPath:    cfg.PIDPath,
		}
	default:
		// Deploy disabled or unknown — return a no-op applier.
		// The service guards via ErrDeployDisabled before calling Apply.
		return mosquitto.FileApplier{}
	}
}

// openConfiguredStorage opens the persistence backend selected in the config.
//
// The "postgres" backend shape is accepted by config validation for
// forward-compatibility (see ADR-0008), but no Postgres implementation exists
// yet. Rather than silently falling back to SQLite, boot fails loudly so the
// operator knows their selection is not honored.
func openConfiguredStorage(cfg config.DatabaseConfig) (*storage.Store, error) {
	switch cfg.Backend {
	case "", "sqlite":
		return storage.Open(cfg.Path)
	case "postgres":
		return nil, fmt.Errorf("database.backend %q is configured but not yet implemented; use \"sqlite\"", cfg.Backend)
	default:
		return nil, fmt.Errorf("database.backend %q is not supported; use \"sqlite\"", cfg.Backend)
	}
}

// Run initializes persistence and serves the HTTP API.
func Run(ctx context.Context, cfg config.Config) error {
	logger, err := logging.New(cfg.Logging, os.Stderr)
	if err != nil {
		return fmt.Errorf("build logger: %w", err)
	}
	slog.SetDefault(logger)

	store, err := openConfiguredStorage(cfg.Database)
	if err != nil {
		return err
	}
	defer store.Close()

	app, err := New(cfg, store, logger)
	if err != nil {
		return err
	}

	// Wire the deploy service. It is always constructed; when deploy mode is
	// empty the service returns ErrDeployDisabled on all operations.
	deployAuditFn := func(auditCtx context.Context, actor, action, resourceType, resourceID, result string, metadata []byte) {
		payload := metadata
		if payload == nil {
			payload = []byte(`{}`)
		}
		now := time.Now().UTC()
		_, _ = store.RecordAuditEvent(auditCtx, storage.CreateAuditEventParams{
			OccurredAt:   now,
			Actor:        actor,
			Action:       action,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			Result:       result,
			Metadata:     json.RawMessage(payload),
		})
		if app.auditRetention > 0 {
			_, _ = store.PruneAuditEvents(auditCtx, now.Add(-app.auditRetention))
		}
	}
	go app.StartEventRetentionPruner(ctx)
	deployCfg := cfg.Mosquitto.Deploy
	applier := buildApplier(deployCfg)
	app.deploySvc = deploy.NewService(
		applier,
		store.ACLStore(),
		store,
		store,
		diagnostics.CheckMQTTConnectivity,
		cfg.Mosquitto,
		deployCfg,
		deployAuditFn,
	)

	if dist, fErr := frontend.DistFS(); fErr == nil {
		app.frontendFS = dist
	}
	if err := app.BootstrapAdmin(ctx, cfg); err != nil {
		return err
	}
	go app.StartBrokerMonitor(ctx, cfg.Mosquitto)

	handler := withRequestLogging(app.Handler(), logger, app.metrics, app.trustedProxies)
	tlsConfig, err := buildHTTPTLSConfig(cfg.HTTP.TLS)
	if err != nil {
		return err
	}
	if tlsConfig != nil {
		handler = withHSTS(handler)
	}

	server := newHTTPServer(cfg, handler, tlsConfig)
	shutdownDone := make(chan struct{})

	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownHTTPServerAndAlerts(server.Shutdown, app.alerts.Shutdown, gracefulShutdownTimeout, logger)
	}()

	if tlsConfig != nil {
		// Cert/key already loaded into TLSConfig; ListenAndServeTLS ignores its file arguments when set.
		if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen and serve tls: %w", err)
		}
		<-shutdownDone
		return nil
	}

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}
	<-shutdownDone
	return nil
}

func shutdownHTTPServerAndAlerts(shutdownHTTP func(context.Context) error, shutdownAlerts func(context.Context) error, timeout time.Duration, logger *slog.Logger) {
	shutdownHTTPServer(shutdownHTTP, timeout, logger)
	alertCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := shutdownAlerts(alertCtx); err != nil {
		logger.Error("webhook alerter shutdown failed", slog.String("error", err.Error()))
	}
}

func shutdownHTTPServer(shutdown func(context.Context) error, timeout time.Duration, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		logger.Error("http server shutdown failed", slog.String("error", err.Error()))
	}
}

func newHTTPServer(cfg config.Config, handler http.Handler, tlsConfig *tls.Config) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.HTTP.BindAddress, cfg.HTTP.Port),
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
}

// buildHTTPTLSConfig returns a tls.Config when TLS is enabled, or nil otherwise.
// The configuration is validated by config.Validate before reaching this point;
// any IO error here aborts startup so misconfiguration fails loudly.
func buildHTTPTLSConfig(cfg config.HTTPTLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load http tls cert/key: %w", err)
	}

	minVersion, err := parseTLSVersion(cfg.MinVersion)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   minVersion,
	}

	if cfg.ClientCAFile != "" {
		caPEM, err := os.ReadFile(cfg.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read http tls client_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("http tls client_ca_file %q contains no PEM certificates", cfg.ClientCAFile)
		}
		tlsConfig.ClientCAs = pool
		if cfg.RequireClientCert {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		}
	} else if cfg.RequireClientCert {
		return nil, fmt.Errorf("http.tls.require_client_cert is true but http.tls.client_ca_file is empty")
	}

	return tlsConfig, nil
}

func parseTLSVersion(value string) (uint16, error) {
	switch value {
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unsupported http.tls.min_version %q (use \"1.2\" or \"1.3\")", value)
	}
}

// withHSTS adds Strict-Transport-Security to every TLS response.
func withHSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}
