package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"io/fs"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/alerting"
	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/metrics"
	"github.com/fgjcarlos/mcm/internal/storage"
)

type contextKey string

const currentUserContextKey contextKey = "current_user"

// App wires the HTTP API to storage and auth dependencies.
type App struct {
	store              *storage.Store
	aclStore           acl.Store
	tokens             *auth.TokenManager
	brokerEvents       *BrokerEventHub
	schemaCache        *jsonSchemaCache
	alerts             *alerting.WebhookAlerter
	metrics            *metrics.Registry
	mosquitto          config.MosquittoConfig
	cfg                config.Config
	deploySvc          deployServicer
	loginLockoutWindow time.Duration
	loginMaxAttempts   int
	auditRetention     time.Duration
	securityRetention  time.Duration
	trustedProxies     []*net.IPNet
	frontendFS         fs.FS
	logger             *slog.Logger
	now                func() time.Time
}

// New creates an HTTP app configured for the auth MVP. logger may be nil; the
// app will fall back to slog.Default() in that case.
func New(cfg config.Config, store *storage.Store, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	ttl, err := time.ParseDuration(cfg.Auth.TokenTTL)
	if err != nil {
		return nil, fmt.Errorf("parse auth token ttl: %w", err)
	}
	metricsRetention, err := time.ParseDuration(cfg.Metrics.BrokerRetention)
	if err != nil {
		return nil, fmt.Errorf("parse broker metrics retention: %w", err)
	}
	auditRetention, err := time.ParseDuration(cfg.Metrics.AuditRetention)
	if err != nil {
		return nil, fmt.Errorf("parse audit event retention: %w", err)
	}
	securityRetention, err := time.ParseDuration(cfg.Metrics.SecurityRetention)
	if err != nil {
		return nil, fmt.Errorf("parse security event retention: %w", err)
	}
	loginLockoutWindow, err := time.ParseDuration(cfg.Auth.LoginLockout.Window)
	if err != nil {
		return nil, fmt.Errorf("parse auth login lockout window: %w", err)
	}
	trustedProxies, err := parseTrustedProxies(cfg.HTTP.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("parse http trusted proxies: %w", err)
	}

	mcmMetrics := metrics.New()
	brokerEvents := NewBrokerEventHub()
	brokerEvents.SetPersistence(store, metricsRetention)
	brokerEvents.SetMetrics(mcmMetrics)

	return &App{
		store:              store,
		aclStore:           store.ACLStore(),
		tokens:             auth.NewTokenManager(cfg.Auth.JWTSecret, ttl),
		brokerEvents:       brokerEvents,
		schemaCache:        &jsonSchemaCache{},
		alerts:             alerting.NewWebhookAlerter(cfg.Alerting, logger),
		metrics:            mcmMetrics,
		mosquitto:          cfg.Mosquitto,
		cfg:                cfg,
		loginLockoutWindow: loginLockoutWindow,
		loginMaxAttempts:   cfg.Auth.LoginLockout.MaxAttempts,
		auditRetention:     auditRetention,
		securityRetention:  securityRetention,
		trustedProxies:     trustedProxies,
		logger:             logger,
		now:                time.Now,
	}, nil
}

// BootstrapAdmin creates the configured bootstrap admin if no admin users exist yet.
func (a *App) BootstrapAdmin(ctx context.Context, cfg config.Config) error {
	count, err := a.store.CountActiveAdminUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if cfg.Auth.BootstrapAdmin.Username == "" || cfg.Auth.BootstrapAdmin.Password == "" {
		return nil
	}

	passwordHash, err := auth.HashPassword(cfg.Auth.BootstrapAdmin.Password)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}

	_, err = a.store.CreateAdminUser(ctx, storage.CreateAdminUserParams{
		Username:     cfg.Auth.BootstrapAdmin.Username,
		PasswordHash: passwordHash,
		Role:         string(auth.RoleAdmin),
	})
	if err != nil {
		return fmt.Errorf("create bootstrap admin user: %w", err)
	}

	return nil
}

func (a *App) loadCurrentUser(ctx context.Context, claims auth.UserClaims) (storage.AdminUser, error) {
	user, err := a.store.GetAdminUserByID(ctx, claims.UserID)
	if err != nil {
		return storage.AdminUser{}, err
	}
	if user.Disabled {
		return storage.AdminUser{}, storage.ErrUserNotFound
	}
	return user, nil
}

// StartEventRetentionPruner periodically enforces bounded retention for durable audit and security event tables.
func (a *App) StartEventRetentionPruner(ctx context.Context) {
	if a.store == nil {
		return
	}
	a.pruneEventRetention(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pruneEventRetention(ctx)
		}
	}
}

