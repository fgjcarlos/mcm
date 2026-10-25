package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// newScopedTestApp builds an app whose deploy configuration points at an
// ACL file inside a temporary directory, so the scope paths are stable.
func newScopedTestApp(t *testing.T) (*App, *storage.Store, string) {
	t.Helper()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	app, store := newTestApp(t, func(cfg *config.Config) {
		cfg.Mosquitto.Deploy.Mode = "file"
		cfg.Mosquitto.Deploy.ACLPath = aclPath
		cfg.Mosquitto.Deploy.PasswdPath = filepath.Join(dir, "passwd")
	})
	return app, store, aclPath
}

// TestCreateRuleRejectsUnknownFields pins that the rule request refuses an
// unknown field, so a client cannot silently set a scope MCM ignores.
func TestCreateRuleRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	app, store, _ := newScopedTestApp(t)
	token := loginAsSeededUser(t, app, store, "admin", "admin-password")

	body := `{"principal":"alice","topic_filter":"sensors/#","permission":"read","scope":"listener"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestCreateRuleStoresTheListenerScope pins that a rule can be created for
// a listener scope and that the scope survives the round trip.
func TestCreateRuleStoresTheListenerScope(t *testing.T) {
	t.Parallel()

	app, store, _ := newScopedTestApp(t)
	token := loginAsSeededUser(t, app, store, "admin", "admin-password")

	body := `{"principal":"bob","topic_filter":"tls/#","permission":"read","scope":"listener","listener_id":"tls"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var rule acl.Rule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("decode rule: %v", err)
	}
	if rule.Scope != acl.ScopeListener {
		t.Fatalf("scope = %q, want %q", rule.Scope, acl.ScopeListener)
	}
	if rule.ListenerID != "tls" {
		t.Fatalf("listener id = %q, want %q", rule.ListenerID, "tls")
	}
}

