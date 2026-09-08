// Package deploy orchestrates the deploy preview, apply, and rollback lifecycle
// for Mosquitto ACL and password configuration.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// ErrDeployDisabled is returned when the deploy mode is not configured.
var ErrDeployDisabled = errors.New("deploy mode is not configured")

// ErrDeployInProgress is returned when a deploy is already running.
var ErrDeployInProgress = errors.New("deploy already in progress")

// maxDiffLines is the maximum number of lines in a returned unified diff.
const maxDiffLines = 500

// rollbackTimeout bounds the time the deploy service spends restoring
// the broker's configuration from snapshot after a failed apply or
// verification. The deploy service derives a fresh context from
// context.Background() with this timeout so a cancelled HTTP request
// context does NOT abort the rollback — the broker must be brought back
// to a known state regardless of whether the operator's request is still
// alive (issue #292, acceptance criterion 3).
const rollbackTimeout = 10 * time.Second

// verifyAttempts and verifyBackoff schedule the bounded retry loop for
// the post-apply active verification (issue #293, acceptance criterion 4).
//
//   attempt 1: immediate after reloadSettleDelay
//   attempt 2: after verifyBackoff (1s)
//   attempt 3: after 2*verifyBackoff (2s)
//
// reloadSettleDelay is a short pause between applier.Apply returning and
// the verifier starting. Mosquitto processes SIGHUP asynchronously in its
// event loop; without a settle, the verifier can race the reload and
// observe the previous ACL (default-deny for unknown users, but full
// allow for any user without a matching rule — which is what the new
// config has during the race window). 1s is enough on a quiet broker;
// the bounded retries handle longer reload delays.
const (
	verifyAttempts     = 3
	verifyBackoff      = 1 * time.Second
	reloadSettleDelay  = 1 * time.Second
)

// ActiveVerifier is the post-apply check that proves the broker is
// serving the freshly-applied configuration (issue #293). Implementations
// must run a positive test (subscribe + publish round-trip) and a
// negative test (subscribe to a denied topic is rejected) and return
// OK=true only when both pass. The deploy service invokes this with
// bounded retries and backoff.
type ActiveVerifier interface {
	VerifyActive(ctx context.Context, opts diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult
}

// CleartextPasswordLookup returns the cleartext password for a username
// known to the API. The deploy service uses this to authenticate as a
// non-service user during the post-apply verification. Cleartexts are
// stored in memory only (never persisted) and populated at user
// creation time by the API handler.
type CleartextPasswordLookup interface {
	CleartextPassword(username string) (string, bool)
}

// DeploymentStore abstracts deployment record persistence.
type DeploymentStore interface {
	InsertDeployment(ctx context.Context, d *storage.Deployment) error
	GetDeployment(ctx context.Context, id int64) (storage.Deployment, error)
	UpdateDeploymentStatus(ctx context.Context, id int64, status string, message string) error
	ListDeployments(ctx context.Context, limit, offset int) ([]storage.Deployment, error)
}

// MQTTUserLister is the subset of storage.Store needed to list MQTT users.
type MQTTUserLister interface {
	ListMQTTUsers(ctx context.Context) ([]storage.MQTTUser, error)
}

// FileReader abstracts reading current on-disk config files (injectable for testing).
type FileReader func(path string) (string, error)

// AuditFunc is a function that records an audit event.
type AuditFunc func(ctx context.Context, actor, action, resourceType, resourceID, result string, metadata []byte)

// PreviewResult contains the diff output and rendered content for a deploy preview.
type PreviewResult struct {
	ACLDiff    string `json:"acl_diff"`
	PasswdDiff string `json:"passwd_diff"`
	ACLBody    string `json:"acl_body"`
	PasswdBody string `json:"passwd_body"`
	HasChanges bool   `json:"has_changes"`
}

// Service orchestrates deploy preview, apply, and history.
type Service struct {
	mu             sync.Mutex
	applier        mosquitto.Applier
	aclStore       acl.Store
	mqttStore      MQTTUserLister
	deployStore    DeploymentStore
	verifier       ActiveVerifier
	passwordLookup CleartextPasswordLookup
	readFile       FileReader
	mosquittoCfg   config.MosquittoConfig
	deployCfg      config.DeployConfig
	auditFn        AuditFunc
}

// NewService constructs a deploy Service.
//
// Issue #293: the legacy HealthChecker field is replaced by an
// ActiveVerifier (positive+negative MQTT round-trip) plus a
// CleartextPasswordLookup (so the verifier can authenticate as a
// non-service user that was created in this process lifetime).
func NewService(
	applier mosquitto.Applier,
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployStore DeploymentStore,
	verifier ActiveVerifier,
	passwordLookup CleartextPasswordLookup,
	mosquittoCfg config.MosquittoConfig,
	deployCfg config.DeployConfig,
	auditFn AuditFunc,
) *Service {
	return &Service{
		applier:        applier,
		aclStore:       aclStore,
		mqttStore:      mqttStore,
		deployStore:    deployStore,
		verifier:       verifier,
		passwordLookup: passwordLookup,
		readFile:       defaultFileReader,
		mosquittoCfg:   mosquittoCfg,
		deployCfg:      deployCfg,
		auditFn:        auditFn,
	}
}

// defaultFileReader reads a file from disk; returns empty string when file does not exist.
func defaultFileReader(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read file %q: %w", path, err)
	}
	return string(data), nil
}

