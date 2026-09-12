// Package deploy orchestrates the deploy preview, apply, and rollback lifecycle
// for Mosquitto ACL and password configuration.
package deploy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
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

// Revision errors are mapped to HTTP 400/409 by the deploy handler.
var (
	ErrRevisionMissing  = errors.New("preview revision is required")
	ErrRevisionMismatch = errors.New("preview revision base no longer matches")
	ErrRevisionExpired  = errors.New("preview revision expired")
	ErrRevisionConsumed = errors.New("preview revision already consumed")
)

// maxDiffLines is the maximum number of lines in a returned unified diff.
const maxDiffLines = 500

const previewRevisionTTL = time.Hour

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
//	attempt 1: immediate after reloadSettleDelay
//	attempt 2: after verifyBackoff (1s)
//	attempt 3: after 2*verifyBackoff (2s)
//
// reloadSettleDelay is a short pause between applier.Apply returning and
// the verifier starting. Mosquitto processes SIGHUP asynchronously in its
// event loop; without a settle, the verifier can race the reload and
// observe the previous ACL (default-deny for unknown users, but full
// allow for any user without a matching rule — which is what the new
// config has during the race window). 1s is enough on a quiet broker;
// the bounded retries handle longer reload delays.
const (
	verifyAttempts    = 3
	verifyBackoff     = 1 * time.Second
	reloadSettleDelay = 1 * time.Second
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

// previewRevisionStore is kept separate from DeploymentStore because preview
// revisions are immutable, short-lived inputs to an apply, not deployment
// lifecycle records.
type previewRevisionStore interface {
	InsertPreviewRevision(ctx context.Context, revision *storage.PreviewRevision) error
	GetPreviewRevision(ctx context.Context, id string) (storage.PreviewRevision, error)
	ConsumePreviewRevision(ctx context.Context, id string, appliedAt time.Time) error
}

// MQTTUserLister is the subset of storage.Store needed to list MQTT users.
type MQTTUserLister interface {
	ListMQTTUsers(ctx context.Context) ([]storage.MQTTUser, error)
}

// OrphanRuleLister is the optional storage capability used to identify ACL
// rules that would otherwise be rendered for disabled or deleted users. The
// production storage.Store implements both MQTTUserLister and this interface;
// keeping it optional preserves the small in-memory fakes used by older
// callers.
type OrphanRuleLister interface {
	FindOrphanRules(ctx context.Context) ([]storage.ACLRuleRow, error)
}

// MutationCoordinator serializes managed MQTT/ACL mutations with the
// validation-to-apply window. Production storage implements this so a
// revision cannot become stale after it has been validated for apply.
type MutationCoordinator interface {
	LockMutations()
	UnlockMutations()
}

// FileReader abstracts reading current on-disk config files (injectable for testing).
type FileReader func(path string) (string, error)

// AuditFunc is a function that records an audit event.
type AuditFunc func(ctx context.Context, actor, action, resourceType, resourceID, result string, metadata []byte)

// PreviewResult contains the diff output and rendered content for a deploy preview.
type PreviewResult struct {
	RevisionID         string               `json:"revision_id"`
	BaseACLHash        string               `json:"base_acl_hash"`
	BasePasswdHash     string               `json:"base_passwd_hash"`
	RenderedACLHash    string               `json:"rendered_acl_hash"`
	RenderedPasswdHash string               `json:"rendered_passwd_hash"`
	OrphanRules        []storage.ACLRuleRow `json:"orphan_rules"`
	ACLDiff            string               `json:"acl_diff"`
	PasswdDiff         string               `json:"passwd_diff"`
	ACLBody            string               `json:"acl_body"`
	PasswdBody         string               `json:"-"`
	Summary            ChangeSummary        `json:"summary"`
	HasChanges         bool                 `json:"has_changes"`
}

// Service orchestrates deploy preview, apply, and history.
type Service struct {
	mu                  sync.Mutex
	applier             mosquitto.Applier
	aclStore            acl.Store
	mqttStore           MQTTUserLister
	orphanRuleLister    OrphanRuleLister
	mutationCoordinator MutationCoordinator
	deployStore         DeploymentStore
	revisionStore       previewRevisionStore
	verifier            ActiveVerifier
	passwordLookup      CleartextPasswordLookup
	readFile            FileReader
	mosquittoCfg        config.MosquittoConfig
	deployCfg           config.DeployConfig
	auditFn             AuditFunc
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
	revisionStore, ok := deployStore.(previewRevisionStore)
	if !ok {
		revisionStore = newMemoryPreviewRevisionStore()
	}
	orphanRuleLister, _ := mqttStore.(OrphanRuleLister)
	mutationCoordinator, _ := mqttStore.(MutationCoordinator)
	return &Service{
		applier:             applier,
		aclStore:            aclStore,
		mqttStore:           mqttStore,
		orphanRuleLister:    orphanRuleLister,
		mutationCoordinator: mutationCoordinator,
		deployStore:         deployStore,
		revisionStore:       revisionStore,
		verifier:            verifier,
		passwordLookup:      passwordLookup,
		readFile:            defaultFileReader,
		mosquittoCfg:        mosquittoCfg,
		deployCfg:           deployCfg,
		auditFn:             auditFn,
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

// memoryPreviewRevisionStore keeps the existing service fakes useful while
// production uses the SQLite-backed storage.Store implementation.
type memoryPreviewRevisionStore struct {
	mu        sync.Mutex
	revisions map[string]storage.PreviewRevision
}

func newMemoryPreviewRevisionStore() *memoryPreviewRevisionStore {
	return &memoryPreviewRevisionStore{revisions: make(map[string]storage.PreviewRevision)}
}

func (s *memoryPreviewRevisionStore) InsertPreviewRevision(_ context.Context, revision *storage.PreviewRevision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.revisions[revision.ID]; exists {
		return fmt.Errorf("preview revision %q already exists", revision.ID)
	}
	s.revisions[revision.ID] = *revision
	return nil
}

func (s *memoryPreviewRevisionStore) GetPreviewRevision(_ context.Context, id string) (storage.PreviewRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revision, ok := s.revisions[id]
	if !ok {
		return storage.PreviewRevision{}, storage.ErrPreviewRevisionNotFound
	}
	return revision, nil
}

func (s *memoryPreviewRevisionStore) ConsumePreviewRevision(_ context.Context, id string, appliedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	revision, ok := s.revisions[id]
	if !ok {
		return storage.ErrPreviewRevisionNotFound
	}
	if !revision.AppliedAt.IsZero() {
		return storage.ErrPreviewRevisionConsumed
	}
	revision.AppliedAt = appliedAt
	s.revisions[id] = revision
	return nil
}

func newRevisionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate preview revision id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func contentHash(body string) string {
	hash := sha256.Sum256([]byte(body))
	return hex.EncodeToString(hash[:])
}

// render fetches rules and users from the stores and produces rendered ACL and passwd bodies.
// The service user block (MCM_MOSQUITTO_USERNAME) and its ACL grants are
// always emitted so the deploy healthcheck and the broker events feed
// keep working after every apply. The service user's existing hash on
// disk is reused when present — re-hashing on every render would make
// has_changes non-idempotent (salt is random), forcing the applier to
// rewrite the file and SIGHUP the broker on every preview/apply cycle
// even when nothing changed.
func (s *Service) render(ctx context.Context) (aclBody, passwdBody string, orphanRules []storage.ACLRuleRow, err error) {
	rules, err := s.aclStore.ListRules(ctx)
	if err != nil {
		return "", "", nil, fmt.Errorf("list acl rules: %w", err)
	}

	orphanRules = make([]storage.ACLRuleRow, 0)
	if s.orphanRuleLister != nil {
		orphanRules, err = s.orphanRuleLister.FindOrphanRules(ctx)
		if err != nil {
			return "", "", nil, fmt.Errorf("find orphan acl rules: %w", err)
		}
		if orphanRules == nil {
			orphanRules = make([]storage.ACLRuleRow, 0)
		}
	}

	users, err := s.mqttStore.ListMQTTUsers(ctx)
	if err != nil {
		return "", "", nil, fmt.Errorf("list mqtt users: %w", err)
	}

	// Read the on-disk passwd so we can preserve the service user's
	// existing hash. Preview/Apply already read it for the diff, but
	// render() must not depend on those callers. If the file is absent
	// (first boot) we recompute the hash.
	existingPasswd, err := s.readFile(s.deployCfg.PasswdPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("read current passwd file: %w", err)
	}
	existingEntries := mosquitto.ParsePasswdFile(existingPasswd)
	existingHashByUser := make(map[string]string, len(existingEntries))
	for _, e := range existingEntries {
		existingHashByUser[e.Username] = e.Hash
	}

	// Orphan rules are surfaced in the preview but excluded from the rendered
	// broker configuration. They cannot authenticate because their user is
	// disabled or absent, and retaining them would make the rendered state
	// disagree with the active user set.
	orphanPrincipals := make(map[string]struct{}, len(orphanRules))
	for _, rule := range orphanRules {
		orphanPrincipals[rule.Principal] = struct{}{}
	}
	activeRules := make([]acl.Rule, 0, len(rules))
	for _, rule := range rules {
		if _, orphan := orphanPrincipals[rule.Principal]; orphan {
			continue
		}
		activeRules = append(activeRules, rule)
	}

	// Build the ACL body: active managed rules + service user block.
	allRules := make([]acl.Rule, 0, len(activeRules)+1)
	allRules = append(allRules, activeRules...)
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
				return "", "", nil, fmt.Errorf("hash service user password: %w", hashErr)
			}
		}
		entries = append(entries, mosquitto.PasswdEntry{
			Username: s.mosquittoCfg.Username,
			Hash:     hash,
		})
	}
	passwdBody = mosquitto.RenderPasswdFile(entries)

	return aclBody, passwdBody, orphanRules, nil
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

// supportedPasswdHashRe matches the password formats supported by this
// project: Mosquitto's $7$ PBKDF2-SHA512 format and the bcrypt formats
// emitted by mosquitto_passwd ($2a$, $2b$, $2y$, $2x$, or legacy $2$).
var supportedPasswdHashRe = regexp.MustCompile(`^([^:\s]+):((?:\$7\$|\$2[abxy\$])[^\s]+)\s*$`)

// redactPasswdHashes replaces every supported password hash in a passwd file body
// with a redacted marker that shows the algorithm + prefix + length.
// This is required by issue #296 (acceptance criterion 3): the deploy
// API must NEVER leak the broker's password hashes back to the
// operator or the UI — only the metadata needed to recognise the
// change set.
//
// Output format per user line:
//
//	user:REDACTED  algo=$2a$  hash_len=60  prefix=$2a$10$
//
// Comments (lines starting with `#`) and blank lines are preserved
// as-is. The trailing newline (if any) is preserved too.
func redactPasswdHashes(body string) string {
	lines := strings.Split(body, "\n")
	trailingNL := false
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		trailingNL = true
		lines = lines[:len(lines)-1]
	}
	redacted := make([]string, 0, len(lines))
	for _, line := range lines {
		m := supportedPasswdHashRe.FindStringSubmatch(line)
		if m == nil {
			redacted = append(redacted, line)
		} else {
			username, hash := m[1], m[2]
			algorithm, prefix := passwdHashMetadata(hash)
			redacted = append(redacted, fmt.Sprintf("%s:REDACTED  algo=%s  hash_len=%d  prefix=%s",
				username, algorithm, len(hash), prefix))
		}
	}
	out := strings.Join(redacted, "\n")
	if trailingNL {
		out += "\n"
	}
	return out
}

