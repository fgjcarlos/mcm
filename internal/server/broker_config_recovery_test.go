package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
)

func TestBrokerConfigRecoveryAndCopies(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(t.TempDir(), "mosquitto.conf")
	original := "# original\nmax_connections 20\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cat := loadTestCatalog(t)
	svc := deploy.NewService(nil, nil, store, store, nil, nil, config.MosquittoConfig{}, config.DeployConfig{Mode: "file"}, nil).WithBrokerConfig(path, cat)
	app.brokerCfgSvc, app.brokerCfgCatalog = svc, cat
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	proof := recentMFATestProof(t, app, token)
	call := func(method, url, body string, withMFA bool) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := authedRequest(method, url, body, token)
		if withMFA {
			req.Header.Set("X-MCM-MFA", proof)
		}
		app.Handler().ServeHTTP(rec, req)
		return rec
	}
	imported := call("POST", "/api/v1/broker/config/import", "{}", false)
	var initial deploy.PreviewResult
	if imported.Code != 200 || json.Unmarshal(imported.Body.Bytes(), &initial) != nil {
		t.Fatal(imported.Body.String())
	}
	history := call("GET", "/api/v1/broker/config/revisions", "", false)
	if history.Code != 200 {
		t.Fatalf("history: %d %s", history.Code, history.Body.String())
	}
	var listed struct {
		Revisions []struct {
			ID string `json:"id"`
		} `json:"revisions"`
	}
	if json.Unmarshal(history.Body.Bytes(), &listed) != nil || len(listed.Revisions) != 1 || listed.Revisions[0].ID != initial.RevisionID {
		t.Fatalf("history: %s", history.Body.String())
	}
	exported := call("GET", "/api/v1/broker/config/copy", "", true)
	if exported.Code != 200 || exported.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export: %d %s", exported.Code, exported.Body.String())
	}
	if denied := call("POST", "/api/v1/broker/config/copy", exported.Body.String(), false); denied.Code != 403 {
		t.Fatalf("copy import without MFA: %d", denied.Code)
	}
	copied := call("POST", "/api/v1/broker/config/copy", exported.Body.String(), true)
	if copied.Code != 200 {
		t.Fatalf("copy import: %d %s", copied.Code, copied.Body.String())
	}
	recovered := call("POST", "/api/v1/broker/config/revisions/"+initial.RevisionID+"/recover", `{"base_hash":"`+initial.BaseConfHash+`"}`, true)
	var fresh deploy.PreviewResult
	if recovered.Code != 200 || json.Unmarshal(recovered.Body.Bytes(), &fresh) != nil || fresh.RevisionID == initial.RevisionID || fresh.ConfBody != original {
		t.Fatalf("recovery: %d %s", recovered.Code, recovered.Body.String())
	}
	bad := call("POST", "/api/v1/broker/config/copy", `{"format":"unknown","body":"garbage"}`, true)
	if bad.Code != 422 {
		t.Fatalf("unknown copy format accepted: %d %s", bad.Code, bad.Body.String())
	}
	before := call("GET", "/api/v1/broker/config/revisions", "", false).Body.String()
	bad = call("POST", "/api/v1/broker/config/copy", `{"format":"mcm-broker-config/v1","body":"max_connections 30\n","hash":"tampered"}`, true)
	if bad.Code != 422 {
		t.Fatalf("corrupt copy accepted: %d %s", bad.Code, bad.Body.String())
	}
	after := call("GET", "/api/v1/broker/config/revisions", "", false).Body.String()
	if before != after {
		t.Fatal("rejected copy changed revision history")
	}
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != original {
		t.Fatalf("preview/copy/recovery changed disk: %q %v", disk, err)
	}
	// Auditor can inspect metadata but cannot restore/export, even with a proof.
	seedAdminUserWithRole(t, store, "auditor", "secret", auth.RoleAuditor)
	token = loginAs(t, app, "auditor", "secret")
	if r := call("GET", "/api/v1/broker/config/revisions", "", false); r.Code != 200 {
		t.Fatalf("auditor history: %d", r.Code)
	}
	if r := call("GET", "/api/v1/broker/config/copy", "", true); r.Code != 403 {
		t.Fatalf("auditor export: %d", r.Code)
	}
}