// render fetches rules and users from the stores and produces rendered ACL and passwd bodies.
// The service user block (MCM_MOSQUITTO_USERNAME) and its ACL grants are
// always emitted so the deploy healthcheck and the broker events feed
// keep working after every apply. The service user's existing hash on
// disk is reused when present — re-hashing on every render would make
// has_changes non-idempotent (salt is random), forcing the applier to
// rewrite the file and SIGHUP the broker on every preview/apply cycle
// even when nothing changed.
func (s *Service) render(ctx context.Context) (aclBody, passwdBody string, err error) {
	rules, err := s.aclStore.ListRules(ctx)
	if err != nil {
		return "", "", fmt.Errorf("list acl rules: %w", err)
	}

	users, err := s.mqttStore.ListMQTTUsers(ctx)
	if err != nil {
		return "", "", fmt.Errorf("list mqtt users: %w", err)
	}

	// Read the on-disk passwd so we can preserve the service user's
	// existing hash. Preview/Apply already read it for the diff, but
	// render() must not depend on those callers. If the file is absent
	// (first boot) we recompute the hash.
	existingPasswd, err := s.readFile(s.deployCfg.PasswdPath)
	if err != nil {
		return "", "", fmt.Errorf("read current passwd file: %w", err)
	}
	existingEntries := mosquitto.ParsePasswdFile(existingPasswd)
	existingHashByUser := make(map[string]string, len(existingEntries))
	for _, e := range existingEntries {
		existingHashByUser[e.Username] = e.Hash
	}

	// Build the ACL body: managed rules + service user block.
	allRules := make([]acl.Rule, 0, len(rules)+1)
	allRules = append(allRules, rules...)
	if s.mosquittoCfg.Username != "" {
		allRules = append(allRules, serviceUserACL(s.mosquittoCfg.Username)...)
	}
	aclBody = mosquitto.RenderACLFile(allRules)

	entries := make([]mosquitto.PasswdEntry, 0, len(users)+1)
	for _, u := range users {
		if !u.Disabled {
			entries = append(entries, mosquitto.PasswdEntry{
				Username: u.Username,
				Hash:     u.PasswordHash,
			})
		}
	}
	// Include the broker service user (MCM_MOSQUITTO_USERNAME /
	// MCM_MOSQUITTO_PASSWORD) in the rendered passwd so the deploy
	// healthcheck — which authenticates as that user — keeps working
	// after the broker reloads. Without this the apply would always
	// roll back because the user MCM connects with would be missing
	// from the freshly-rendered file. The hash is reused from the
	// on-disk file when present so the rendered output is stable across
	// idempotent applies; only the first render (or a password change
	// in the MCM_MOSQUITTO_PASSWORD env var) produces a new hash.
	if s.mosquittoCfg.Username != "" && s.mosquittoCfg.Password != "" {
		hash := existingHashByUser[s.mosquittoCfg.Username]
		matchesConfiguredPassword := false
		if hash != "" {
			matchesConfiguredPassword, _ = mosquitto.VerifyPassword(hash, s.mosquittoCfg.Password)
		}
		if !matchesConfiguredPassword {
			var hashErr error
			hash, hashErr = mosquitto.HashPassword(s.mosquittoCfg.Password, mosquitto.DefaultIterations)
			if hashErr != nil {
				return "", "", fmt.Errorf("hash service user password: %w", hashErr)
			}
		}
		entries = append(entries, mosquitto.PasswdEntry{
			Username: s.mosquittoCfg.Username,
			Hash:     hash,
		})
	}
	passwdBody = mosquitto.RenderPasswdFile(entries)

	return aclBody, passwdBody, nil
}

