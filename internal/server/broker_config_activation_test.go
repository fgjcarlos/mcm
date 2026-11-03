package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

type configTestApplier struct {
	reloads  int
	failNext bool
}

func (a *configTestApplier) Apply(context.Context, mosquitto.ApplyRequest) error { return nil }
func (a *configTestApplier) ReloadBrokerOnly() error {
	a.reloads++
	if a.failNext {
		a.failNext = false
		return errors.New("reload failed")
	}
	return nil
}

// configTestVerifier reports the broker's live access policy by reading the
// effective configuration, so an unverified apply is never presented as
// verified: the probe succeeds only when the file carries the expected
// marker, which is how the broker serves the applied revision.
type configTestVerifier struct {
	path  string
	calls int
}

// brokenPolicyMarker marks a configuration the broker does not serve as
// expected: while it is on disk the probe fails.
const brokenPolicyMarker = "# broken-policy"

func (v *configTestVerifier) VerifyActive(context.Context, diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult {
	v.calls++
	body, _ := os.ReadFile(v.path)
	ok := !strings.Contains(string(body), brokenPolicyMarker)
	return diagnostics.VerifyActiveResult{OK: ok, Message: "broker policy verification"}
}

func TestBrokerConfigApplyWithoutVerificationSubject(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	root := t.TempDir()
	path := filepath.Join(root, "mosquitto.conf")
	original := "max_connections 20\nlistener 1883\nconnection_messages true\n"
	for name, body := range map[string]string{"mosquitto.conf": original, "passwd": "probe:hash\n", "acl": "user probe\ntopic readwrite probe/allowed\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cat := loadTestCatalog(t)
	applier := &configTestApplier{}
	verifier := &configTestVerifier{path: path}
	// No managed MQTT user with a known password exists, so MCM cannot
	// observe MQTT access for this deployment.
	svc := deploy.NewService(applier, store.ACLStore(), store, store, verifier, app, config.MosquittoConfig{}, config.DeployConfig{Mode: "file", ACLPath: filepath.Join(root, "acl"), PasswdPath: filepath.Join(root, "passwd")}, nil).WithBrokerConfig(path, cat)
	app.brokerCfgSvc, app.brokerCfgCatalog = svc, cat
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	proof := recentMFATestProof(t, app, token)
	payload, _ := json.Marshal(map[string]string{"body": strings.Replace(original, "connection_messages true", "connection_messages false", 1)})
	rec := httptest.NewRecorder()
	req := authedRequest("POST", "/api/v1/broker/config/preview", string(payload), token)
	req.Header.Set("X-MCM-MFA", proof)
	app.Handler().ServeHTTP(rec, req)
	var preview deploy.PreviewResult
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &preview) != nil {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	applied := httptest.NewRecorder()
	applyReq := authedRequest("POST", "/api/v1/broker/config/apply", `{"revision_id":"`+preview.RevisionID+`","adopt":true}`, token)
	applyReq.Header.Set("X-MCM-MFA", proof)
	app.Handler().ServeHTTP(applied, applyReq)
	var result storage.Deployment
	if applied.Code != 200 || json.Unmarshal(applied.Body.Bytes(), &result) != nil {
		t.Fatalf("apply: %d %s", applied.Code, applied.Body.String())
	}
	if result.Status != "pending_activation" {
		t.Fatalf("unverifiable apply claimed %q instead of pending_activation", result.Status)
	}
	if !strings.Contains(result.Message, "verification unavailable") {
		t.Fatalf("apply did not explain the missing verification: %q", result.Message)
	}
	if verifier.calls != 0 {
		t.Fatalf("verification ran without a usable subject: %d calls", verifier.calls)
	}
	// The change was written and the reload signalled, so the disk state
	// must reflect the applied revision.
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != strings.Replace(original, "connection_messages true", "connection_messages false", 1) {
		t.Fatalf("apply did not write the reviewed revision: %q %v", disk, err)
	}
}

func TestBrokerConfigApplyVerificationAndRecovery(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	root := t.TempDir()
	path := filepath.Join(root, "mosquitto.conf")
	original := "max_connections 20\nlistener 1883\ninclude_dir parts\nconnection_messages true\n"
	if err := os.Mkdir(filepath.Join(root, "parts"), 0700); err != nil {
		t.Fatal(err)
	}
	included := filepath.Join(root, "parts", "logging.conf")
	if err := os.WriteFile(included, []byte("log_dest stderr\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"mosquitto.conf": original, "passwd": "probe:hash\n", "acl": "user probe\ntopic readwrite probe/allowed\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cat := loadTestCatalog(t)
	applier := &configTestApplier{}
	verifier := &configTestVerifier{path: path}
	svc := deploy.NewService(applier, store.ACLStore(), store, store, verifier, app, config.MosquittoConfig{}, config.DeployConfig{Mode: "file", ACLPath: filepath.Join(root, "acl"), PasswdPath: filepath.Join(root, "passwd")}, nil).WithBrokerConfig(path, cat)
	app.brokerCfgSvc, app.brokerCfgCatalog, app.deploySvc = svc, cat, svc
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	proof := recentMFATestProof(t, app, token)
	call := func(url, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := authedRequest("POST", url, body, token)
		req.Header.Set("X-MCM-MFA", proof)
		app.Handler().ServeHTTP(rec, req)
		return rec
	}
	created := call("/api/v1/mqtt-users", `{"username":"probe"}`)
	if created.Code != 201 {
		t.Fatalf("create verification subject: %d %s", created.Code, created.Body.String())
	}
	preview := func(body string) deploy.PreviewResult {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"body": body})
		rec := call("/api/v1/broker/config/preview", string(payload))
		var p deploy.PreviewResult
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &p) != nil {
			t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
		}
		return p
	}
	apply := func(p deploy.PreviewResult) storage.Deployment {
		t.Helper()
		rec := call("/api/v1/broker/config/apply", `{"revision_id":"`+p.RevisionID+`","adopt":true}`)
		var result storage.Deployment
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
			t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
		}
		return result
	}
	p := preview(strings.Replace(original, "connection_messages true", "connection_messages false", 1))
	if p.RequiresRestart {
		t.Fatal("unchanged listener made reload-only edit require restart")
	}
	good := apply(p)
	if good.Status != "active_verified" || good.RevisionID != p.RevisionID || verifier.calls == 0 {
		t.Fatalf("unverified apply: %+v, probes %d", good, verifier.calls)
	}
	observed := httptest.NewRecorder()
	app.Handler().ServeHTTP(observed, authedRequest("GET", "/api/v1/broker/config", "", token))
	var state deploy.GetBrokerConfigResult
	if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &state) != nil || state.LastVerified == nil || state.LastVerified.RevisionID != p.RevisionID || state.ExternalDrift || state.BrokerVersion != "unknown" || state.Capabilities["restart"] || !state.Capabilities["verify_mqtt"] {
		t.Fatalf("observed state/capabilities: %d %s", observed.Code, observed.Body.String())
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("apply broadened file permissions: %v %v", info, err)
	}
	stale := preview(original)
	if err := os.WriteFile(included, []byte("log_dest stdout\n"), 0600); err != nil {
		t.Fatal(err)
	}
	observed = httptest.NewRecorder()
	app.Handler().ServeHTTP(observed, authedRequest("GET", "/api/v1/broker/config", "", token))
	if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &state) != nil || !state.ExternalDrift {
		t.Fatalf("include drift not visible: %d %s", observed.Code, observed.Body.String())
	}
	rejected := call("/api/v1/broker/config/apply", `{"revision_id":"`+stale.RevisionID+`","adopt":true}`)
	if rejected.Code != 409 {
		t.Fatalf("included file drift accepted: %d %s", rejected.Code, rejected.Body.String())
	}
	if err := os.WriteFile(included, []byte("log_dest stderr\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A failed verifier restores and reloads the previous bytes, verifies the
	// restored broker and reports the failed candidate, not a successful apply.
	failed := apply(preview(strings.Replace(original, "connection_messages true", "connection_messages false", 1) + brokenPolicyMarker + "\n"))
	disk, err := os.ReadFile(path)
	if failed.Status != "rolled_back" || err != nil || string(disk) != strings.Replace(original, "connection_messages true", "connection_messages false", 1) {
		t.Fatalf("rollback: %+v %q %v", failed, disk, err)
	}
	applier.failNext = true
	reloadFailed := apply(preview(original))
	if reloadFailed.Status != "rolled_back" {
		t.Fatalf("reload rollback not recorded: %+v", reloadFailed)
	}
}
