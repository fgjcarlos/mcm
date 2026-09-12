package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// --- Fakes ---

// fakePasswordLookup answers CleartextPassword calls from an in-memory
// map populated by tests.
type fakePasswordLookup struct {
	mu   sync.Mutex
	pwds map[string]string
}

func newFakePasswordLookup() *fakePasswordLookup {
	return &fakePasswordLookup{pwds: make(map[string]string)}
}

func (f *fakePasswordLookup) remember(username, password string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pwds[username] = password
}

func (f *fakePasswordLookup) CleartextPassword(username string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pw, ok := f.pwds[username]
	return pw, ok
}

// fakeApplier records calls to Apply and can be configured to fail.
type fakeApplier struct {
	mu       sync.Mutex
	calls    []applyCall
	failOnce bool // fail the first Apply call
	failAll  bool // fail all Apply calls
}

type applyCall struct {
	aclBody        string
	passwdBody     string
	aclSnapshot    string
	passwdSnapshot string
}

func (f *fakeApplier) Apply(_ context.Context, aclBody, passwdBody, aclSnapshot, passwdSnapshot string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, applyCall{
		aclBody:        aclBody,
		passwdBody:     passwdBody,
		aclSnapshot:    aclSnapshot,
		passwdSnapshot: passwdSnapshot,
	})
	if f.failAll {
		return errors.New("applier: write failed")
	}
	if f.failOnce && len(f.calls) == 1 {
		return errors.New("applier: write failed")
	}
	return nil
}

func (f *fakeApplier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeApplier) callAt(i int) applyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

// fakeDeploymentStore is an in-memory DeploymentStore.
type fakeDeploymentStore struct {
	mu          sync.Mutex
	deployments map[int64]*storage.Deployment
	nextID      int64
}

func newFakeDeploymentStore() *fakeDeploymentStore {
	return &fakeDeploymentStore{
		deployments: make(map[int64]*storage.Deployment),
		nextID:      1,
	}
}

func (f *fakeDeploymentStore) InsertDeployment(_ context.Context, d *storage.Deployment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	d.ID = f.nextID
	f.nextID++
	d.CreatedAt = now
	d.UpdatedAt = now
	clone := *d
	f.deployments[d.ID] = &clone
	return nil
}

func (f *fakeDeploymentStore) GetDeployment(_ context.Context, id int64) (storage.Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deployments[id]
	if !ok {
		return storage.Deployment{}, storage.ErrDeploymentNotFound
	}
	return *d, nil
}

func (f *fakeDeploymentStore) UpdateDeploymentStatus(_ context.Context, id int64, status, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deployments[id]
	if !ok {
		return storage.ErrDeploymentNotFound
	}
	d.Status = status
	d.Message = message
	d.UpdatedAt = time.Now().UTC()
	return nil
}

func (f *fakeDeploymentStore) ListDeployments(_ context.Context, limit, offset int) ([]storage.Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]storage.Deployment, 0, len(f.deployments))
	for _, d := range f.deployments {
		result = append(result, *d)
	}
	return result, nil
}

func (f *fakeDeploymentStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deployments)
}

// fakeACLStore is an in-memory acl.Store.
type fakeACLStore struct {
	rules []acl.Rule
}

func (f *fakeACLStore) ListRules(_ context.Context) ([]acl.Rule, error) {
	return f.rules, nil
}

func (f *fakeACLStore) CreateRule(_ context.Context, rule acl.Rule) (acl.Rule, error) {
	return rule, nil
}

func (f *fakeACLStore) UpdateRule(_ context.Context, id string, rule acl.Rule) (acl.Rule, error) {
	return rule, nil
}

func (f *fakeACLStore) DeleteRule(_ context.Context, id string) error {
	return nil
}

// fakeMQTTUserLister is a minimal MQTTUserLister.
type fakeMQTTUserLister struct {
	users       []storage.MQTTUser
	orphanRules []storage.ACLRuleRow
	orphanErr   error
}

func (f *fakeMQTTUserLister) ListMQTTUsers(_ context.Context) ([]storage.MQTTUser, error) {
	return f.users, nil
}

func (f *fakeMQTTUserLister) FindOrphanRules(_ context.Context) ([]storage.ACLRuleRow, error) {
	return f.orphanRules, f.orphanErr
}

type coordinatedMQTTUserLister struct {
	*fakeMQTTUserLister
	mu sync.Mutex
}

func (f *coordinatedMQTTUserLister) LockMutations() {
	f.mu.Lock()
}

func (f *coordinatedMQTTUserLister) UnlockMutations() {
	f.mu.Unlock()
}

// --- Helpers ---

// enabledDeployCfg returns a DeployConfig with deploy enabled pointing at temp files.
func enabledDeployCfg(t *testing.T) (config.DeployConfig, string, string) {
	t.Helper()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")
	return config.DeployConfig{
		Mode:               "file",
		ACLPath:            aclPath,
		PasswdPath:         passwdPath,
		HealthcheckTimeout: 5 * time.Second,
	}, aclPath, passwdPath
}