// serviceUserACL returns the set of ACL rules MCM always emits for the
// broker service user (MCM_MOSQUITTO_USERNAME). The list is the minimum
// the runtime needs to keep working after every apply:
//   - read # — MCM subscribes to every topic for the broker events feed
//     (the WebSocket clients consume that subscription).
//   - readwrite mcm/healthcheck — the deploy healthcheck publishes here
//     and (depending on the test) verifies the round-trip.
//
// These are intentional baseline permissions for the service user; they
// are not user-managed rules and are NEVER exposed in the ACL HTTP API.
// Operators who want the service user to have more/fewer permissions
// must use a non-dev deploy mode (e.g. file mode with the service user
// removed from MCM entirely) and authenticate via an admin ACL rule.
func serviceUserACL(username string) []acl.Rule {
	return []acl.Rule{
		{
			Principal:   username,
			TopicFilter: "#",
			Permission:  acl.PermissionRead,
		},
		{
			Principal:   username,
			TopicFilter: "mcm/healthcheck",
			Permission:  acl.PermissionReadWrite,
		},
	}
}

// unifiedDiff generates a unified diff between current and rendered content, clamped to maxDiffLines.
func unifiedDiff(fromFile, toFile, current, rendered string) (string, error) {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(current),
		B:        difflib.SplitLines(rendered),
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	})
	if err != nil {
		return "", fmt.Errorf("generate unified diff: %w", err)
	}

	lines := strings.SplitAfter(diff, "\n")
	if len(lines) > maxDiffLines {
		lines = lines[:maxDiffLines]
		lines = append(lines, fmt.Sprintf("\n[... diff truncated at %d lines ...]\n", maxDiffLines))
	}

	return strings.Join(lines, ""), nil
}

// Preview returns unified diffs between on-disk files and the rendered configuration.
// Preview is read-only and does not acquire the apply mutex.
func (s *Service) Preview(ctx context.Context, actor string) (PreviewResult, error) {
	if s.deployCfg.Mode == "" {
		return PreviewResult{}, ErrDeployDisabled
	}

	aclBody, passwdBody, err := s.render(ctx)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("render config: %w", err)
	}

	currentACL, err := s.readFile(s.deployCfg.ACLPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("read on-disk acl file: %w", err)
	}

	currentPasswd, err := s.readFile(s.deployCfg.PasswdPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("read on-disk passwd file: %w", err)
	}

	aclDiff, err := unifiedDiff("current", "rendered", currentACL, aclBody)
	if err != nil {
		return PreviewResult{}, err
	}

	passwdDiff, err := unifiedDiff("current", "rendered", currentPasswd, passwdBody)
	if err != nil {
		return PreviewResult{}, err
	}

	hasChanges := aclDiff != "" || passwdDiff != ""

	if s.auditFn != nil {
		result := "success"
		s.auditFn(ctx, actor, "deployment.preview", "deployment", "", result, nil)
	}

	return PreviewResult{
		ACLDiff:    aclDiff,
		PasswdDiff: passwdDiff,
		ACLBody:    aclBody,
		PasswdBody: passwdBody,
		HasChanges: hasChanges,
	}, nil
}