func (a *App) pruneEventRetention(ctx context.Context) {
	now := a.now().UTC()
	if a.auditRetention > 0 {
		if deleted, err := a.store.PruneAuditEvents(ctx, now.Add(-a.auditRetention)); err != nil {
			a.logger.Warn("prune audit events failed", "err", err)
		} else if deleted > 0 {
			a.logger.Info("pruned audit events", "deleted", deleted)
		}
	}
	if a.securityRetention > 0 {
		if deleted, err := a.store.PruneSecurityEvents(ctx, now.Add(-a.securityRetention)); err != nil {
			a.logger.Warn("prune security events failed", "err", err)
		} else if deleted > 0 {
			a.logger.Info("pruned security events", "deleted", deleted)
		}
	}
}

// Handler returns the configured HTTP handler tree.
//
// Body-size limiting: the entire mux is wrapped with withBodyLimit so every route —
// including pre-auth endpoints such as POST /api/v1/auth/login — enforces the 1 MiB
// cap. Routes that carry no body (GET /healthz, GET /metrics, GET /api/v1/broker/events
// WebSocket upgrade) are unaffected because http.MaxBytesReader is a no-op when
// nothing is read from the body.
func (a *App) Handler() http.Handler {
	aclAPI := &aclAPI{
		store:                 a.aclStore,
		audit:                 a.recordAuditFromRequest,
		recordSecurityFailure: a.recordSecurityFailure,
		recordSecurityChange:  a.recordSecurityChange,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", aclAPI.handleHealthz)
	mux.HandleFunc("GET /readyz", a.handleReadyz)
	mux.Handle("GET /metrics", a.metrics.Handler())
	mux.Handle("GET /api/v1/acls", a.requireRole(auth.RoleAuditor, http.HandlerFunc(aclAPI.handleListRules)))
	mux.Handle("POST /api/v1/acls", a.requireRole(auth.RoleOperator, http.HandlerFunc(aclAPI.handleCreateRule)))
	mux.Handle("PUT /api/v1/acls/{id}", a.requireRole(auth.RoleOperator, http.HandlerFunc(aclAPI.handleUpdateRule)))
	mux.Handle("DELETE /api/v1/acls/{id}", a.requireRole(auth.RoleOperator, http.HandlerFunc(aclAPI.handleDeleteRule)))
	mux.HandleFunc("POST /api/v1/auth/login", a.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/login/mfa", a.handleLoginMFA)
	mux.Handle("GET /api/v1/auth/me", a.requireAuth(http.HandlerFunc(a.handleCurrentUser)))
	mux.Handle("POST /api/v1/auth/mfa/setup", a.requireAuth(http.HandlerFunc(a.handleMFASetup)))
	mux.Handle("POST /api/v1/auth/mfa/verify", a.requireAuth(http.HandlerFunc(a.handleMFAVerify)))
	mux.Handle("DELETE /api/v1/auth/mfa", a.requireAuth(http.HandlerFunc(a.handleMFADisable)))
	mux.Handle("GET /api/v1/admin-users", a.requireRole(auth.RoleAuditor, http.HandlerFunc(a.handleListAdminUsers)))
	mux.Handle("POST /api/v1/admin-users", a.requireRole(auth.RoleAdmin, http.HandlerFunc(a.handleCreateAdminUser)))
	mux.Handle("GET /api/v1/admin-users/{id}", a.requireRole(auth.RoleAuditor, http.HandlerFunc(a.handleGetAdminUser)))
	mux.Handle("PUT /api/v1/admin-users/{id}", a.requireRole(auth.RoleAdmin, http.HandlerFunc(a.handleUpdateAdminUser)))
	mux.Handle("DELETE /api/v1/admin-users/{id}", a.requireRole(auth.RoleAdmin, http.HandlerFunc(a.handleDeleteAdminUser)))
	mux.Handle("GET /api/v1/json-schemas", a.requireRole(auth.RoleAuditor, http.HandlerFunc(a.handleListJSONSchemas)))
	mux.Handle("POST /api/v1/json-schemas", a.requireRole(auth.RoleOperator, http.HandlerFunc(a.handleCreateJSONSchema)))
	mux.Handle("PUT /api/v1/json-schemas/{id}", a.requireRole(auth.RoleOperator, http.HandlerFunc(a.handleUpdateJSONSchema)))
	mux.Handle("DELETE /api/v1/json-schemas/{id}", a.requireRole(auth.RoleOperator, http.HandlerFunc(a.handleDeleteJSONSchema)))
	mux.Handle("GET /api/v1/audit-events", a.requireRole(auth.RoleAuditor, http.HandlerFunc(a.handleListAuditEvents)))
	mux.Handle("GET /api/v1/security/events", a.requireRole(auth.RoleAuditor, http.HandlerFunc(a.handleSecurityEvents)))
	mux.HandleFunc("GET /api/v1/status", a.handleStatus)
	mux.HandleFunc("GET /api/v1/broker/events", a.handleBrokerEvents)
	mux.Handle("GET /api/v1/mqtt-users", a.requireRole(auth.RoleAuditor, http.HandlerFunc(a.handleListMQTTUsers)))
	mux.Handle("POST /api/v1/mqtt-users", a.requireRole(auth.RoleOperator, http.HandlerFunc(a.handleCreateMQTTUser)))
	mux.Handle("GET /api/v1/mqtt-users/{id}", a.requireRole(auth.RoleAuditor, http.HandlerFunc(a.handleGetMQTTUser)))
	mux.Handle("PUT /api/v1/mqtt-users/{id}", a.requireRole(auth.RoleOperator, http.HandlerFunc(a.handleUpdateMQTTUser)))
	mux.Handle("DELETE /api/v1/mqtt-users/{id}", a.requireRole(auth.RoleOperator, http.HandlerFunc(a.handleDeleteMQTTUser)))
	mux.Handle("POST /api/v1/mqtt-users/{id}/reset-password", a.requireRole(auth.RoleOperator, http.HandlerFunc(a.handleResetMQTTUserPassword)))
	edgeSiteAPI := &edgeAPI{store: a.store}
	mux.Handle("POST /api/v1/edge/heartbeat", a.requireRole(auth.RoleOperator, http.HandlerFunc(edgeSiteAPI.handleHeartbeat)))
	mux.Handle("GET /api/v1/edge/sites", a.requireRole(auth.RoleViewer, http.HandlerFunc(edgeSiteAPI.handleListSites)))
	mux.Handle("GET /api/v1/edge/sites/{id}", a.requireRole(auth.RoleViewer, http.HandlerFunc(edgeSiteAPI.handleGetSite)))
	if a.deploySvc != nil {
		dAPI := &deployAPI{svc: a.deploySvc}
		mux.Handle("GET /api/v1/deployments", a.requireRole(auth.RoleAuditor, http.HandlerFunc(dAPI.handleList)))
		mux.Handle("POST /api/v1/deployments/preview", a.requireRole(auth.RoleOperator, http.HandlerFunc(dAPI.handlePreview)))
		mux.Handle("POST /api/v1/deployments/apply", a.requireRole(auth.RoleAdmin, http.HandlerFunc(dAPI.handleApply)))
	}
	mux.Handle("GET /api/v1/settings", a.requireRole(auth.RoleAdmin, http.HandlerFunc(a.handleSettings)))
	if a.frontendFS != nil {
		mux.Handle("/", spaHandler(a.frontendFS))
	}
	return withBodyLimit(mux, apiBodyLimitBytes)
}

type statusResponse struct {
	Broker brokerStatusResponse `json:"broker"`
}

type brokerStatusResponse struct {
	Status     string                `json:"status"`
	ObservedAt time.Time             `json:"observed_at"`
	Target     brokerTargetResponse  `json:"target"`
	Metrics    brokerMetricsResponse `json:"metrics"`
}

type brokerTargetResponse struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Address    string `json:"address"`
	TLSEnabled bool   `json:"tls_enabled"`
}

type brokerMetricsResponse struct {
	EventSubscribers int                  `json:"event_subscribers"`
	StatusEvents     uint64               `json:"status_events"`
	TopicMessages    uint64               `json:"topic_messages"`
	LastMessageAt    *time.Time           `json:"last_message_at,omitempty"`
	Traffic          BrokerTrafficMetrics `json:"traffic"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Disabled bool   `json:"disabled"`
	Role     string `json:"role"`
}

type jsonSchemaRequest struct {
	Name        string          `json:"name"`
	TopicFilter string          `json:"topic_filter"`
	Schema      json.RawMessage `json:"schema"`
	Description string          `json:"description"`
	Enabled     bool            `json:"enabled"`
}

type loginResponse struct {
	Token        string            `json:"token,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at,omitempty"`
	User         adminUserResponse `json:"user,omitzero"`
	MFARequired  bool              `json:"mfa_required,omitempty"`
	MFAChallenge string            `json:"mfa_challenge,omitempty"`
}

type adminUserResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Disabled  bool      `json:"disabled"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	snapshot := a.brokerEvents.Snapshot()
	address := net.JoinHostPort(a.mosquitto.Host, strconv.Itoa(a.mosquitto.Port))

	writeJSON(w, http.StatusOK, statusResponse{
		Broker: brokerStatusResponse{
			Status:     snapshot.Status.Status,
			ObservedAt: snapshot.Status.ObservedAt,
			Target: brokerTargetResponse{
				Host:       a.mosquitto.Host,
				Port:       a.mosquitto.Port,
				Address:    address,
				TLSEnabled: a.mosquitto.TLS.Enabled,
			},
			Metrics: brokerMetricsResponse{
				EventSubscribers: snapshot.EventSubscribers,
				StatusEvents:     snapshot.StatusEvents,
				TopicMessages:    snapshot.TopicMessages,
				LastMessageAt:    snapshot.LastMessageAt,
				Traffic:          snapshot.Traffic,
			},
		},
	})
}

func (a *App) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"error":  "database unavailable",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "username and password are required"})
		return
	}

	if a.enforceLoginLockout(w, r, username) {
		return
	}

	user, err := a.store.GetAdminUserByUsername(r.Context(), username)
	if errors.Is(err, storage.ErrUserNotFound) {
		a.recordSecurityFailure(r, "admin_login_failed", "invalid_credentials", username)
		a.recordLoginAttempt(r, username, false)
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if user.Disabled {
		a.recordSecurityFailure(r, "admin_login_failed", "disabled_user", user.Username)
		a.recordLoginAttempt(r, user.Username, false)
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "user is disabled"})
		return
	}

	match, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !match {
		a.recordSecurityFailure(r, "admin_login_failed", "invalid_credentials", user.Username)
		a.recordLoginAttempt(r, user.Username, false)
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}

	if user.MFAEnabled {
		// Password is correct but MFA is required; issue a short-lived challenge
		// that the second-step endpoint exchanges for a real access token.
		challenge, err := a.tokens.IssueMFAChallenge(user.ID, user.Username, mfaChallengeTTL, a.now().UTC())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		// The password attempt itself is recorded as a success here so the IP/username
		// brute-force counter does not punish a user mid-MFA prompt. The MFA verify
		// step has its own audit + security events.
		a.recordLoginAttempt(r, user.Username, true)
		writeJSON(w, http.StatusOK, loginResponse{
			MFARequired:  true,
			MFAChallenge: challenge,
		})
		return
	}

	userRole, err := auth.ParseRole(user.Role, auth.RoleAdmin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	token, expiresAt, err := a.tokens.Issue(user.ID, user.Username, userRole, a.now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	a.recordLoginAttempt(r, user.Username, true)
	a.resetFailedLoginAttempts(r, user.Username)

	writeJSON(w, http.StatusOK, loginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		User:      toAdminUserResponse(user),
	})
}

