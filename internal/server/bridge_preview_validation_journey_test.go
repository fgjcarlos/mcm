package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
)

func TestBridgePreviewRejectsDirectiveInjectionWithoutChangingState(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	root := t.TempDir()
	path := filepath.Join(root, "mosquitto.conf")
	original := "# managed bridge config\nlistener 1883 127.0.0.1\nallow_anonymous true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := loadTestCatalog(t)
	svc := deploy.NewService(nil, nil, store, store, nil, nil, config.MosquittoConfig{}, config.DeployConfig{Mode: "file"}, nil).
		WithBrokerConfig(path, catalog)
	app.bridgeSvc = svc
	app.deploySvc = svc
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	token := loginAs(t, app, "operator", "secret")

	request := func(method, endpoint, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, authedRequest(method, endpoint, body, token))
		return rec
	}
	assertNoDeployments := func(stage string) {
		t.Helper()
		rec := request(http.MethodGet, "/api/v1/deployments", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s deployment list: %d %s", stage, rec.Code, rec.Body.String())
		}
		var body struct {
			Deployments []json.RawMessage `json:"deployments"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s deployment list JSON: %v", stage, err)
		}
		if len(body.Deployments) != 0 {
			t.Errorf("%s created %d deployments, want none", stage, len(body.Deployments))
		}
	}

	assertNoDeployments("before preview")
	const secret = "private-bridge-secret\r\nallow_anonymous true"
	payload, err := json.Marshal(map[string]any{
		"bridges": []any{map[string]any{
			"name":     "upstream",
			"address":  "remote.example.org:1883",
			"username": "bridge-user",
			"password": secret,
			"topics": []any{map[string]any{
				"topic": "bridge/#", "direction": "both", "qos": 1,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := request(http.MethodPost, "/api/v1/broker/bridges/preview", string(payload))
	if preview.Code != http.StatusBadRequest {
		t.Errorf("preview status = %d, want 400; body = %s", preview.Code, preview.Body.String())
	}
	if strings.Contains(preview.Body.String(), "private-bridge-secret") {
		t.Errorf("preview response leaked the bridge password: %s", preview.Body.String())
	}
	if strings.Contains(preview.Body.String(), `"conf_body"`) {
		t.Errorf("rejected preview returned rendered config: %s", preview.Body.String())
	}
	if strings.Contains(preview.Body.String(), `"revision_id"`) {
		t.Errorf("rejected preview returned a revision: %s", preview.Body.String())
	}
	assertNoDeployments("after rejected preview")

	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != original {
		t.Errorf("rejected preview changed config: got %q, want %q", disk, original)
	}
}
