package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// mqttUserResponse is the JSON representation of an MQTT user (no password material).
type mqttUserResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// mqttUserWithPasswordResponse extends mqttUserResponse with a cleartext password.
// It is returned only on create and reset-password operations.
type mqttUserWithPasswordResponse struct {
	mqttUserResponse
	Password string `json:"password"`
}

// createMQTTUserRequest is the JSON body for POST /api/v1/mqtt-users.
type createMQTTUserRequest struct {
	Username string `json:"username"`
}

// updateMQTTUserRequest is the JSON body for PUT /api/v1/mqtt-users/{id}.
type updateMQTTUserRequest struct {
	Username *string `json:"username,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

// toMQTTUserResponse converts a storage.MQTTUser to the HTTP response shape.
func toMQTTUserResponse(u storage.MQTTUser) mqttUserResponse {
	return mqttUserResponse{
		ID:        u.ID,
		Username:  u.Username,
		Disabled:  u.Disabled,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// generateMQTTPassword returns a 24-character URL-safe random password.
func generateMQTTPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf)[:24], nil
}

// handleCreateMQTTUser handles POST /api/v1/mqtt-users.
func (a *App) handleCreateMQTTUser(w http.ResponseWriter, r *http.Request) {
	var req createMQTTUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "username is required"})
		return
	}

	password, err := generateMQTTPassword()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	hash, err := mosquitto.HashPassword(password, mosquitto.DefaultIterations)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	u, err := a.store.CreateMQTTUser(r.Context(), storage.CreateMQTTUserParams{
		Username:     strings.TrimSpace(req.Username),
		PasswordHash: hash,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "username already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	a.recordAuditFromRequest(r, "mqtt_user.create", "mqtt_user", strconv.FormatInt(u.ID, 10), "success", map[string]any{"username": u.Username})
	writeJSON(w, http.StatusCreated, mqttUserWithPasswordResponse{
		mqttUserResponse: toMQTTUserResponse(u),
		Password:         password,
	})
}

// handleListMQTTUsers handles GET /api/v1/mqtt-users.
func (a *App) handleListMQTTUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListMQTTUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	response := make([]mqttUserResponse, 0, len(users))
	for _, u := range users {
		response = append(response, toMQTTUserResponse(u))
	}
	writeJSON(w, http.StatusOK, response)
}

// handleGetMQTTUser handles GET /api/v1/mqtt-users/{id}.
func (a *App) handleGetMQTTUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	u, err := a.store.GetMQTTUser(r.Context(), id)
	if errors.Is(err, storage.ErrMQTTUserNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "mqtt user not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, toMQTTUserResponse(u))
}

// handleUpdateMQTTUser handles PUT /api/v1/mqtt-users/{id}.
func (a *App) handleUpdateMQTTUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var req updateMQTTUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	params := storage.UpdateMQTTUserParams{
		Username: req.Username,
		Disabled: req.Disabled,
	}

	u, err := a.store.UpdateMQTTUser(r.Context(), id, params)
	if errors.Is(err, storage.ErrMQTTUserNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "mqtt user not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	a.recordAuditFromRequest(r, "mqtt_user.update", "mqtt_user", strconv.FormatInt(u.ID, 10), "success", map[string]any{"username": u.Username, "disabled": u.Disabled})
	writeJSON(w, http.StatusOK, toMQTTUserResponse(u))
}

// handleDeleteMQTTUser handles DELETE /api/v1/mqtt-users/{id}.
func (a *App) handleDeleteMQTTUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	resourceID := strconv.FormatInt(id, 10)

	if err := a.store.DeleteMQTTUser(r.Context(), id); errors.Is(err, storage.ErrMQTTUserNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "mqtt user not found"})
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	} else {
		a.recordAuditFromRequest(r, "mqtt_user.delete", "mqtt_user", resourceID, "success", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleResetMQTTUserPassword handles POST /api/v1/mqtt-users/{id}/reset-password.
func (a *App) handleResetMQTTUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	u, err := a.store.GetMQTTUser(r.Context(), id)
	if errors.Is(err, storage.ErrMQTTUserNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "mqtt user not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	password, err := generateMQTTPassword()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	hash, err := mosquitto.HashPassword(password, mosquitto.DefaultIterations)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	u, err = a.store.UpdateMQTTUser(r.Context(), id, storage.UpdateMQTTUserParams{
		PasswordHash: &hash,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	a.recordAuditFromRequest(r, "mqtt_user.reset_password", "mqtt_user", strconv.FormatInt(u.ID, 10), "success", map[string]any{"username": u.Username})
	writeJSON(w, http.StatusOK, mqttUserWithPasswordResponse{
		mqttUserResponse: toMQTTUserResponse(u),
		Password:         password,
	})
}