func passwdHashMetadata(hash string) (algorithm, prefix string) {
	if strings.HasPrefix(hash, "$7$") {
		algorithm = "$7$"
		prefix = algorithm
		if end := strings.IndexByte(hash[3:], '$'); end >= 0 {
			prefix = hash[:4+end]
		}
		return algorithm, prefix
	}

	algorithm = hash[:4]
	if strings.HasPrefix(hash, "$2$") {
		algorithm = "$2$"
	}
	prefix = algorithm
	if end := strings.IndexByte(hash[len(algorithm):], '$'); end >= 0 {
		prefix = hash[:len(algorithm)+end+1]
	}
	return algorithm, prefix
}

// ChangeSummary is the per-preview summary of what would change if
// the operator applied the rendered configuration. It is designed to
// be safe to return over the API: only counts and rotation flags,
// never the actual hash values.
type ChangeSummary struct {
	UsersAdded       int  `json:"users_added"`   // new username not in current
	UsersRemoved     int  `json:"users_removed"` // username in current, not in rendered
	UsersRotated     int  `json:"users_rotated"` // same username, different hash
	HasPasswdChanges bool `json:"has_passwd_changes"`

	TopicsAdded   int  `json:"topics_added"`
	TopicsRemoved int  `json:"topics_removed"`
	HasACLChanges bool `json:"has_acl_changes"`
}