// okVerifier is the ActiveVerifier that always returns OK=true.
func okVerifier(_ context.Context, _ diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult {
	return diagnostics.VerifyActiveResult{
		OK:              true,
		Stage:           "ok",
		Message:         "positive: OK; negative: rejected",
		PositiveMessage: "positive: round-trip OK",
		NegativeMessage: "negative: rejected (0x80)",
	}
}

// failVerifier is the ActiveVerifier that always returns OK=false.
func failVerifier(_ context.Context, _ diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult {
	return diagnostics.VerifyActiveResult{
		OK:              false,
		Stage:           "positive",
		Message:         "positive: subscribe failed",
		PositiveMessage: "positive: subscribe failed",
	}
}

// flakyVerifier returns OK=false on the first failUntil calls and OK=true
// afterwards. Used to exercise the bounded-retry path.
type flakyVerifier struct {
	mu        sync.Mutex
	failUntil int
	calls     int
}

func (f *flakyVerifier) VerifyActive(_ context.Context, _ diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failUntil {
		return failVerifier(context.Background(), diagnostics.VerifyActiveOptions{})
	}
	return okVerifier(context.Background(), diagnostics.VerifyActiveOptions{})
}

func (f *flakyVerifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func noAudit(_ context.Context, _, _, _, _, _ string, _ []byte) {}

func newTestService(
	applier mosquitto.Applier,
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployStore DeploymentStore,
	verifier ActiveVerifier,
	passwordLookup CleartextPasswordLookup,
	deployCfg config.DeployConfig,
) *Service {
	svc := NewService(applier, aclStore, mqttStore, deployStore, verifier, passwordLookup,
		config.MosquittoConfig{Host: "localhost", Port: 1883},
		deployCfg,
		noAudit,
	)
	return svc
}

// writeFile writes content to a file path (used to set up on-disk state in tests).
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeFile %q: %v", path, err)
	}
}

// --- Tests ---

func TestPreview_Disabled(t *testing.T) {
	t.Parallel()
	store := newFakeDeploymentStore()
	svc := newTestService(
		&fakeApplier{},
		&fakeACLStore{},
		&fakeMQTTUserLister{},
		store,
		diagnostics.VerifierFunc(okVerifier),
		&fakePasswordLookup{},
		config.DeployConfig{Mode: ""}, // disabled
	)

	_, err := svc.Preview(context.Background(), "operator")
	if !errors.Is(err, ErrDeployDisabled) {
		t.Errorf("error = %v, want ErrDeployDisabled", err)
	}
	if store.count() != 0 {
		t.Error("Preview (disabled) must not create deployment records")
	}
}

func TestPreview_NoChanges(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)

	aclStore := &fakeACLStore{rules: []acl.Rule{}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{}}

	// On-disk files match rendered output (both empty).
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	svc := newTestService(&fakeApplier{}, aclStore, mqttStore, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)

	result, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	if result.HasChanges {
		t.Error("want HasChanges = false, got true")
	}
	if result.ACLDiff != "" {
		t.Errorf("want empty ACLDiff, got %q", result.ACLDiff)
	}
	if result.PasswdDiff != "" {
		t.Errorf("want empty PasswdDiff, got %q", result.PasswdDiff)
	}
}

func TestPreview_HappyPath(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)

	// Store has a rule; on-disk file is empty (stale).
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Principal: "alice", TopicFilter: "sensors/#", Permission: "read"},
	}}
	mqttStore := &fakeMQTTUserLister{}

	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	svc := newTestService(&fakeApplier{}, aclStore, mqttStore, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)

	result, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	if !result.HasChanges {
		t.Error("want HasChanges = true, got false")
	}
	if result.ACLDiff == "" {
		t.Error("want non-empty ACLDiff")
	}
}

func TestPreview_SurfacesAndFiltersOrphanRules(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Principal: "active-user", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
		{ID: "2", Principal: "deleted-user", TopicFilter: "legacy/#", Permission: acl.PermissionWrite},
	}}
	mqttStore := &fakeMQTTUserLister{
		users: []storage.MQTTUser{{Username: "active-user"}},
		orphanRules: []storage.ACLRuleRow{{
			ID:          "2",
			Principal:   "deleted-user",
			TopicFilter: "legacy/#",
			Permission:  acl.PermissionWrite,
		}},
	}

	svc := newTestService(&fakeApplier{}, aclStore, mqttStore, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)
	result, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}

	if len(result.OrphanRules) != 1 || result.OrphanRules[0].Principal != "deleted-user" {
		t.Fatalf("OrphanRules = %+v, want deleted-user rule", result.OrphanRules)
	}
	if !strings.Contains(result.ACLBody, "user active-user\ntopic read sensors/#") {
		t.Fatalf("ACLBody missing active rule:\n%s", result.ACLBody)
	}
	if strings.Contains(result.ACLBody, "deleted-user") || strings.Contains(result.ACLBody, "legacy/#") {
		t.Fatalf("ACLBody rendered orphan rule:\n%s", result.ACLBody)
	}
}

func TestPreview_ReturnsOrphanRuleLookupError(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")
	lookupErr := errors.New("orphan lookup failed")
	mqttStore := &fakeMQTTUserLister{orphanErr: lookupErr}
	svc := newTestService(&fakeApplier{}, &fakeACLStore{}, mqttStore, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)

	_, err := svc.Preview(context.Background(), "operator")
	if !errors.Is(err, lookupErr) {
		t.Fatalf("Preview error = %v, want lookup error", err)
	}
	if !strings.Contains(err.Error(), "find orphan acl rules") {
		t.Fatalf("Preview error = %v, want orphan lookup context", err)
	}
}