// Apply applies the current rendered configuration, verifies the
// broker is serving it (positive + negative tests with bounded
// retries), and rolls back on any failure.
//
// Lifecycle (issue #293 P0):
//
//	saved              — record inserted with rendered content
//	applying           — applier is writing files / signalling reload
//	pending_activation — files written + reload signalled, awaiting verify
//	active_verified    — verification passed (positive + negative)
//	failed             — verification exhausted retries; rolled back to snapshot
//	rolled_back        — (legacy alias kept for compatibility)
//	rollback_failed    — restore from snapshot also failed
//
// The applier receives the on-disk snapshot (issue #292) and the verifier
// is invoked with bounded retries + backoff. On any persistent failure
// the rollback runs against a fresh bounded context derived from
// context.Background() so a cancelled HTTP request cannot abort it.
func (s *Service) Apply(ctx context.Context, actor string) (storage.Deployment, error) {
	if s.deployCfg.Mode == "" {
		return storage.Deployment{}, ErrDeployDisabled
	}

	if !s.mu.TryLock() {
		return storage.Deployment{}, ErrDeployInProgress
	}
	defer s.mu.Unlock()

	// Snapshot current on-disk files.
	aclSnapshot, err := s.readFile(s.deployCfg.ACLPath)
	if err != nil {
		return storage.Deployment{}, fmt.Errorf("snapshot acl file: %w", err)
	}
	passwdSnapshot, err := s.readFile(s.deployCfg.PasswdPath)
	if err != nil {
		return storage.Deployment{}, fmt.Errorf("snapshot passwd file: %w", err)
	}

	// Render new configuration.
	aclRendered, passwdRendered, err := s.render(ctx)
	if err != nil {
		return storage.Deployment{}, fmt.Errorf("render config: %w", err)
	}

	// Issue #293: insert with status="saved" so the lifecycle starts
	// at "rendered content exists, not yet applied to disk".
	d := &storage.Deployment{
		Actor:          actor,
		Status:         "saved",
		ACLSnapshot:    aclSnapshot,
		PasswdSnapshot: passwdSnapshot,
		ACLRendered:    aclRendered,
		PasswdRendered: passwdRendered,
	}
	if err := s.deployStore.InsertDeployment(ctx, d); err != nil {
		return storage.Deployment{}, fmt.Errorf("insert deployment: %w", err)
	}
	s.emitAudit(ctx, actor, "deployment.saved", d.ID, "success")

	// Move to "applying" before touching disk.
	_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "applying", "")
	s.emitAudit(ctx, actor, "deployment.applying", d.ID, "success")

	// Apply rendered configuration. The applier restores from snapshot
	// internally on partial failure (issue #292).
	applyErr := s.applier.Apply(ctx, aclRendered, passwdRendered, aclSnapshot, passwdSnapshot)
	if applyErr != nil {
		return s.recordApplyFailure(ctx, actor, d, applyErr)
	}

	// Move to "pending_activation" — files on disk, reload signalled,
	// awaiting verification.
	_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "pending_activation", "")
	s.emitAudit(ctx, actor, "deployment.pending_activation", d.ID, "success")

	// Mosquitto processes SIGHUP asynchronously; wait a moment before
	// the verifier races the reload against the new config (issue #293).
	// The settle uses a fresh ctx derived from context.Background() so a
	// cancelled HTTP request does not abort it (the same principle as
	// the bounded rollback ctx — issue #292, acceptance criterion 3).
	select {
	case <-time.After(reloadSettleDelay):
	case <-ctx.Done():
		// Ignore — the reload settle must finish even when the request
		// ctx is cancelled; only the bounded ctx controls the total
		// verifier budget.
	}

	// Bounded-retry verification (issue #293, acceptance criterion 4).
	verifyResult, attempts := s.runVerifier(ctx, aclRendered, passwdRendered)
	if verifyResult.OK {
		msg := ""
		if attempts > 1 {
			msg = fmt.Sprintf("verified after %d attempts", attempts)
		}
		_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "active_verified", msg)
		s.emitAudit(ctx, actor, "deployment.active_verified", d.ID, "success")
		return s.mustGetDeployment(ctx, d.ID), nil
	}

	// Verification exhausted retries — rollback from snapshot.
	msg := fmt.Sprintf("verification failed after %d attempts: %s", attempts, verifyResult.Message)
	return s.rollbackAfterVerifyFailure(ctx, actor, d, aclSnapshot, passwdSnapshot, msg)
}

