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
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/fgjcarlos/mcm/frontend"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/logging"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const (
	httpReadHeaderTimeout   = 5 * time.Second
	httpReadTimeout         = 30 * time.Second
	httpWriteTimeout        = 30 * time.Second
	httpIdleTimeout         = 60 * time.Second
	httpMaxHeaderBytes      = 16 << 10
	gracefulShutdownTimeout = 15 * time.Second
	// brokerMonitorShutdownTimeout caps how long Run waits for
	// StartBrokerMonitor to drain after context cancel. A stuck
	// broker monitor cannot block Run beyond the
	// gracefulShutdownTimeout budget.
	brokerMonitorShutdownTimeout = 5 * time.Second
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
		file := mosquitto.FileApplier{
			ACLPath:       cfg.ACLPath,
			PasswdPath:    cfg.PasswdPath,
			PIDPath:       cfg.PIDPath,
			ReloadCommand: cfg.ReloadCommand,
			ReloadRunner:  mosquitto.ExecRunner{},
		}
		// Issue #294: a restart is a different mechanism than a reload.
		// The file applier used to report that it could restart the
		// broker as soon as a reload command was configured, and it then
		// ran that RELOAD command as the restart: a change Mosquitto
		// reads only at start-up (a bridge, a plugin) stayed inactive
		// while the deployment reported it as applied. The wrapper makes
		// the restart explicit: without a restart command the deployment
		// cannot restart the broker and a change that needs one is
		// refused instead of being reloaded.
		return deploy.NewRestartApplier(file, cfg.RestartCommand, mosquitto.ExecRunner{})
	default:
		// Deploy disabled or unknown — return a no-op applier.
		// The service guards via ErrDeployDisabled before calling Apply.
		return mosquitto.FileApplier{}
	}
}

// brokerConfigDir returns the broker-side path of the shared configuration
// directory. It falls back to the broker default when the operator did
// not configure one.
func brokerConfigDir(cfg config.MosquittoConfig) string {
	dir := strings.TrimSpace(cfg.BrokerConfigDir)
	if dir == "" {
		return defaultBrokerConfigDir
	}
	return dir
}

// brokerConfigFile returns the path of the broker's mosquitto.conf as the
// MCM process sees it, and whether MCM knows where the broker
// configuration lives at all.
//
// The broker and MCM mount the same configuration volume under different
// paths: the broker reads /mosquitto/config/mosquitto.conf while MCM sees
// it under its own mount. The path is empty when the operator did not
// configure a configuration directory — the startup check then cannot
// compare the broker's effective files and says so.
func brokerConfigFile(cfg config.MosquittoConfig) string {
	dir := strings.TrimSpace(cfg.ConfigDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "mosquitto.conf")
}