func TestPreview_UsesStorageOrphanRules(t *testing.T) {
	t.Parallel()

	store, err := storage.Open(filepath.Join(t.TempDir(), "mcm.db"))
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	user, err := store.CreateMQTTUser(ctx, storage.CreateMQTTUserParams{Username: "disabled-user"})
	if err != nil {
		t.Fatalf("CreateMQTTUser returned error: %v", err)
	}
	if _, err := store.ACLStore().CreateRule(ctx, acl.Rule{
		Principal:   user.Username,
		TopicFilter: "legacy/#",
		Permission:  acl.PermissionRead,
	}); err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}
	disabled := true
	if _, err := store.UpdateMQTTUser(ctx, user.ID, storage.UpdateMQTTUserParams{Disabled: &disabled}); err != nil {
		t.Fatalf("UpdateMQTTUser returned error: %v", err)
	}

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")
	svc := newTestService(&fakeApplier{}, store.ACLStore(), store, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)

	result, err := svc.Preview(ctx, "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	if len(result.OrphanRules) != 1 || result.OrphanRules[0].Principal != "disabled-user" {
		t.Fatalf("OrphanRules = %+v, want disabled-user rule", result.OrphanRules)
	}
	if result.ACLBody != "" {
		t.Fatalf("ACLBody = %q, want orphan rule excluded", result.ACLBody)
	}
}

// TestPreview_ServiceUserHashReused covers the follow-up review on PR #284:
// render() must NOT re-hash the broker service user on every call. Re-hashing
// with a fresh random salt makes the rendered passwd file always-different
// from the on-disk file, so has_changes is never false on a no-op preview,
// and every apply call rewrites the file + SIGHUPs the broker even when
// nothing changed. The fix: read the existing passwd file, look up the
// service user, reuse that hash verbatim.
func TestPreview_ServiceUserHashReused(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)

	// Pre-seed the on-disk passwd with the service user and the same
	// configured password. A matching valid hash must be reused verbatim.
	existingHash, err := mosquitto.HashPassword("anything", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "svc-admin:"+existingHash+"\n")

	aclStore := &fakeACLStore{}
	mqttStore := &fakeMQTTUserLister{}

	svc := newTestService(&fakeApplier{}, aclStore, mqttStore, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)
	// Configure the service user so render() looks it up.
	svc.mosquittoCfg.Username = "svc-admin"
	svc.mosquittoCfg.Password = "anything" // never re-hashed; the existing entry wins

	result, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}

	// The passwd body must use the existing hash verbatim, not a fresh one.
	if !strings.Contains(result.PasswdBody, existingHash) {
		t.Errorf("PasswdBody did not reuse the existing hash.\nGot:\n%s", result.PasswdBody)
	}
	// The passwd diff against the on-disk file must be empty: the rendered
	// passwd is byte-identical to the existing one. We assert on the
	// passwd diff specifically (not HasChanges) because the service user
	// ACL block is also always emitted, so the ACL diff is non-empty on
	// first preview — that's intentional.
	if result.PasswdDiff != "" {
		t.Errorf("want empty PasswdDiff (hash reused), got:\n%s", result.PasswdDiff)
	}
}

func TestPreview_ServiceUserPasswordChangeRehashes(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	oldHash, err := mosquitto.HashPassword("old-password", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "svc-admin:"+oldHash+"\n")

	svc := newTestService(
		&fakeApplier{},
		&fakeACLStore{},
		&fakeMQTTUserLister{},
		newFakeDeploymentStore(),
		diagnostics.VerifierFunc(okVerifier),
		&fakePasswordLookup{},
		deployCfg,
	)
	svc.mosquittoCfg.Username = "svc-admin"
	svc.mosquittoCfg.Password = "new-password"

	result, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	entries := mosquitto.ParsePasswdFile(result.PasswdBody)
	if len(entries) != 1 {
		t.Fatalf("rendered passwd entries = %d, want 1", len(entries))
	}
	if entries[0].Hash == oldHash {
		t.Fatal("service user retained the old hash after its configured password changed")
	}
	ok, err := mosquitto.VerifyPassword(entries[0].Hash, "new-password")
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if !ok {
		t.Fatal("rendered service-user hash does not match the new configured password")
	}
}

// TestPreview_ServiceUserACLAlwaysPresent covers the follow-up review on
// PR #284: an empty ACL file means DENY ALL in Mosquitto. The service
// user (MCM_MOSQUITTO_USERNAME) is the operator's own connection to the
// broker — it must always have at least a baseline set of rules so the
// deploy healthcheck and the broker events feed keep working after every
// apply. The fix: render() always appends a service-user block to the
// ACL body, regardless of what the operator's rule store contains.
func TestPreview_ServiceUserACLAlwaysPresent(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)

	// Empty operator ruleset + empty on-disk ACL.
	aclStore := &fakeACLStore{}
	mqttStore := &fakeMQTTUserLister{}
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	svc := newTestService(&fakeApplier{}, aclStore, mqttStore, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)
	svc.mosquittoCfg.Username = "svc-admin"
	svc.mosquittoCfg.Password = "p"

	result, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}

	// The ACL body must contain the service user block with read # and
	// readwrite mcm/healthcheck. Without those, the deploy healthcheck
	// silently fails on an ACL-enabled Mosquitto (issue #275 review).
	if !strings.Contains(result.ACLBody, "user svc-admin") {
		t.Errorf("ACLBody missing service user block.\nGot:\n%s", result.ACLBody)
	}
	if !strings.Contains(result.ACLBody, "topic read #") {
		t.Errorf("ACLBody missing service user 'topic read #' rule.\nGot:\n%s", result.ACLBody)
	}
	if !strings.Contains(result.ACLBody, "topic readwrite mcm/healthcheck") {
		t.Errorf("ACLBody missing service user 'topic readwrite mcm/healthcheck' rule.\nGot:\n%s", result.ACLBody)
	}
}