const mfaChallengeTTL = 5 * time.Minute

type loginMFARequest struct {
	Challenge string `json:"mfa_challenge"`
	Code      string `json:"code"`
}

func (a *App) handleLoginMFA(w http.ResponseWriter, r *http.Request) {
	var req loginMFARequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	challenge := strings.TrimSpace(req.Challenge)
	code := strings.TrimSpace(req.Code)
	if challenge == "" || code == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "mfa_challenge and code are required"})
		return
	}

	userID, username, err := a.tokens.VerifyMFAChallenge(challenge, a.now().UTC())
	if err != nil {
		a.recordSecurityFailure(r, "admin_login_failed", "invalid_mfa_challenge", "")
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}

	user, err := a.store.GetAdminUserByID(r.Context(), userID)
	if errors.Is(err, storage.ErrUserNotFound) {
		a.recordSecurityFailure(r, "admin_login_failed", "invalid_mfa_challenge", username)
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if user.Disabled || !user.MFAEnabled {
		a.recordSecurityFailure(r, "admin_login_failed", "mfa_not_enrolled", user.Username)
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}

	usedRecovery := false
	if totpStep, ok := auth.MatchTOTPStep(user.MFASecret, code, a.now().UTC()); ok {
		if err := a.store.ConsumeTOTPTimeStep(r.Context(), user.ID, totpStep); err != nil {
			if !errors.Is(err, storage.ErrTOTPCodeReused) {
				a.recordSecurityFailure(r, "admin_login_failed", "totp_code_consume_failed", user.Username)
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			a.recordSecurityFailure(r, "admin_login_failed", "totp_code_reused", user.Username)
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
			return
		}
	} else if id, ok := a.matchRecoveryCode(r.Context(), user.ID, code); ok {
		if err := a.store.ConsumeRecoveryCode(r.Context(), id); err != nil {
			a.recordSecurityFailure(r, "admin_login_failed", "recovery_code_consume_failed", user.Username)
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
			return
		}
		usedRecovery = true
		a.recordSecurityChange(r, "mfa_recovery_code_used", "recovery_code", user.Username)
	} else {
		a.recordSecurityFailure(r, "admin_login_failed", "invalid_mfa_code", user.Username)
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}

	userRole, err := auth.ParseRole(user.Role, auth.RoleAdmin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	token, expiresAt, err := a.tokens.Issue(user.ID, user.Username, userRole, a.now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if usedRecovery {
		a.recordAuditFromRequest(r, "mfa.recovery_code_used", "admin_user", strconv.FormatInt(user.ID, 10), "success", map[string]any{"username": user.Username})
	}
	a.resetFailedLoginAttempts(r, user.Username)

	writeJSON(w, http.StatusOK, loginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		User:      toAdminUserResponse(user),
	})
}

func (a *App) matchRecoveryCode(ctx context.Context, userID int64, code string) (int64, bool) {
	codes, err := a.store.UnusedRecoveryCodeHashes(ctx, userID)
	if err != nil {
		return 0, false
	}
	for _, rec := range codes {
		if auth.MatchRecoveryCode(rec.Hash, code) {
			return rec.ID, true
		}
	}
	return 0, false
}

func (a *App) enforceLoginLockout(w http.ResponseWriter, r *http.Request, username string) bool {
	if a.loginMaxAttempts <= 0 || a.loginLockoutWindow <= 0 {
		return false
	}

	now := a.now().UTC()
	windowStart := now.Add(-a.loginLockoutWindow)
	sourceIP := clientIP(r, a.trustedProxies)

	if sourceIP != "" {
		if stats, err := a.store.CountFailedLoginAttemptsByIP(r.Context(), sourceIP, windowStart); err == nil && stats.Count >= a.loginMaxAttempts {
			a.respondWithLockout(w, r, "ip_lockout", username, stats.OldestAt, now)
			return true
		}
	}
	if username != "" {
		if stats, err := a.store.CountFailedLoginAttemptsByUsername(r.Context(), username, windowStart); err == nil && stats.Count >= a.loginMaxAttempts {
			a.respondWithLockout(w, r, "username_lockout", username, stats.OldestAt, now)
			return true
		}
	}
	return false
}

func (a *App) respondWithLockout(w http.ResponseWriter, r *http.Request, reason string, username string, oldestAt time.Time, now time.Time) {
	a.recordSecurityFailure(r, "admin_login_rate_limited", reason, username)

	retryAfter := int64(1)
	if !oldestAt.IsZero() {
		until := oldestAt.Add(a.loginLockoutWindow).Sub(now)
		if until > 0 {
			seconds := int64(until / time.Second)
			if until%time.Second > 0 {
				seconds++
			}
			if seconds > retryAfter {
				retryAfter = seconds
			}
		}
	}
	w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
	writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "too many login attempts, please try again later"})
}