// summarizeChanges diffs the passwd and ACL files and produces a
// summary that does NOT leak hashes.
func summarizeChanges(currentPasswd, renderedPasswd, currentACL, renderedACL string) ChangeSummary {
	var s ChangeSummary

	currentUsers := parsePasswdUsers(currentPasswd)
	renderedUsers := parsePasswdUsers(renderedPasswd)
	for u, curHash := range currentUsers {
		newHash, ok := renderedUsers[u]
		if !ok {
			s.UsersRemoved++
			continue
		}
		if newHash != curHash {
			s.UsersRotated++
		}
		delete(renderedUsers, u)
	}
	for range renderedUsers {
		s.UsersAdded++
	}
	if s.UsersAdded+s.UsersRemoved+s.UsersRotated > 0 {
		s.HasPasswdChanges = true
	}

	currentTopics := parseACLTopics(currentACL)
	renderedTopics := parseACLTopics(renderedACL)
	for t := range currentTopics {
		if _, ok := renderedTopics[t]; !ok {
			s.TopicsRemoved++
		}
		delete(renderedTopics, t)
	}
	for range renderedTopics {
		s.TopicsAdded++
	}
	if s.TopicsAdded+s.TopicsRemoved > 0 {
		s.HasACLChanges = true
	}
	return s
}