func TestApply_Disabled(t *testing.T) {
	t.Parallel()

	store := newFakeDeploymentStore()
	svc := newTestService(
		&fakeApplier{},
		&fakeACLStore{},
		&fakeMQTTUserLister{},
		store,
		diagnostics.VerifierFunc(okVerifier),
		&fakePasswordLookup{},
		config.DeployConfig{Mode: ""},
	)

	_, err := svc.Apply(context.Background(), "operator")
	if !errors.Is(err, ErrDeployDisabled) {
		t.Errorf("error = %v, want ErrDeployDisabled", err)
	}
	if store.count() != 0 {
		t.Error("Apply (disabled) must not create deployment records")
	}
}

func TestApply_Success(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := &fakeApplier{}
	store := newFakeDeploymentStore()
	aclStore, mqttStore, pwLookup := withSeedUsers(t)

	svc := newTestService(applier, aclStore, mqttStore, store, diagnostics.VerifierFunc(okVerifier), pwLookup, deployCfg)

	d, err := svc.Apply(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "active_verified" {
		t.Errorf("Status = %q, want %q", d.Status, "active_verified")
	}
	if applier.callCount() != 1 {
		t.Errorf("applier called %d times, want 1", applier.callCount())
	}
	if store.count() != 1 {
		t.Error("want exactly 1 deployment record")
	}
}

func TestApply_ApplierError(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := &fakeApplier{failAll: true}
	store := newFakeDeploymentStore()

	svc := newTestService(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)

	_, err := svc.Apply(context.Background(), "operator")
	if err == nil {
		t.Fatal("Apply: want error, got nil")
	}

	// Check deployment record is "failed".
	recs, _ := store.ListDeployments(context.Background(), 20, 0)
	if len(recs) != 1 {
		t.Fatalf("want 1 deployment record, got %d", len(recs))
	}
	if recs[0].Status != "failed" {
		t.Errorf("Status = %q, want %q", recs[0].Status, "failed")
	}
	// No verification should have been called; applier called exactly once.
	if applier.callCount() != 1 {
		t.Errorf("applier called %d times, want 1", applier.callCount())
	}
}

// TestApply_LifecycleSavedApplyingPendingActivationActiveVerified
// covers issue #293 acceptance criterion 1: the deployment record must
// walk the explicit lifecycle states in order — saved (record inserted),
// applying (applier writing), pending_activation (applier done, awaiting
// verification), and finally active_verified (positive + negative
// verification passed).
func TestApply_LifecycleSavedApplyingPendingActivationActiveVerified(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := &fakeApplier{}
	store := newRecordingDeploymentStore()
	aclStore, mqttStore, pwLookup := withSeedUsers(t)

	svc := newTestService(applier, aclStore, mqttStore, store, diagnostics.VerifierFunc(okVerifier), pwLookup, deployCfg)

	d, err := svc.Apply(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "active_verified" {
		t.Errorf("Status = %q, want %q", d.Status, "active_verified")
	}
	// Every lifecycle state must appear in the recorded transitions in
	// the expected order.
	want := []string{"saved", "applying", "pending_activation", "active_verified"}
	got := store.statuses()
	if len(got) != len(want) {
		t.Fatalf("recorded statuses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("transition[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestApply_VerifierRetriesThenSucceeds covers issue #293 acceptance
// criterion 4: bounded retries. The first attempt fails (transient),
// the second succeeds — apply still ends in active_verified.
func TestApply_VerifierRetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := &fakeApplier{}
	store := newFakeDeploymentStore()
	aclStore, mqttStore, pwLookup := withSeedUsers(t)
	verifier := &flakyVerifier{failUntil: 1}

	svc := newTestService(applier, aclStore, mqttStore, store, diagnostics.VerifierFunc(verifier.VerifyActive), pwLookup, deployCfg)

	d, err := svc.Apply(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "active_verified" {
		t.Errorf("Status = %q, want %q", d.Status, "active_verified")
	}
	if verifier.callCount() != 2 {
		t.Errorf("verifier called %d times, want 2 (one retry)", verifier.callCount())
	}
}

// TestApply_VerifierExhaustsRetries covers issue #293 acceptance
// criterion 4: bounded retries must terminate. When the verifier fails
// every attempt, the apply rolls back and lands in rolled_back (NOT
// active_verified).
func TestApply_VerifierExhaustsRetries(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := &fakeApplier{}
	store := newFakeDeploymentStore()
	aclStore, mqttStore, pwLookup := withSeedUsers(t)
	verifier := &flakyVerifier{failUntil: 99} // never succeeds

	svc := newTestService(applier, aclStore, mqttStore, store, diagnostics.VerifierFunc(verifier.VerifyActive), pwLookup, deployCfg)

	d, err := svc.Apply(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "rolled_back" {
		t.Errorf("Status = %q, want %q", d.Status, "rolled_back")
	}
	if verifier.callCount() != verifyAttempts {
		t.Errorf("verifier called %d times, want %d", verifier.callCount(), verifyAttempts)
	}
	// Applier called twice: once for apply, once for rollback.
	if applier.callCount() != 2 {
		t.Errorf("applier called %d times, want 2 (apply + rollback)", applier.callCount())
	}
}

func TestApply_HealthcheckFailure_Rollback(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl content")
	writeFile(t, passwdPath, "old passwd content")

	// Applier succeeds both times (apply + rollback).
	applier := &fakeApplier{}
	store := newFakeDeploymentStore()

	svc := newTestService(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(failVerifier), &fakePasswordLookup{}, deployCfg)

	d, err := svc.Apply(context.Background(), "operator")
	// rolled_back is not an error to the caller according to spec scenarios; the
	// deployment record should be returned with status rolled_back.
	if err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}
	if d.Status != "rolled_back" {
		t.Errorf("Status = %q, want %q", d.Status, "rolled_back")
	}
	// Applier called twice: apply + rollback.
	if applier.callCount() != 2 {
		t.Errorf("applier called %d times, want 2", applier.callCount())
	}
	// Second call (rollback) uses the snapshot content.
	rollbackCall := applier.callAt(1)
	if rollbackCall.aclBody != "old acl content" {
		t.Errorf("rollback aclBody = %q, want %q", rollbackCall.aclBody, "old acl content")
	}
}

func TestApply_RollbackFailure(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	// Applier succeeds on first call (apply), fails on second (rollback).
	applier := &fakeApplier{}
	applier.failOnce = false

	// We need a custom applier that succeeds first, fails second.
	rollbackFailApplier := &rollbackFailFakeApplier{}
	store := newFakeDeploymentStore()

	svc := newTestService(rollbackFailApplier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(failVerifier), &fakePasswordLookup{}, deployCfg)

	_, err := svc.Apply(context.Background(), "operator")
	if err == nil {
		t.Fatal("Apply with rollback failure: want error, got nil")
	}

	recs, _ := store.ListDeployments(context.Background(), 20, 0)
	if len(recs) != 1 {
		t.Fatalf("want 1 deployment record, got %d", len(recs))
	}
	if recs[0].Status != "rollback_failed" {
		t.Errorf("Status = %q, want %q", recs[0].Status, "rollback_failed")
	}
}

// rollbackFailFakeApplier succeeds on the first Apply, fails on the second.
type rollbackFailFakeApplier struct {
	count int32
}

func (r *rollbackFailFakeApplier) Apply(_ context.Context, _, _, _, _ string) error {
	n := atomic.AddInt32(&r.count, 1)
	if n == 1 {
		return nil // first call (apply) succeeds
	}
	return errors.New("rollback applier: write failed")
}

// recordingDeploymentStore extends fakeDeploymentStore to record every
// status transition in order so the lifecycle test can assert the
// recorded sequence.
type recordingDeploymentStore struct {
	*fakeDeploymentStore
	mu        sync.Mutex
	transitns []string
}

func newRecordingDeploymentStore() *recordingDeploymentStore {
	return &recordingDeploymentStore{fakeDeploymentStore: newFakeDeploymentStore()}
}

func (r *recordingDeploymentStore) InsertDeployment(ctx context.Context, d *storage.Deployment) error {
	r.mu.Lock()
	r.transitns = append(r.transitns, d.Status)
	r.mu.Unlock()
	return r.fakeDeploymentStore.InsertDeployment(ctx, d)
}

func (r *recordingDeploymentStore) UpdateDeploymentStatus(ctx context.Context, id int64, status, message string) error {
	r.mu.Lock()
	r.transitns = append(r.transitns, status)
	r.mu.Unlock()
	return r.fakeDeploymentStore.UpdateDeploymentStatus(ctx, id, status, message)
}

func (r *recordingDeploymentStore) statuses() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.transitns))
	copy(out, r.transitns)
	return out
}

// recordingAudit captures every audit call for assertions.
type recordingAudit struct {
	mu    sync.Mutex
	calls []auditCall
}

type auditCall struct {
	actor        string
	action       string
	resourceType string
	resourceID   string
	result       string
}

func (r *recordingAudit) record(_ context.Context, actor, action, resourceType, resourceID, result string, _ []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, auditCall{
		actor:        actor,
		action:       action,
		resourceType: resourceType,
		resourceID:   resourceID,
		result:       result,
	})
}

func (r *recordingAudit) actions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.calls))
	for _, c := range r.calls {
		out = append(out, c.action+":"+c.result)
	}
	return out
}