func (a *App) recordLoginAttempt(r *http.Request, username string, success bool) {
	now := a.now().UTC()
	_ = a.store.RecordLoginAttempt(r.Context(), storage.LoginAttemptParams{
		Username:    username,
		SourceIP:    clientIP(r, a.trustedProxies),
		Success:     success,
		AttemptedAt: now,
	})
	if a.loginLockoutWindow > 0 {
		_, _ = a.store.PruneLoginAttempts(r.Context(), now.Add(-a.loginLockoutWindow*4))
	}
	if a.metrics != nil {
		result := "failure"
		if success {
			result = "success"
		}
		a.metrics.LoginAttempts.WithLabelValues(result).Inc()
	}
}

func (a *App) resetFailedLoginAttempts(r *http.Request, username string) {
	_, _ = a.store.ResetFailedLoginAttempts(r.Context(), username, clientIP(r, a.trustedProxies))
}

func (a *App) handleCurrentUser(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	storedUser, err := a.store.GetAdminUserByID(r.Context(), user.UserID)
	if errors.Is(err, storage.ErrUserNotFound) || storedUser.Disabled {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, toAdminUserResponse(storedUser))
}

func (a *App) handleListAdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListAdminUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	response := make([]adminUserResponse, 0, len(users))
	for _, user := range users {
		response = append(response, toAdminUserResponse(user))
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) handleCreateAdminUser(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeAdminUserRequest(w, r)
	if !ok {
		a.recordAuditFromRequest(r, "admin_user.create", "admin_user", "", "failure", map[string]any{"reason": "invalid_request"})
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		a.recordAuditFromRequest(r, "admin_user.create", "admin_user", "", "failure", map[string]any{"username": req.Username, "reason": "password_hash_failed"})
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	user, err := a.store.CreateAdminUser(r.Context(), storage.CreateAdminUserParams{
		Username:     req.Username,
		PasswordHash: passwordHash,
		Disabled:     req.Disabled,
		Role:         strings.TrimSpace(req.Role),
	})
	if err != nil {
		a.recordAuditFromRequest(r, "admin_user.create", "admin_user", "", "failure", map[string]any{"username": req.Username, "disabled": req.Disabled, "reason": err.Error()})
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	a.recordAuditFromRequest(r, "admin_user.create", "admin_user", strconv.FormatInt(user.ID, 10), "success", map[string]any{"username": user.Username, "disabled": user.Disabled})
	writeJSON(w, http.StatusCreated, toAdminUserResponse(user))
}

func (a *App) handleGetAdminUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	user, err := a.store.GetAdminUserByID(r.Context(), id)
	if errors.Is(err, storage.ErrUserNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "admin user not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, toAdminUserResponse(user))
}

func (a *App) handleUpdateAdminUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	req, ok := decodeAdminUserRequest(w, r)
	if !ok {
		a.recordAuditFromRequest(r, "admin_user.update", "admin_user", strconv.FormatInt(id, 10), "failure", map[string]any{"reason": "invalid_request"})
		return
	}

	var passwordHash *string
	passwordChanged := req.Password != ""
	if passwordChanged {
		hashed, err := auth.HashPassword(req.Password)
		if err != nil {
			a.recordAuditFromRequest(r, "admin_user.update", "admin_user", strconv.FormatInt(id, 10), "failure", map[string]any{"username": req.Username, "reason": "password_hash_failed"})
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		passwordHash = &hashed
	}

	user, err := a.store.UpdateAdminUser(r.Context(), id, storage.UpdateAdminUserParams{
		Username:     req.Username,
		PasswordHash: passwordHash,
		Disabled:     req.Disabled,
		Role:         strings.TrimSpace(req.Role),
	})
	if errors.Is(err, storage.ErrUserNotFound) {
		a.recordAuditFromRequest(r, "admin_user.update", "admin_user", strconv.FormatInt(id, 10), "failure", map[string]any{"reason": "admin user not found"})
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "admin user not found"})
		return
	}
	if errors.Is(err, storage.ErrLastActiveAdmin) {
		a.recordAuditFromRequest(r, "admin_user.update", "admin_user", strconv.FormatInt(id, 10), "failure", map[string]any{"reason": err.Error()})
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		a.recordAuditFromRequest(r, "admin_user.update", "admin_user", strconv.FormatInt(id, 10), "failure", map[string]any{"username": req.Username, "disabled": req.Disabled, "password_changed": passwordChanged, "reason": err.Error()})
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	a.recordAuditFromRequest(r, "admin_user.update", "admin_user", strconv.FormatInt(user.ID, 10), "success", map[string]any{"username": user.Username, "disabled": user.Disabled, "password_changed": passwordChanged})
	writeJSON(w, http.StatusOK, toAdminUserResponse(user))
}

func (a *App) handleDeleteAdminUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	resourceID := strconv.FormatInt(id, 10)

	if err := a.store.DeleteAdminUser(r.Context(), id); errors.Is(err, storage.ErrUserNotFound) {
		a.recordAuditFromRequest(r, "admin_user.delete", "admin_user", resourceID, "failure", map[string]any{"reason": "admin user not found"})
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "admin user not found"})
	} else if errors.Is(err, storage.ErrLastActiveAdmin) {
		a.recordAuditFromRequest(r, "admin_user.delete", "admin_user", resourceID, "failure", map[string]any{"reason": err.Error()})
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	} else if err != nil {
		a.recordAuditFromRequest(r, "admin_user.delete", "admin_user", resourceID, "failure", map[string]any{"reason": err.Error()})
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	} else {
		a.recordAuditFromRequest(r, "admin_user.delete", "admin_user", resourceID, "success", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *App) handleListJSONSchemas(w http.ResponseWriter, r *http.Request) {
	schemas, err := a.store.ListJSONSchemas(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": schemas})
}

func (a *App) handleCreateJSONSchema(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeJSONSchemaRequest(w, r)
	if !ok {
		a.recordAuditFromRequest(r, "json_schema.create", "json_schema", "", "failure", map[string]any{"reason": "invalid_request"})
		return
	}
	definition, err := a.store.CreateJSONSchema(r.Context(), storage.CreateJSONSchemaParams{
		Name:        req.Name,
		TopicFilter: req.TopicFilter,
		Schema:      req.Schema,
		Description: req.Description,
		Enabled:     req.Enabled,
	})
	if err != nil {
		a.recordAuditFromRequest(r, "json_schema.create", "json_schema", "", "failure", map[string]any{"name": req.Name, "topic_filter": req.TopicFilter, "reason": err.Error()})
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	a.schemaCache.invalidate()
	a.recordAuditFromRequest(r, "json_schema.create", "json_schema", strconv.FormatInt(definition.ID, 10), "success", map[string]any{"name": definition.Name, "topic_filter": definition.TopicFilter, "enabled": definition.Enabled})
	writeJSON(w, http.StatusCreated, definition)
}

func (a *App) handleUpdateJSONSchema(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	req, ok := decodeJSONSchemaRequest(w, r)
	if !ok {
		a.recordAuditFromRequest(r, "json_schema.update", "json_schema", strconv.FormatInt(id, 10), "failure", map[string]any{"reason": "invalid_request"})
		return
	}
	definition, err := a.store.UpdateJSONSchema(r.Context(), id, storage.UpdateJSONSchemaParams{
		Name:        req.Name,
		TopicFilter: req.TopicFilter,
		Schema:      req.Schema,
		Description: req.Description,
		Enabled:     req.Enabled,
	})
	if errors.Is(err, sql.ErrNoRows) {
		a.recordAuditFromRequest(r, "json_schema.update", "json_schema", strconv.FormatInt(id, 10), "failure", map[string]any{"reason": "json schema not found"})
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "json schema not found"})
		return
	}
	if err != nil {
		a.recordAuditFromRequest(r, "json_schema.update", "json_schema", strconv.FormatInt(id, 10), "failure", map[string]any{"name": req.Name, "topic_filter": req.TopicFilter, "reason": err.Error()})
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	a.schemaCache.invalidate()
	a.recordAuditFromRequest(r, "json_schema.update", "json_schema", strconv.FormatInt(definition.ID, 10), "success", map[string]any{"name": definition.Name, "topic_filter": definition.TopicFilter, "enabled": definition.Enabled})
	writeJSON(w, http.StatusOK, definition)
}

func (a *App) handleDeleteJSONSchema(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	resourceID := strconv.FormatInt(id, 10)
	if err := a.store.DeleteJSONSchema(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		a.recordAuditFromRequest(r, "json_schema.delete", "json_schema", resourceID, "failure", map[string]any{"reason": "json schema not found"})
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "json schema not found"})
	} else if err != nil {
		a.recordAuditFromRequest(r, "json_schema.delete", "json_schema", resourceID, "failure", map[string]any{"reason": err.Error()})
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	} else {
		a.schemaCache.invalidate()
		a.recordAuditFromRequest(r, "json_schema.delete", "json_schema", resourceID, "success", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeJSONSchemaRequest(w http.ResponseWriter, r *http.Request) (jsonSchemaRequest, bool) {
	var req jsonSchemaRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return req, false
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return req, false
	}
	return req, true
}

func (a *App) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	events, err := a.store.ListAuditEvents(r.Context(), storage.AuditEventQuery{Limit: limit, Offset: offset})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "limit": limit, "offset": offset})
}