// checkDeployCapabilities is the issue #294 startup gate that fails fast
// with an actionable error when the deploy surface is misconfigured.
// It does NOT fail the MCM server when deploy is disabled (empty mode)
// or when the only thing missing is a reload trigger (the existing
// apply path returns ErrReloadNotSignaled at runtime; we just log a
// warning so operators see the misconfiguration before traffic arrives).
//
// The check covers these surfaces:
//
//  1. ACLPath + PasswdPath parents writable by the MCM process — the
//     atomic-rename applier writes to those directories on every
//     successful deploy.
//  2. PIDPath readable when the legacy SIGHUP reload is configured —
//     the applier reads it on every apply.
//     2b. The reload and restart commands exist and are executable.
//     2c. The shared configuration directory is not world-writable (warning).
//  3. Either PIDPath or ReloadCommand set when reload_strategy is
//     "sighup" / unspecified — otherwise the apply will fail at
//     runtime (#293) and operators should know before serving traffic.
//  4. ContainerName non-empty in docker mode — docker exec needs a
//     target.
//  5. The broker's effective password_file / acl_file are the files MCM
//     writes — a broker that reads other files keeps enforcing the
//     previous policy while every deploy reports success.
//
// brokerConfigDir is the broker-side path of the shared configuration
// directory (the broker's own mount point). It is used to translate the
// paths the broker reads into the namespace MCM writes in.
func checkDeployCapabilities(cfg config.DeployConfig, mosquittoCfg config.MosquittoConfig, applier mosquitto.Applier, logger *slog.Logger) error {
	if cfg.Mode == "" {
		// Deploy disabled — nothing to verify.
		return nil
	}

	switch cfg.Mode {
	case "file":
		// (1) ACLPath + PasswdPath writable.
		if err := requireWritableParent(cfg.ACLPath, "acl_path"); err != nil {
			return err
		}
		if err := requireWritableParent(cfg.PasswdPath, "passwd_path"); err != nil {
			return err
		}

		// (2) PIDPath readable when set.
		if cfg.PIDPath != "" {
			if _, err := os.Stat(cfg.PIDPath); err != nil {
				return fmt.Errorf("deploy PIDPath %q is not accessible: %w", cfg.PIDPath, err)
			}
		}

		// (2b) The reload and restart commands must exist and be
		// executable. A command that cannot run fails at the first
		// deploy apply, long after the operator configured it; the
		// startup check turns that into an actionable boot error.
		if err := requireExecutableCommand(cfg.ReloadCommand, "reload_command"); err != nil {
			return err
		}
		if err := requireExecutableCommand(cfg.RestartCommand, "restart_command"); err != nil {
			return err
		}

		// (2c) The shared configuration directory must not be
		// world-writable: every process on the host could then replace
		// the files the broker reads. It is a warning and not an error
		// because the development stack bootstraps the shared volume
		// with 0777 on purpose.
		warnIfWorldWritable(filepath.Dir(cfg.ACLPath), "acl_path", logger)
		warnIfWorldWritable(filepath.Dir(cfg.PasswdPath), "passwd_path", logger)

		// (3) Reload trigger — log-only warning, no startup failure.
		if cfg.PIDPath == "" && len(cfg.ReloadCommand) == 0 {
			logger.Warn(
				"deploy has no reload trigger (neither PIDPath nor ReloadCommand set); POST /api/v1/deployments/apply will fail with ErrReloadNotSignaled until you configure one",
				"mode", cfg.Mode,
			)
		}

		// (5) The broker must read exactly the files MCM writes. A
		// broker that reads another password file (the legacy
		// `passwords` path) or no ACL file keeps enforcing the previous
		// policy while every deploy reports success.
		brokerConfigPath := brokerConfigFile(mosquittoCfg)
		if brokerConfigPath == "" {
			logger.Warn(
				"mosquitto.config_dir is not configured; the broker's effective password_file / acl_file cannot be compared with the deploy paths",
				"mode", cfg.Mode,
			)
		} else if err := deploy.CheckBrokerEffectiveFiles(deploy.EffectiveFilesOptions{
			BrokerConfigPath: brokerConfigPath,
			BrokerConfigDir:  brokerConfigDir(mosquittoCfg),
			ACLPath:          cfg.ACLPath,
			PasswdPath:       cfg.PasswdPath,
		}); err != nil {
			return err
		}
		return nil

	case "docker":
		// (4) ContainerName non-empty.
		if cfg.ContainerName == "" {
			return fmt.Errorf("deploy container_name is empty; the \"docker\" mode requires a target container for docker exec")
		}
		// Note: Docker socket reachability is checked at apply time via
		// docker exec. We intentionally do NOT touch the socket at
		// startup — production deployments do NOT mount it.
		return nil

	default:
		return fmt.Errorf("deploy mode %q is not supported; use \"file\" or \"docker\"", cfg.Mode)
	}
}

// requireExecutableCommand ensures a configured command exists and can be
// executed. An empty command is accepted: the caller decides whether its
// absence is an error.
func requireExecutableCommand(command []string, fieldName string) error {
	if len(command) == 0 {
		return nil
	}
	name := strings.TrimSpace(command[0])
	if name == "" {
		return fmt.Errorf("deploy %s is empty; provide the command name", fieldName)
	}
	info, err := os.Stat(name)
	if err != nil {
		return fmt.Errorf("deploy %s %q is not accessible: %w", fieldName, name, err)
	}
	if info.IsDir() {
		return fmt.Errorf("deploy %s %q is a directory, not a command", fieldName, name)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("deploy %s %q is not executable (mode %s); make it executable or point it at an executable program", fieldName, name, info.Mode().Perm())
	}
	return nil
}

// warnIfWorldWritable logs a warning when the directory MCM writes the
// broker's files into is world-writable. Every process on the host could
// then replace the files the broker reads. It is a warning because the
// development stack bootstraps the shared volume with 0777 on purpose.
func warnIfWorldWritable(dir, fieldName string, logger *slog.Logger) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		return
	}
	if info.Mode().Perm()&0o002 != 0 {
		logger.Warn(
			"the shared broker configuration directory is world-writable; every local process can replace the files the broker reads",
			"field", fieldName,
			"dir", dir,
			"mode", info.Mode().Perm().String(),
		)
	}
}