// withSeedUsers returns an ACL store, an MQTT user list store, and a
// password lookup pre-populated with a single test user ("verify-user")
// that has a readwrite grant on "verify/topic". Use this in tests that
// expect the deploy verifier to succeed (active_verified) — without
// the seed the verifier has no test subject and the apply rolls back.
func withSeedUsers(t *testing.T) (*fakeACLStore, *fakeMQTTUserLister, *fakePasswordLookup) {
	t.Helper()
	hash, err := mosquitto.HashPassword("verify-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{Principal: "verify-user", TopicFilter: "verify/topic", Permission: acl.PermissionReadWrite},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "verify-user", PasswordHash: hash},
	}}
	pwLookup := newFakePasswordLookup()
	pwLookup.remember("verify-user", "verify-pass")
	return aclStore, mqttStore, pwLookup
}

// newTestServiceWithAudit wires the service with a recording audit fn.
func newTestServiceWithAudit(
	applier mosquitto.Applier,
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployStore DeploymentStore,
	verifier ActiveVerifier,
	passwordLookup CleartextPasswordLookup,
	deployCfg config.DeployConfig,
	audit *recordingAudit,
) *Service {
	return NewService(applier, aclStore, mqttStore, deployStore, verifier, passwordLookup,
		config.MosquittoConfig{Host: "localhost", Port: 1883},
		deployCfg,
		audit.record,
	)
}

