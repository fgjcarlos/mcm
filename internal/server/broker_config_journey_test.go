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

func TestBrokerConfigEditablePreview(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	root := t.TempDir()
	path := filepath.Join(root, "mosquitto.conf")
	original := "# existing broker\nmax_connections 20\ninclude_dir parts\n"
	if err := os.Mkdir(filepath.Join(root, "parts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "parts", "listener.conf"), []byte("listener 1883\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cat := loadTestCatalog(t)
	svc := deploy.NewService(nil, nil, store, store, nil, nil, config.MosquittoConfig{}, config.DeployConfig{Mode: "file"}, nil).WithBrokerConfig(path, cat)
	app.brokerCfgSvc, app.brokerCfgCatalog, app.deploySvc = svc, cat, svc
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	token := loginAs(t, app, "operator", "secret")
	request := func(method, url, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, authedRequest(method, url, body, token))
		return rec
	}
	current := request("GET", "/api/v1/broker/config", "")
	if current.Code != 200 {
		t.Fatal(current.Body.String())
	}
	var state struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(current.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	draft := strings.Replace(original, "20", "30", 1)
	payload, _ := json.Marshal(map[string]string{"body": draft, "base_hash": state.Hash})
	preview := request("POST", "/api/v1/broker/config/preview", string(payload))
	if preview.Code != 200 {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var p deploy.PreviewResult
	if err := json.Unmarshal(preview.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.ConfBody != draft || !p.HasChanges || p.RevisionID == "" || !strings.Contains(p.ConfDiff, "+max_connections 30") {
		t.Fatalf("edited preview not preserved: %+v", p)
	}
	if strings.Contains(p.ConfBody, "listener 1883") {
		t.Fatal("include expanded into root and would be loaded twice")
	}
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != original {
		t.Fatalf("preview changed disk: %q %v", disk, err)
	}
	// Previous no-body import behavior must remain available, but include bodies
	// must not be copied into the root configuration.
	imported := request("POST", "/api/v1/broker/config/import", "{}")
	var old deploy.PreviewResult
	if imported.Code != 200 || json.Unmarshal(imported.Body.Bytes(), &old) != nil || old.ConfBody != original || old.HasChanges {
		t.Fatalf("import round trip: %d %s", imported.Code, imported.Body.String())
	}
	if err := os.WriteFile(path, []byte(original+"# external edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := request("POST", "/api/v1/broker/config/preview", string(payload))
	if stale.Code != http.StatusConflict || stale.Body.String() != "{\"error\":\"preview revision base no longer matches the on-disk configuration\"}\n" {
		t.Fatalf("stale draft accepted: %d %s", stale.Code, stale.Body.String())
	}
	invalid := request("POST", "/api/v1/broker/config/preview", `{"body":"max_connections nope\n"}`)
	if invalid.Code != http.StatusUnprocessableEntity || !strings.Contains(invalid.Body.String(), "validation_issues") || strings.Contains(invalid.Body.String(), `"revision_id":"`) {
		t.Fatalf("invalid draft accepted: %d %s", invalid.Code, invalid.Body.String())
	}
	after := request("GET", "/api/v1/deployments", "")
	if after.Code != 200 || !strings.Contains(after.Body.String(), `"deployments":[]`) {
		t.Fatalf("rejected requests created deployments: %d %s", after.Code, after.Body.String())
	}
	disk, err = os.ReadFile(path)
	if err != nil || string(disk) != original+"# external edit\n" {
		t.Fatalf("rejection changed disk: %q %v", disk, err)
	}
	if err := os.WriteFile(path, []byte("remote_password private-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	protected := request("GET", "/api/v1/broker/config", "")
	if protected.Code != 409 || strings.Contains(protected.Body.String(), "private-value") {
		t.Fatalf("inline credential leaked: %d %s", protected.Code, protected.Body.String())
	}
}