// runVerifier invokes the ActiveVerifier with bounded retries and
// backoff. Returns the last result and the number of attempts made.
// The verifier context is bounded by HealthcheckTimeout so the deploy
// respects the operator's overall budget.
func (s *Service) runVerifier(ctx context.Context, aclBody, passwdBody string) (diagnostics.VerifyActiveResult, int) {
	// Pick a test user + topics from the rendered config. If we cannot
	// find a usable test subject, the verifier is invoked with empty
	// credentials/topics and will fail — the deploy is then rolled back.
	testUser, allowedTopic, deniedTopic := s.pickVerificationSubject(ctx, aclBody, passwdBody)
	password, hasPassword := s.lookupCleartextPassword(testUser)
	if testUser == "" || !hasPassword || allowedTopic == "" || deniedTopic == "" {
		return diagnostics.VerifyActiveResult{
			OK:              false,
			Stage:           "setup",
			Message:         "no test subject available (need a non-service user with an ACL grant and a known cleartext password)",
			PositiveMessage: "skipped: no test subject",
		}, 0
	}

	timeout := s.deployCfg.HealthcheckTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	var last diagnostics.VerifyActiveResult
	for attempt := 1; attempt <= verifyAttempts; attempt++ {
		verifyCtx, cancel := context.WithTimeout(ctx, timeout)
		result := s.verifier.VerifyActive(verifyCtx, diagnostics.VerifyActiveOptions{
			Config:           s.mosquittoCfg,
			Username:         testUser,
			Password:         password,
			AllowedTopic:     allowedTopic,
			DeniedTopic:      deniedTopic,
			DialTimeout:      2 * time.Second,
			RoundTripTimeout: 2 * time.Second,
			SubTimeout:       2 * time.Second,
		})
		cancel()
		last = result
		if result.OK {
			return result, attempt
		}
		if attempt < verifyAttempts {
			select {
			case <-time.After(time.Duration(attempt) * verifyBackoff):
			case <-ctx.Done():
				last.Message = fmt.Sprintf("verification cancelled after %d attempts: %v", attempt, ctx.Err())
				return last, attempt
			}
		}
	}
	return last, verifyAttempts
}

// pickVerificationSubject picks a non-service user from the rendered
// passwd, the first topic granted to that user, and a topic the user
// does NOT have access to (negative test). Returns empty strings when
// no suitable subject is available.
func (s *Service) pickVerificationSubject(_ context.Context, aclBody, passwdBody string) (user, allowed, denied string) {
	entries := mosquitto.ParsePasswdFile(passwdBody)
	rules := mosquitto.ParseACLFile(aclBody)
	if len(entries) == 0 || len(rules) == 0 {
		return "", "", ""
	}
	for _, e := range entries {
		// Skip the service user — it has # access so the negative test
		// would be vacuous.
		if e.Username == s.mosquittoCfg.Username && s.mosquittoCfg.Username != "" {
			continue
		}
		for _, r := range rules {
			if r.Principal != e.Username {
				continue
			}
			allowed = r.TopicFilter
			denied = r.TopicFilter + "/denied"
			return e.Username, allowed, denied
		}
	}
	return "", "", ""
}