// TestApply_HealthcheckFailure_AuditAndFilesReverted covers the full rollback
// lifecycle: when the healthcheck fails but the rollback applier call succeeds,
// the on-disk files must be reverted to the snapshot and an audit event of
// type deployment.rolled_back with result "failure" must be emitted.
//
// This locks in the contract that #275 depends on: a successful rollback
// leaves the broker in the exact state it was in before the failed deploy.
func TestApply_HealthcheckFailure_AuditAndFilesReverted(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl content")
	writeFile(t, passwdPath, "old passwd content")

	applier := &fakeApplier{}
	store := newFakeDeploymentStore()
	audit := &recordingAudit{}

	svc := newTestServiceWithAudit(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(failVerifier), &fakePasswordLookup{}, deployCfg, audit)

	d, err := svc.Apply(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}
	if d.Status != "rolled_back" {
		t.Errorf("Status = %q, want %q", d.Status, "rolled_back")
	}
	// Applier was called twice: apply + rollback.
	if applier.callCount() != 2 {
		t.Fatalf("applier called %d times, want 2", applier.callCount())
	}
	// Second call (rollback) uses the snapshot content.
	rollbackCall := applier.callAt(1)
	if rollbackCall.aclBody != "old acl content" {
		t.Errorf("rollback aclBody = %q, want %q", rollbackCall.aclBody, "old acl content")
	}
	if rollbackCall.passwdBody != "old passwd content" {
		t.Errorf("rollback passwdBody = %q, want %q", rollbackCall.passwdBody, "old passwd content")
	}

	// On-disk files must match the snapshot after rollback.
	gotACL, err := os.ReadFile(aclPath)
	if err != nil {
		t.Fatalf("ReadFile acl: %v", err)
	}
	if string(gotACL) != "old acl content" {
		t.Errorf("acl on disk after rollback = %q, want %q", string(gotACL), "old acl content")
	}
	gotPasswd, err := os.ReadFile(passwdPath)
	if err != nil {
		t.Fatalf("ReadFile passwd: %v", err)
	}
	if string(gotPasswd) != "old passwd content" {
		t.Errorf("passwd on disk after rollback = %q, want %q", string(gotPasswd), "old passwd content")
	}

	// Audit events: must contain deployment.rolled_back:failure, must NOT
	// contain deployment.applied:success.
	actions := audit.actions()
	if !contains(actions, "deployment.rolled_back:failure") {
		t.Errorf("audit actions = %v, want deployment.rolled_back:failure", actions)
	}
	for _, a := range actions {
		if a == "deployment.applied:success" {
			t.Errorf("audit actions = %v, must not include deployment.applied:success when healthcheck failed", actions)
		}
	}
}

// TestApply_RollbackFailure_AuditEmitted covers the case where the rollback
// applier call itself fails. The service must surface an error to the caller,
// persist status "rollback_failed", and emit an audit event of type
// deployment.rollback_failed with result "failure".
//
// This is the contract #275 acceptance criteria "A failed reload reverts the
// files and emits an audit event" depends on. When the rollback itself fails
// the revert is necessarily incomplete, so the audit must record the failure
// so operators can investigate.
func TestApply_RollbackFailure_AuditEmitted(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := &rollbackFailFakeApplier{}
	store := newFakeDeploymentStore()
	audit := &recordingAudit{}

	svc := newTestServiceWithAudit(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(failVerifier), &fakePasswordLookup{}, deployCfg, audit)

	_, err := svc.Apply(context.Background(), "operator")
	if err == nil {
		t.Fatal("Apply with rollback failure: want error, got nil")
	}

	recs, _ := store.ListDeployments(context.Background(), 20, 0)
	if len(recs) != 1 {
		t.Fatalf("want 1 deployment record, got %d", len(recs))
	}
	if recs[0].Status != "rollback_failed" {
		t.Errorf("Status = %q, want %q", recs[0].Status, "rollback_failed")
	}
	if !strings.Contains(recs[0].Message, "rollback") {
		t.Errorf("Message = %q, want substring 'rollback'", recs[0].Message)
	}

	actions := audit.actions()
	if !contains(actions, "deployment.rollback_failed:failure") {
		t.Errorf("audit actions = %v, want deployment.rollback_failed:failure", actions)
	}
	for _, a := range actions {
		if a == "deployment.applied:success" {
			t.Errorf("audit actions = %v, must not include deployment.applied:success when rollback failed", actions)
		}
	}
}