// requireWritableParent ensures the parent directory of path exists and
// is writable by the MCM process. Returns an actionable error otherwise.
func requireWritableParent(path, fieldName string) error {
	if path == "" {
		return fmt.Errorf("deploy %s is empty; set it to the broker passwd/acl file path the MCM process must write to", fieldName)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("deploy %s parent dir %q is not creatable: %w", fieldName, dir, err)
	}
	// Probe write by creating a tiny file in the parent dir and removing it.
	f, err := os.CreateTemp(dir, ".mcm-cap-probe-*")
	if err != nil {
		return fmt.Errorf("deploy %s parent dir %q is not writable by the MCM process: %w", fieldName, dir, err)
	}
	probePath := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(probePath)
		return fmt.Errorf("deploy %s parent dir %q probe failed: %w", fieldName, dir, err)
	}
	if err := os.Remove(probePath); err != nil {
		return fmt.Errorf("deploy %s parent dir %q probe cleanup failed: %w", fieldName, dir, err)
	}
	return nil
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
	defer store.Close() //nolint:errcheck

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
	// Issue #294: capability checks run before serving traffic so the
	// operator sees a clear error at startup instead of a confusing
	// 500 at the first POST /api/v1/deployments/apply. The checks
	// cover the minimum surface needed for a deploy to land on the
	// broker: the ACL/passwd paths are writable by the MCM process,
	// and (when reload_strategy=sighup is used) the PID file is
	// readable.
	if err := checkDeployCapabilities(deployCfg, cfg.Mosquitto, applier, logger); err != nil {
		return fmt.Errorf("deploy capability check failed: %w", err)
	}
	// Issue #298: wire the broker-config surface. When ConfigDir is empty
	// or the catalog fails to load, WithBrokerConfig leaves the surface
	// disabled and the handler returns 503. ConfPath is the broker's
	// mosquitto.conf inside the configured ConfigDir; an empty ConfigDir
	// means "no broker config to manage".
	deployService := deploy.NewService(
		applier,
		store.ACLStore(),
		store,
		store,
		// Issue #293: the post-apply verifier (positive+negative round-trip)
		// replaces the old CONNECT/CONNACK-only healthcheck. The cleartext
		// lookup lives on App so the verifier can authenticate as a
		// non-service user that was created in this process lifetime.
		diagnostics.VerifierFunc(diagnostics.VerifyActive),
		app,
		cfg.Mosquitto,
		deployCfg,
		deployAuditFn,
	)
	app.deploySvc = deployService
	// Issue #292 (S19): restore an apply interrupted by the death of a
	// previous MCM process before serving any deploy request. Recovery
	// always restores the pre-apply files, never resumes the apply.
	deployService.RecoverInterruptedApply()

	var brokerCatalog *catalog.Catalog
	if cat, catErr := catalog.LoadVersion("2.0"); catErr == nil {
		brokerCatalog = cat
	} else {
		logger.Warn("broker-config catalog load failed; broker-config endpoints disabled", "err", catErr)
	}
	if cfg.Mosquitto.ConfigDir != "" && brokerCatalog != nil {
		confPath := filepath.Join(cfg.Mosquitto.ConfigDir, "mosquitto.conf")
		deployService.WithBrokerConfig(confPath, brokerCatalog)
		app.brokerCfgSvc = deployService
		app.bridgeSvc = deployService
		if app.brokerCfgCatalog == nil {
			app.brokerCfgCatalog = brokerCatalog
		}
	}

	// Issue #304: the plugin catalog lists the plugins MCM is able to
	// configure for the broker version, and the control channel of the
	// dynamic security plugin is how MCM administers its database.
	pluginCatalog, pluginCatalogErr := plugins.LoadVersion("2.0")
	if pluginCatalogErr != nil {
		logger.Warn("plugin catalog load failed; plugin endpoints disabled", "err", pluginCatalogErr)
	} else {
		deployService.WithPluginCatalog(pluginCatalog)
		deployService.WithDynSec(buildDynSecTransportFactory(cfg.Mosquitto))
		app.pluginSvc = deployService
		app.dynsecSvc = deployService
	}

	if dist, fErr := frontend.DistFS(); fErr == nil {
		app.frontendFS = dist
	}
	if err := app.BootstrapAdmin(ctx, cfg); err != nil {
		return err
	}

	// errgroup so Run waits for the broker monitor to drain after
	// context cancel — the bare `go` previously used could leak the
	// goroutine past Run returning, losing in-flight broker events.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		app.StartBrokerMonitor(gctx, cfg.Mosquitto)
		return nil
	})
	g.Go(func() error {
		app.StartBrokerLogPoller(gctx)
		return nil
	})

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
	} else {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen and serve: %w", err)
		}
	}
	<-shutdownDone

	// Wait for the broker monitor to drain after context cancel, capped
	// at brokerMonitorShutdownTimeout so a stuck monitor cannot block
	// Run beyond the gracefulShutdownTimeout budget.
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_ = g.Wait()
	}()
	select {
	case <-waitDone:
	case <-time.After(brokerMonitorShutdownTimeout):
		logger.Warn("broker monitor did not shut down within timeout",
			slog.Duration("timeout", brokerMonitorShutdownTimeout))
	}
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
