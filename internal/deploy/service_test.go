package deploy

import (
	"context"
	"errors"
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

// fakeApplier records calls to Apply and can be configured to fail.
type fakeApplier struct {
	mu       sync.Mutex
	calls    []applyCall
	failOnce bool // fail the first Apply call
	failAll  bool // fail all Apply calls
}

type applyCall struct {
	aclBody    string
	passwdBody string
}

func (f *fakeApplier) Apply(_ context.Context, aclBody, passwdBody string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, applyCall{aclBody: aclBody, passwdBody: passwdBody})
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
	users []storage.MQTTUser
}

func (f *fakeMQTTUserLister) ListMQTTUsers(_ context.Context) ([]storage.MQTTUser, error) {
	return f.users, nil
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

func okHealthCheck(_ context.Context, _ config.MosquittoConfig) diagnostics.MQTTResult {
	return diagnostics.MQTTResult{OK: true, Message: "ok"}
}

func failHealthCheck(_ context.Context, _ config.MosquittoConfig) diagnostics.MQTTResult {
	return diagnostics.MQTTResult{OK: false, Message: "broker unreachable"}
}

func noAudit(_ context.Context, _, _, _, _, _ string, _ []byte) {}

func newTestService(
	applier mosquitto.Applier,
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployStore DeploymentStore,
	healthCheck HealthChecker,
	deployCfg config.DeployConfig,
) *Service {
	svc := NewService(applier, aclStore, mqttStore, deployStore, healthCheck,
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
		okHealthCheck,
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

	svc := newTestService(&fakeApplier{}, aclStore, mqttStore, newFakeDeploymentStore(), okHealthCheck, deployCfg)

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

	svc := newTestService(&fakeApplier{}, aclStore, mqttStore, newFakeDeploymentStore(), okHealthCheck, deployCfg)

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

func TestApply_Disabled(t *testing.T) {
	t.Parallel()

	store := newFakeDeploymentStore()
	svc := newTestService(
		&fakeApplier{},
		&fakeACLStore{},
		&fakeMQTTUserLister{},
		store,
		okHealthCheck,
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

	svc := newTestService(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, okHealthCheck, deployCfg)

	d, err := svc.Apply(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "applied" {
		t.Errorf("Status = %q, want %q", d.Status, "applied")
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

	svc := newTestService(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, okHealthCheck, deployCfg)

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
	// No healthcheck should have been called; applier called exactly once.
	if applier.callCount() != 1 {
		t.Errorf("applier called %d times, want 1", applier.callCount())
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

	svc := newTestService(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, failHealthCheck, deployCfg)

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

	svc := newTestService(rollbackFailApplier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, failHealthCheck, deployCfg)

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

func (r *rollbackFailFakeApplier) Apply(_ context.Context, _, _ string) error {
	n := atomic.AddInt32(&r.count, 1)
	if n == 1 {
		return nil // first call (apply) succeeds
	}
	return errors.New("rollback applier: write failed")
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

// newTestServiceWithAudit wires the service with a recording audit fn.
func newTestServiceWithAudit(
	applier mosquitto.Applier,
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployStore DeploymentStore,
	healthCheck HealthChecker,
	deployCfg config.DeployConfig,
	audit *recordingAudit,
) *Service {
	return NewService(applier, aclStore, mqttStore, deployStore, healthCheck,
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

	svc := newTestServiceWithAudit(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, failHealthCheck, deployCfg, audit)

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

	svc := newTestServiceWithAudit(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, failHealthCheck, deployCfg, audit)

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
	svc := newTestService(applier, &fakeACLStore{}, &fakeMQTTUserLister{}, store, okHealthCheck, deployCfg)

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

func (b *blockingApplier) Apply(_ context.Context, _, _ string) error {
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
	svc := newTestService(&fakeApplier{}, &fakeACLStore{}, &fakeMQTTUserLister{}, newFakeDeploymentStore(), okHealthCheck, deployCfg)

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