// rollbackCtxRecordingApplier captures the ctx passed to the rollback Apply
// call so the test can assert it is derived from context.Background()
// (not from the cancelled request ctx).
type rollbackCtxRecordingApplier struct {
	applyErrs      []error // errors to return on each Apply call
	rollbackCtxErr error   // error captured from the rollback call's ctx
}

func (r *rollbackCtxRecordingApplier) Apply(ctx context.Context, _, _, _, _ string) error {
	// Record the ctx.Err() of the SECOND call (the rollback) to confirm
	// it is NOT cancelled.
	if len(r.applyErrs) >= 1 {
		r.rollbackCtxErr = ctx.Err()
	}
	if len(r.applyErrs) == 0 {
		r.applyErrs = append(r.applyErrs, nil) // apply succeeds
		return nil
	}
	// rollback fails
	return errors.New("rollback applier: simulated failure")
}

// TestApply_RollbackUsesIndependentContext covers issue #292 acceptance
// criterion 3: when the request ctx is cancelled, the rollback must still
// run. The rollback applier call must use a fresh context derived from
// context.Background() — not the cancelled request ctx.
func TestApply_RollbackUsesIndependentContext(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := &rollbackCtxRecordingApplier{}
	store := newFakeDeploymentStore()

	svc := newTestService(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(failVerifier), &fakePasswordLookup{}, deployCfg)

	// Use a context that is cancelled BEFORE Apply runs. The healthcheck
	// will fail because failVerifier ignores ctx and always returns
	// failure; the rollback must then run with an independent ctx.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.Apply(ctx, "operator")
	if err == nil {
		t.Fatal("Apply: want error (rollback failure), got nil")
	}

	// The rollback applier call must have seen a non-cancelled context.
	if applier.rollbackCtxErr != nil {
		t.Errorf("rollback ctx.Err() = %v, want nil (rollback must use an independent context)", applier.rollbackCtxErr)
	}

	// The deployment record must reflect rollback_failed.
	recs, _ := store.ListDeployments(context.Background(), 20, 0)
	if len(recs) != 1 {
		t.Fatalf("want 1 deployment record, got %d", len(recs))
	}
	if recs[0].Status != "rollback_failed" {
		t.Errorf("Status = %q, want %q", recs[0].Status, "rollback_failed")
	}
}

// errApplyRolledBackFakeApplier always returns an error wrapping
// mosquitto.ErrApplyRestored (apply failed but applier restored from
// snapshot). Used to verify the deploy service records status "failed"
// with the right audit event when the applier handles the rollback itself.
type errApplyRolledBackFakeApplier struct{}

func (errApplyRolledBackFakeApplier) Apply(_ context.Context, _, _, _, _ string) error {
	return fmt.Errorf("simulated: %w", mosquitto.ErrApplyRestored)
}

// TestApply_ApplierRolledBack_StatusFailed covers the case where the
// applier handles the partial-failure rollback internally and returns
// ErrApplyRestored. The deploy service must record status "failed"
// (NOT "rollback_failed") because the broker is back on the previous
// configuration — operator does not need to intervene.
func TestApply_ApplierRolledBack_StatusFailed(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := errApplyRolledBackFakeApplier{}
	store := newFakeDeploymentStore()
	audit := &recordingAudit{}

	svc := newTestServiceWithAudit(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg, audit)

	_, err := svc.Apply(context.Background(), "operator")
	if err == nil {
		t.Fatal("Apply: want error (apply failed), got nil")
	}

	recs, _ := store.ListDeployments(context.Background(), 20, 0)
	if len(recs) != 1 {
		t.Fatalf("want 1 deployment record, got %d", len(recs))
	}
	// Status must be "failed" (broker was restored to snapshot, but
	// the apply itself did not succeed).
	if recs[0].Status != "failed" {
		t.Errorf("Status = %q, want %q (broker was restored by applier; operator does not need to intervene)", recs[0].Status, "failed")
	}
	// The audit must record deployment.failed, NOT deployment.rollback_failed.
	actions := audit.actions()
	if !contains(actions, "deployment.failed:failure") {
		t.Errorf("audit actions = %v, want deployment.failed:failure", actions)
	}
	for _, a := range actions {
		if a == "deployment.rollback_failed:failure" {
			t.Errorf("audit actions = %v, must NOT include deployment.rollback_failed when applier restored successfully", actions)
		}
		if a == "deployment.applied:success" {
			t.Errorf("audit actions = %v, must not include deployment.applied:success on apply failure", actions)
		}
	}
}

// errApplyRollbackFailedFakeApplier always returns an error wrapping
// mosquitto.ErrRollbackFailed. Used to verify the deploy service records
// status "rollback_failed" (not "failed") when the applier could not
// restore from snapshot and the broker is in an indeterminate state.
type errApplyRollbackFailedFakeApplier struct{}

func (errApplyRollbackFailedFakeApplier) Apply(_ context.Context, _, _, _, _ string) error {
	return fmt.Errorf("simulated: %w", mosquitto.ErrRollbackFailed)
}