// parsePasswdUsers returns username -> hash for each non-comment
// non-blank line.
func parsePasswdUsers(body string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(body, "\n") {
		m := supportedPasswdHashRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out[m[1]] = m[2]
	}
	return out
}

// parseACLTopics returns a set of "<principal> <topic> <perm>" tuples
// that appear in the rendered ACL body.
func parseACLTopics(body string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "topic" {
			continue
		}
		out[strings.Join(fields, " ")] = struct{}{}
	}
	return out
}

// Preview returns unified diffs between on-disk files and the rendered configuration.
// Preview is read-only and does not acquire the apply mutex.
func (s *Service) Preview(ctx context.Context, actor string) (PreviewResult, error) {
	if s.deployCfg.Mode == "" {
		return PreviewResult{}, ErrDeployDisabled
	}

	aclBody, passwdBody, orphanRules, err := s.render(ctx)
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

	passwdDiff, err := unifiedDiff("current", "rendered",
		redactPasswdHashes(currentPasswd), redactPasswdHashes(passwdBody))
	if err != nil {
		return PreviewResult{}, err
	}

	hasChanges := aclDiff != "" || passwdDiff != ""
	revisionID, err := newRevisionID()
	if err != nil {
		return PreviewResult{}, err
	}
	revision := &storage.PreviewRevision{
		ID:                 revisionID,
		Actor:              actor,
		BaseACLHash:        contentHash(currentACL),
		BasePasswdHash:     contentHash(currentPasswd),
		RenderedACLHash:    contentHash(aclBody),
		RenderedPasswdHash: contentHash(passwdBody),
		ACLRendered:        aclBody,
		PasswdRendered:     passwdBody,
		CreatedAt:          time.Now().UTC(),
	}
	if err := s.revisionStore.InsertPreviewRevision(ctx, revision); err != nil {
		return PreviewResult{}, fmt.Errorf("store preview revision: %w", err)
	}
	summary := summarizeChanges(currentPasswd, passwdBody, currentACL, aclBody)
	hasChanges = hasChanges || summary.HasPasswdChanges || summary.HasACLChanges

	if s.auditFn != nil {
		result := "success"
		s.auditFn(ctx, actor, "deployment.preview", "deployment", "", result, nil)
	}

	return PreviewResult{
		RevisionID:         revisionID,
		BaseACLHash:        revision.BaseACLHash,
		BasePasswdHash:     revision.BasePasswdHash,
		RenderedACLHash:    revision.RenderedACLHash,
		RenderedPasswdHash: revision.RenderedPasswdHash,
		OrphanRules:        orphanRules,
		ACLDiff:            aclDiff,
		PasswdDiff:         passwdDiff,
		ACLBody:            aclBody,
		PasswdBody:         passwdBody,
		Summary:            summary,
		HasChanges:         hasChanges,
	}, nil
}

