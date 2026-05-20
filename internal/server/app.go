package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/storage"
)

type contextKey string

const currentUserContextKey contextKey = "current_user"

// App wires the HTTP API to storage and auth dependencies.
type App struct {
	store        *storage.Store
	aclStore     acl.Store
	tokens       *auth.TokenManager
	brokerEvents *BrokerEventHub
	now          func() time.Time
}

// New creates an HTTP app configured for the auth MVP.
func New(cfg config.Config, store *storage.Store) (*App, error) {
	ttl, err := time.ParseDuration(cfg.Auth.TokenTTL)
	if err != nil {
		return nil, fmt.Errorf("parse auth token ttl: %w", err)
	}
	metricsRetention, err := time.ParseDuration(cfg.Metrics.BrokerRetention)
	if err != nil {
		return nil, fmt.Errorf("parse broker metrics retention: %w", err)
	}

	brokerEvents := NewBrokerEventHub()
	brokerEvents.SetPersistence(store, metricsRetention)

	return &App{
		store:        store,
		aclStore:     acl.NewMemoryStore(),
		tokens:       auth.NewTokenManager(cfg.Auth.JWTSecret, ttl),
		brokerEvents: brokerEvents,
		now:          time.Now,
	}, nil
}

// BootstrapAdmin creates the configured bootstrap admin if no admin users exist yet.
func (a *App) BootstrapAdmin(ctx context.Context, cfg config.Config) error {
	count, err := a.store.CountAdminUsers(ctx)
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
	})
	if err != nil {
		return fmt.Errorf("create bootstrap admin user: %w", err)
	}

	return nil
}

// Handler returns the configured HTTP handler tree.
func (a *App) Handler() http.Handler {
	aclAPI := &aclAPI{store: a.aclStore}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", aclAPI.handleHealthz)
	mux.HandleFunc("GET /api/v1/acls", aclAPI.handleListRules)
	mux.HandleFunc("POST /api/v1/acls", aclAPI.handleCreateRule)
	mux.HandleFunc("PUT /api/v1/acls/{id}", aclAPI.handleUpdateRule)
	mux.HandleFunc("DELETE /api/v1/acls/{id}", aclAPI.handleDeleteRule)
	mux.HandleFunc("POST /api/v1/auth/login", a.handleLogin)
	mux.Handle("GET /api/v1/auth/me", a.requireAuth(http.HandlerFunc(a.handleCurrentUser)))
	mux.Handle("GET /api/v1/admin-users", a.requireAuth(http.HandlerFunc(a.handleListAdminUsers)))
	mux.Handle("POST /api/v1/admin-users", a.requireAuth(http.HandlerFunc(a.handleCreateAdminUser)))
	mux.Handle("GET /api/v1/admin-users/{id}", a.requireAuth(http.HandlerFunc(a.handleGetAdminUser)))
	mux.Handle("PUT /api/v1/admin-users/{id}", a.requireAuth(http.HandlerFunc(a.handleUpdateAdminUser)))
	mux.Handle("DELETE /api/v1/admin-users/{id}", a.requireAuth(http.HandlerFunc(a.handleDeleteAdminUser)))
	mux.HandleFunc("GET /api/v1/broker/events", a.handleBrokerEvents)
	return mux
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Disabled bool   `json:"disabled"`
}

type loginResponse struct {
	Token     string            `json:"token"`
	ExpiresAt time.Time         `json:"expires_at"`
	User      adminUserResponse `json:"user"`
}

type adminUserResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "username and password are required"})
		return
	}

	user, err := a.store.GetAdminUserByUsername(r.Context(), req.Username)
	if errors.Is(err, storage.ErrUserNotFound) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if user.Disabled {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "user is disabled"})
		return
	}

	match, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !match {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}

	token, expiresAt, err := a.tokens.Issue(user.ID, user.Username, a.now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		User:      toAdminUserResponse(user),
	})
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
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	user, err := a.store.CreateAdminUser(r.Context(), storage.CreateAdminUserParams{
		Username:     req.Username,
		PasswordHash: passwordHash,
		Disabled:     req.Disabled,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

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
		return
	}

	var passwordHash *string
	if req.Password != "" {
		hashed, err := auth.HashPassword(req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		passwordHash = &hashed
	}

	user, err := a.store.UpdateAdminUser(r.Context(), id, storage.UpdateAdminUserParams{
		Username:     req.Username,
		PasswordHash: passwordHash,
		Disabled:     req.Disabled,
	})
	if errors.Is(err, storage.ErrUserNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "admin user not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, toAdminUserResponse(user))
}

func (a *App) handleDeleteAdminUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	if err := a.store.DeleteAdminUser(r.Context(), id); errors.Is(err, storage.ErrUserNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "admin user not found"})
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
			return
		}

		claims, err := a.tokens.VerifyAt(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), a.now().UTC())
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
			return
		}

		ctx := context.WithValue(r.Context(), currentUserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func currentUserFromContext(ctx context.Context) (auth.UserClaims, bool) {
	claims, ok := ctx.Value(currentUserContextKey).(auth.UserClaims)
	return claims, ok
}

func decodeAdminUserRequest(w http.ResponseWriter, r *http.Request) (adminUserRequest, bool) {
	var req adminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