// TestApply_ApplierRollbackFailed_StatusRollbackFailed covers the case
// where the applier fails to restore from snapshot and returns
// ErrRollbackFailed. The deploy service must record status
// "rollback_failed" because the broker is in an indeterminate state and
// requires operator intervention.
func TestApply_ApplierRollbackFailed_StatusRollbackFailed(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	applier := errApplyRollbackFailedFakeApplier{}
	store := newFakeDeploymentStore()
	audit := &recordingAudit{}

	svc := newTestServiceWithAudit(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg, audit)

	_, err := svc.Apply(context.Background(), "operator")
	if err == nil {
		t.Fatal("Apply: want error, got nil")
	}

	recs, _ := store.ListDeployments(context.Background(), 20, 0)
	if len(recs) != 1 {
		t.Fatalf("want 1 deployment record, got %d", len(recs))
	}
	if recs[0].Status != "rollback_failed" {
		t.Errorf("Status = %q, want %q (applier could not restore; broker indeterminate)", recs[0].Status, "rollback_failed")
	}

	actions := audit.actions()
	if !contains(actions, "deployment.rollback_failed:failure") {
		t.Errorf("audit actions = %v, want deployment.rollback_failed:failure", actions)
	}
	for _, a := range actions {
		if a == "deployment.failed:failure" {
			t.Errorf("audit actions = %v, must NOT include deployment.failed when applier rollback also failed", actions)
		}
	}
}

// contains reports whether slice contains s.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestApply_ConcurrentApplyReturnsInProgress(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	applier := newBlockingApplier()
	store := newFakeDeploymentStore()
	svc := newTestService(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)

	done := make(chan error, 1)
	go func() {
		_, err := svc.Apply(context.Background(), "first")
		done <- err
	}()
	<-applier.started

	_, err := svc.Apply(context.Background(), "second")
	if !errors.Is(err, ErrDeployInProgress) {
		t.Fatalf("concurrent Apply error = %v, want ErrDeployInProgress", err)
	}
	if store.count() != 1 {
		t.Fatalf("deployment records while first apply is running = %d, want 1", store.count())
	}
	if applier.called() != 1 {
		t.Fatalf("applier calls after rejected concurrent apply = %d, want 1", applier.called())
	}

	applier.release()
	if err := <-done; err != nil {
		t.Fatalf("first Apply returned error: %v", err)
	}
	if store.count() != 1 {
		t.Fatalf("deployment records after first apply completes = %d, want 1", store.count())
	}
}

func TestApply_HoldsMutationLockThroughExternalApply(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "old passwd")

	aclStore, mqttStore, pwLookup := withSeedUsers(t)
	coordinatedStore := &coordinatedMQTTUserLister{fakeMQTTUserLister: mqttStore}
	applier := newBlockingApplier()
	svc := newTestService(applier, aclStore, coordinatedStore, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), pwLookup, deployCfg)

	done := make(chan error, 1)
	go func() {
		_, err := svc.Apply(context.Background(), "operator")
		done <- err
	}()
	<-applier.started

	mutationAcquired := make(chan struct{})
	go func() {
		coordinatedStore.LockMutations()
		close(mutationAcquired)
		coordinatedStore.UnlockMutations()
	}()

	select {
	case <-mutationAcquired:
		t.Fatal("mutation lock was released before external apply completed")
	case <-time.After(50 * time.Millisecond):
	}

	applier.release()
	if err := <-done; err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	select {
	case <-mutationAcquired:
	case <-time.After(time.Second):
		t.Fatal("mutation lock was not released after apply completed")
	}
}

type blockingApplier struct {
	started   chan struct{}
	releaseCh chan struct{}
	once      sync.Once
	mu        sync.Mutex
	count     int
}

func newBlockingApplier() *blockingApplier {
	return &blockingApplier{started: make(chan struct{}), releaseCh: make(chan struct{})}
}

func (b *blockingApplier) Apply(_ context.Context, _, _, _, _ string) error {
	b.mu.Lock()
	b.count++
	b.mu.Unlock()
	b.once.Do(func() { close(b.started) })
	<-b.releaseCh
	return nil
}

func (b *blockingApplier) release() { close(b.releaseCh) }

func (b *blockingApplier) called() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

func TestPreview_DiffTruncation(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)

	// Generate > 500 lines for the ACL file.
	var sb strings.Builder
	for i := 0; i < 600; i++ {
		sb.WriteString("line content here\n")
	}
	bigContent := sb.String()

	writeFile(t, aclPath, bigContent)
	writeFile(t, passwdPath, "")

	// ACL store has no rules → rendered ACL is empty → big diff.
	svc := newTestService(&fakeApplier{}, &fakeACLStore{}, &fakeMQTTUserLister{}, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, deployCfg)

	result, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	lines := strings.SplitAfter(result.ACLDiff, "\n")
	// Allow some slack for the truncation marker line.
	if len(lines) > maxDiffLines+5 {
		t.Errorf("ACLDiff has %d lines, want at most %d", len(lines), maxDiffLines+5)
	}
	if !strings.Contains(result.ACLDiff, "truncated") {
		t.Error("expected truncation indicator in ACLDiff")
	}
}