// TestCreateRuleRejectsAListenerScopeWithoutAListener pins that a
// listener-scoped rule must name a listener.
func TestCreateRuleRejectsAListenerScopeWithoutAListener(t *testing.T) {
	t.Parallel()

	app, store, _ := newScopedTestApp(t)
	token := loginAsSeededUser(t, app, store, "admin", "admin-password")

	body := `{"principal":"bob","topic_filter":"tls/#","permission":"read","scope":"listener"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "listener_id is required") {
		t.Fatalf("body = %s, want it to mention the missing listener id", rec.Body.String())
	}
}

// TestListScopesReportsTheListenerFile pins that the scopes endpoint
// reports the file MCM writes for a listener and that it is marked as not
// wired while the listener declares no acl_file.
func TestListScopesReportsTheListenerFile(t *testing.T) {
	t.Parallel()

	app, store, aclPath := newScopedTestApp(t)
	token := loginAsSeededUser(t, app, store, "admin", "admin-password")

	rule := acl.Rule{
		Scope:       acl.ScopeListener,
		ListenerID:  "tls",
		Principal:   "bob",
		TopicFilter: "tls/#",
		Permission:  acl.PermissionRead,
	}
	if _, err := store.CreateRule(t.Context(), rule); err != nil {
		t.Fatalf("CreateRule() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/acls/scopes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response aclScopesResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode scopes: %v", err)
	}
	if len(response.Scopes) != 2 {
		t.Fatalf("the response holds %d scopes, want 2: %+v", len(response.Scopes), response.Scopes)
	}
	global := response.Scopes[0]
	if global.Scope != acl.ScopeGlobal || global.Path != aclPath {
		t.Fatalf("the first scope = %+v, want the broker-wide file %q", global, aclPath)
	}
	scoped := response.Scopes[1]
	if scoped.ListenerID != "tls" {
		t.Fatalf("the second scope belongs to %q, want %q", scoped.ListenerID, "tls")
	}
	wantPath := filepath.Join(filepath.Dir(aclPath), "acl.tls")
	if scoped.Path != wantPath {
		t.Fatalf("the listener file path = %q, want %q", scoped.Path, wantPath)
	}
	if scoped.Wired {
		t.Fatal("the listener file is reported as wired although the listener declares no acl_file")
	}
	if scoped.WiringIssue == "" {
		t.Fatal("the listener file carries no wiring issue")
	}
}

// TestEffectivePermissionsResolveTheDenyRule pins that the effective view
// reports the deny rule that covers a grant.
func TestEffectivePermissionsResolveTheDenyRule(t *testing.T) {
	t.Parallel()

	app, store, _ := newScopedTestApp(t)
	token := loginAsSeededUser(t, app, store, "admin", "admin-password")

	rules := []acl.Rule{
		{Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
		{Principal: "alice", TopicFilter: "sensors/secret/#", Permission: acl.PermissionDeny},
	}
	for _, rule := range rules {
		if _, err := store.CreateRule(t.Context(), rule); err != nil {
			t.Fatalf("CreateRule() error = %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/acls/effective", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response aclEffectiveResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode effective view: %v", err)
	}
	if len(response.Scopes) != 1 {
		t.Fatalf("the response holds %d scopes, want 1", len(response.Scopes))
	}
	var alice *aclIdentityView
	for idx := range response.Scopes[0].Identities {
		if response.Scopes[0].Identities[idx].Identity == "alice" {
			alice = &response.Scopes[0].Identities[idx]
		}
	}
	if alice == nil {
		t.Fatalf("the view holds no identity alice: %+v", response.Scopes[0].Identities)
	}
	if len(alice.Grants) != 1 {
		t.Fatalf("alice has %d grants, want 1: %+v", len(alice.Grants), alice.Grants)
	}
	if alice.Grants[0].Denied {
		t.Fatal("the grant of alice is reported as denied: the deny only covers sensors/secret/#")
	}
}

// TestSimulateAccessAnswersTheDenyRule pins that the simulator answers the
// question an operator asks when a rule looks wrong.
func TestSimulateAccessAnswersTheDenyRule(t *testing.T) {
	t.Parallel()

	app, store, _ := newScopedTestApp(t)
	token := loginAsSeededUser(t, app, store, "admin", "admin-password")

	rules := []acl.Rule{
		{Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
		{Principal: "alice", TopicFilter: "sensors/secret/#", Permission: acl.PermissionDeny},
	}
	for _, rule := range rules {
		if _, err := store.CreateRule(t.Context(), rule); err != nil {
			t.Fatalf("CreateRule() error = %v", err)
		}
	}

	body := `{"username":"alice","topic":"sensors/secret/1","access":"read"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls/effective/simulate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var decision acl.Decision
	if err := json.NewDecoder(rec.Body).Decode(&decision); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("the simulator allowed the access a deny rule covers: %+v", decision)
	}
}

// TestAdoptACLSourceStoresTheVerbatimBody pins that adopting an ACL file
// keeps its content verbatim, so the deploy can compose the managed rules
// into it without losing a line.
func TestAdoptACLSourceStoresTheVerbatimBody(t *testing.T) {
	t.Parallel()

	app, store, aclPath := newScopedTestApp(t)
	token := loginAsSeededUser(t, app, store, "admin", "admin-password")

	body := "# kept comment\nuser alice\ntopic read sensors/#\n\ntopic write cmd/#\n"
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls/source/adopt", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response aclSourceResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode source: %v", err)
	}
	if response.Path != aclPath {
		t.Fatalf("the adopted path = %q, want %q", response.Path, aclPath)
	}
	if response.Body != body {
		t.Fatalf("the adopted body = %q, want %q", response.Body, body)
	}

	// The source is readable afterwards.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/acls/source", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode source: %v", err)
	}
	if !response.Adopted {
		t.Fatal("the adopted source is not reported as adopted")
	}
	if response.Body != body {
		t.Fatalf("the stored body = %q, want %q", response.Body, body)
	}
}