// Apply applies an immutable preview revision, verifies the broker is serving
// it (positive + negative tests with bounded retries), and rolls back on any
// failure. The variadic form keeps older in-process callers compiling while
// the HTTP handler requires exactly one revision ID.
func (s *Service) Apply(ctx context.Context, actor string, revisionIDs ...string) (storage.Deployment, error) {
	if s.deployCfg.Mode == "" {
		return storage.Deployment{}, ErrDeployDisabled
	}
	if len(revisionIDs) > 1 {
		return storage.Deployment{}, ErrRevisionMissing
	}
	if len(revisionIDs) == 0 {
		preview, err := s.Preview(ctx, actor)
		if err != nil {
			return storage.Deployment{}, err
		}
		revisionIDs = []string{preview.RevisionID}
	}
	return s.applyRevision(ctx, actor, revisionIDs[0])
}

func (s *Service) applyRevision(ctx context.Context, actor, revisionID string) (storage.Deployment, error) {
	if revisionID == "" {
		return storage.Deployment{}, ErrRevisionMissing
	}

	if !s.mu.TryLock() {
		return storage.Deployment{}, ErrDeployInProgress
	}
	defer s.mu.Unlock()
	if s.mutationCoordinator != nil {
		s.mutationCoordinator.LockMutations()
		defer s.mutationCoordinator.UnlockMutations()
	}

	revision, err := s.revisionStore.GetPreviewRevision(ctx, revisionID)
	if errors.Is(err, storage.ErrPreviewRevisionNotFound) {
		return storage.Deployment{}, fmt.Errorf("%w: %s", ErrRevisionMissing, revisionID)
	}
	if err != nil {
		return storage.Deployment{}, fmt.Errorf("get preview revision: %w", err)
	}
	if !revision.AppliedAt.IsZero() {
		return storage.Deployment{}, ErrRevisionConsumed
	}
	if time.Now().UTC().After(revision.CreatedAt.Add(previewRevisionTTL)) {
		return storage.Deployment{}, ErrRevisionExpired
	}

	// Snapshot current on-disk files.
	aclSnapshot, err := s.readFile(s.deployCfg.ACLPath)
	if err != nil {
		return storage.Deployment{}, fmt.Errorf("snapshot acl file: %w", err)
	}
	passwdSnapshot, err := s.readFile(s.deployCfg.PasswdPath)
	if err != nil {
		return storage.Deployment{}, fmt.Errorf("snapshot passwd file: %w", err)
	}
	if contentHash(aclSnapshot) != revision.BaseACLHash || contentHash(passwdSnapshot) != revision.BasePasswdHash {
		return storage.Deployment{}, ErrRevisionMismatch
	}
	currentACLRendered, currentPasswdRendered, _, err := s.render(ctx)
	if err != nil {
		return storage.Deployment{}, fmt.Errorf("validate rendered revision: %w", err)
	}
	if contentHash(currentACLRendered) != revision.RenderedACLHash || contentHash(currentPasswdRendered) != revision.RenderedPasswdHash {
		return storage.Deployment{}, ErrRevisionMismatch
	}
	aclRendered, passwdRendered := revision.ACLRendered, revision.PasswdRendered

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
		if err := s.revisionStore.ConsumePreviewRevision(ctx, revisionID, time.Now().UTC()); err != nil {
			if errors.Is(err, storage.ErrPreviewRevisionConsumed) {
				return storage.Deployment{}, ErrRevisionConsumed
			}
			return storage.Deployment{}, fmt.Errorf("consume preview revision: %w", err)
		}
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
