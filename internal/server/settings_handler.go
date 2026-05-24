package server

import (
	"net/http"
)

// settingsHTTPResponse is the safe subset of HTTP config exposed by /api/v1/settings.
type settingsHTTPResponse struct {
	BindAddress string `json:"bind_address"`
	Port        int    `json:"port"`
	TLSEnabled  bool   `json:"tls_enabled"`
}

// settingsMosquittoResponse is the safe subset of Mosquitto config exposed by /api/v1/settings.
type settingsMosquittoResponse struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// settingsDeployResponse is the safe subset of deploy config exposed by /api/v1/settings.
type settingsDeployResponse struct {
	Mode string `json:"mode"`
}

// settingsResponse is the JSON body returned by GET /api/v1/settings.
// Sensitive fields (JWT secret, bootstrap password, DSN) are intentionally absent.
type settingsResponse struct {
	HTTP      settingsHTTPResponse      `json:"http"`
	Mosquitto settingsMosquittoResponse `json:"mosquitto"`
	Deploy    settingsDeployResponse    `json:"deploy"`
}

// handleSettings handles GET /api/v1/settings.
// It returns a redacted view of the running configuration.
// JWT secret, bootstrap admin credentials, TLS key paths, and database DSN are never included.
func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	cfg := a.cfg

	protocol := "mqtt"
	if cfg.Mosquitto.TLS.Enabled {
		protocol = "mqtts"
	}

	resp := settingsResponse{
		HTTP: settingsHTTPResponse{
			BindAddress: cfg.HTTP.BindAddress,
			Port:        cfg.HTTP.Port,
			TLSEnabled:  cfg.HTTP.TLS.Enabled,
		},
		Mosquitto: settingsMosquittoResponse{
			Host:     cfg.Mosquitto.Host,
			Port:     cfg.Mosquitto.Port,
			Protocol: protocol,
		},
		Deploy: settingsDeployResponse{
			Mode: cfg.Mosquitto.Deploy.Mode,
		},
	}

	writeJSON(w, http.StatusOK, resp)
}