func (a *App) handleSecurityEvents(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		limit = parsedLimit
	}

	events, err := a.store.ListSecurityEvents(r.Context(), storage.SecurityEventQuery{Limit: limit})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
func (a *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			a.recordSecurityFailure(r, "protected_api_access_failed", "missing_bearer_token", "")
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
			return
		}

		claims, err := a.tokens.VerifyAt(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), a.now().UTC())
		if err != nil {
			a.recordSecurityFailure(r, "protected_api_access_failed", "invalid_bearer_token", "")
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
			return
		}
		user, err := a.loadCurrentUser(r.Context(), claims)
		if errors.Is(err, storage.ErrUserNotFound) {
			a.recordSecurityFailure(r, "protected_api_access_failed", "inactive_user", claims.Username)
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		claims.Username = user.Username
		claims.Role = auth.Role(user.Role)

		ctx := context.WithValue(r.Context(), currentUserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireRole authenticates the request and verifies that the token role meets the minimum required.
// Returns 401 when the bearer token is missing/invalid (consistent with requireAuth) and 403 when the
// role is insufficient. Both paths record a security event so audit trails capture rejected access.
func (a *App) requireRole(min auth.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			a.recordSecurityFailure(r, "protected_api_access_failed", "missing_bearer_token", "")
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
			return
		}
		claims, err := a.tokens.VerifyAt(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), a.now().UTC())
		if err != nil {
			a.recordSecurityFailure(r, "protected_api_access_failed", "invalid_bearer_token", "")
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
			return
		}
		user, err := a.loadCurrentUser(r.Context(), claims)
		if errors.Is(err, storage.ErrUserNotFound) {
			a.recordSecurityFailure(r, "protected_api_access_failed", "inactive_user", claims.Username)
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		claims.Username = user.Username
		claims.Role = auth.Role(user.Role)
		if !claims.Role.AtLeast(min) {
			a.recordSecurityFailure(r, "protected_api_access_denied", "insufficient_role", claims.Username)
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "insufficient role"})
			return
		}
		ctx := context.WithValue(r.Context(), currentUserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *App) optionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(header, "Bearer ") {
			if claims, err := a.tokens.VerifyAt(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), a.now().UTC()); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), currentUserContextKey, claims))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) recordAuditFromRequest(r *http.Request, action string, resourceType string, resourceID string, result string, metadata map[string]any) {
	actor := "anonymous"
	if claims, ok := currentUserFromContext(r.Context()); ok && strings.TrimSpace(claims.Username) != "" {
		actor = claims.Username
	}
	payload, err := json.Marshal(sanitizeAuditMetadata(metadata))
	if err != nil {
		payload = []byte(`{}`)
	}
	now := a.now().UTC()
	_, _ = a.store.RecordAuditEvent(r.Context(), storage.CreateAuditEventParams{
		OccurredAt:   now,
		Actor:        actor,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Result:       result,
		Metadata:     payload,
	})
	if a.auditRetention > 0 {
		_, _ = a.store.PruneAuditEvents(r.Context(), now.Add(-a.auditRetention))
	}
	if a.metrics != nil {
		safeResult := strings.TrimSpace(result)
		if safeResult == "" {
			safeResult = "unknown"
		}
		a.metrics.AuditEvents.WithLabelValues(safeResult).Inc()
	}
}

func sanitizeAuditMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	safe := make(map[string]any, len(metadata))
	for key, value := range metadata {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "jwt") || strings.Contains(lower, "secret") {
			continue
		}
		safe[key] = value
	}
	return safe
}

func (a *App) recordSecurityFailure(r *http.Request, category string, reason string, username string) {
	a.recordSecurityEvent(r, category, reason, username)
}

func (a *App) recordSecurityChange(r *http.Request, category string, reason string, _ string) {
	a.recordSecurityEvent(r, category, reason, "")
}

func (a *App) recordSecurityEvent(r *http.Request, category string, reason string, username string) {
	observedAt := a.now().UTC()
	if a.metrics != nil {
		safeCategory := strings.TrimSpace(category)
		if safeCategory == "" {
			safeCategory = "unknown"
		}
		a.metrics.SecurityEvents.WithLabelValues(safeCategory).Inc()
	}
	_, _ = a.store.RecordSecurityEvent(r.Context(), storage.CreateSecurityEventParams{
		Category:   category,
		Reason:     reason,
		Username:   username,
		SourceIP:   clientIP(r, a.trustedProxies),
		Method:     r.Method,
		Path:       r.URL.Path,
		ObservedAt: observedAt,
	})
	if a.securityRetention > 0 {
		_, _ = a.store.PruneSecurityEvents(r.Context(), observedAt.Add(-a.securityRetention))
	}
	a.alerts.Enqueue(alerting.WebhookAlert{
		Type:       "security_event",
		Severity:   "warning",
		Source:     "http_api",
		Message:    fmt.Sprintf("Security event: %s (%s)", category, reason),
		ObservedAt: observedAt,
		Details: map[string]any{
			"category":  category,
			"reason":    reason,
			"username":  username,
			"source_ip": clientIP(r, a.trustedProxies),
			"method":    r.Method,
			"path":      r.URL.Path,
		},
	})
}

func currentUserFromContext(ctx context.Context) (auth.UserClaims, bool) {
	claims, ok := ctx.Value(currentUserContextKey).(auth.UserClaims)
	return claims, ok
}

func decodeAdminUserRequest(w http.ResponseWriter, r *http.Request) (adminUserRequest, bool) {
	var req adminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return adminUserRequest{}, false
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return adminUserRequest{}, false
	}
	if strings.TrimSpace(req.Username) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "username is required"})
		return adminUserRequest{}, false
	}
	if r.Method == http.MethodPost && req.Password == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "password is required"})
		return adminUserRequest{}, false
	}
	if strings.TrimSpace(req.Role) != "" && !auth.Role(strings.TrimSpace(req.Role)).Valid() {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `role must be one of "viewer", "auditor", "operator", "admin"`})
		return adminUserRequest{}, false
	}
	return req, true
}

func parseIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid admin user id"})
		return 0, false
	}
	return id, true
}

func toAdminUserResponse(user storage.AdminUser) adminUserResponse {
	return adminUserResponse{
		ID:        user.ID,
		Username:  user.Username,
		Disabled:  user.Disabled,
		Role:      user.Role,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