// lookupCleartextPassword returns the cleartext password for the test
// user, if available in the in-memory store. When the store is not
// configured (e.g. in older test setups), returns false.
func (s *Service) lookupCleartextPassword(username string) (string, bool) {
	if s.passwordLookup == nil || username == "" {
		return "", false
	}
	return s.passwordLookup.CleartextPassword(username)
}

// rollbackAfterVerifyFailure rolls back from snapshot after verification
// exhausted retries and persists the appropriate status / audit event.
func (s *Service) rollbackAfterVerifyFailure(ctx context.Context, actor string, d *storage.Deployment, aclSnapshot, passwdSnapshot, msg string) (storage.Deployment, error) {
	rollbackCtx, cancelRollback := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancelRollback()
	rollbackErr := s.applier.Apply(rollbackCtx, aclSnapshot, passwdSnapshot, "", "")
	if rollbackErr != nil {
		full := fmt.Sprintf("%s; rollback also failed: %s", msg, rollbackErr.Error())
		_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "rollback_failed", full)
		s.emitAudit(ctx, actor, "deployment.rollback_failed", d.ID, "failure")
		return s.mustGetDeployment(ctx, d.ID), fmt.Errorf("verification rollback failed: %w", rollbackErr)
	}
	_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "rolled_back", msg)
	s.emitAudit(ctx, actor, "deployment.rolled_back", d.ID, "failure")
	return s.mustGetDeployment(ctx, d.ID), nil
}

// recordApplyFailure persists the appropriate status and audit event for
// an apply that did not reach the verification stage. Distinguishes
// between "failed but restored" (ErrApplyRestored — operator does not
// need to intervene) and "rollback also failed" (ErrRollbackFailed —
// broker is in an indeterminate state).
func (s *Service) recordApplyFailure(ctx context.Context, actor string, d *storage.Deployment, applyErr error) (storage.Deployment, error) {
	msg := applyErr.Error()
	if errors.Is(applyErr, mosquitto.ErrRollbackFailed) {
		_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "rollback_failed", msg)
		s.emitAudit(ctx, actor, "deployment.rollback_failed", d.ID, "failure")
		return s.mustGetDeployment(ctx, d.ID), fmt.Errorf("apply failed and rollback failed: %w", applyErr)
	}
	_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "failed", msg)
	s.emitAudit(ctx, actor, "deployment.failed", d.ID, "failure")
	return s.mustGetDeployment(ctx, d.ID), fmt.Errorf("apply config: %w", applyErr)
}

// List returns deployment records ordered newest first.
// Returns ErrDeployDisabled when the deploy mode is not configured.
func (s *Service) List(ctx context.Context, limit, offset int) ([]storage.Deployment, error) {
	if s.deployCfg.Mode == "" {
		return nil, ErrDeployDisabled
	}
	return s.deployStore.ListDeployments(ctx, limit, offset)
}

// emitAudit records an audit event for a deployment outcome if auditFn is set.
func (s *Service) emitAudit(ctx context.Context, actor, action string, deploymentID int64, result string) {
	if s.auditFn == nil {
		return
	}
	metadata := []byte(fmt.Sprintf(`{"deployment_id":%d}`, deploymentID))
	s.auditFn(ctx, actor, action, "deployment", fmt.Sprintf("%d", deploymentID), result, metadata)
}

// mustGetDeployment fetches the deployment record; returns a zero value on error
// (errors here are non-critical; the caller already has the outcome).
func (s *Service) mustGetDeployment(ctx context.Context, id int64) storage.Deployment {
	d, _ := s.deployStore.GetDeployment(ctx, id)
	return d
}
