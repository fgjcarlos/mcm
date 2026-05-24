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

// HealthChecker is a function that checks MQTT broker connectivity.
type HealthChecker func(ctx context.Context, cfg config.MosquittoConfig) diagnostics.MQTTResult

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
	ACLDiff     string `json:"acl_diff"`
	PasswdDiff  string `json:"passwd_diff"`
	ACLBody     string `json:"acl_body"`
	PasswdBody  string `json:"passwd_body"`
	HasChanges  bool   `json:"has_changes"`
}

// Service orchestrates deploy preview, apply, and history.
type Service struct {
	mu           sync.Mutex
	applier      mosquitto.Applier
	aclStore     acl.Store
	mqttStore    MQTTUserLister
	deployStore  DeploymentStore
	healthCheck  HealthChecker
	readFile     FileReader
	mosquittoCfg config.MosquittoConfig
	deployCfg    config.DeployConfig
	auditFn      AuditFunc
}

// NewService constructs a deploy Service.
func NewService(
	applier mosquitto.Applier,
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployStore DeploymentStore,
	healthCheck HealthChecker,
	mosquittoCfg config.MosquittoConfig,
	deployCfg config.DeployConfig,
	auditFn AuditFunc,
) *Service {
	return &Service{
		applier:      applier,
		aclStore:     aclStore,
		mqttStore:    mqttStore,
		deployStore:  deployStore,
		healthCheck:  healthCheck,
		readFile:     defaultFileReader,
		mosquittoCfg: mosquittoCfg,
		deployCfg:    deployCfg,
		auditFn:      auditFn,
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
func (s *Service) render(ctx context.Context) (aclBody, passwdBody string, err error) {
	rules, err := s.aclStore.ListRules(ctx)
	if err != nil {
		return "", "", fmt.Errorf("list acl rules: %w", err)
	}

	users, err := s.mqttStore.ListMQTTUsers(ctx)
	if err != nil {
		return "", "", fmt.Errorf("list mqtt users: %w", err)
	}

	aclBody = mosquitto.RenderACLFile(rules)

	entries := make([]mosquitto.PasswdEntry, 0, len(users))
	for _, u := range users {
		if !u.Disabled {
			entries = append(entries, mosquitto.PasswdEntry{
				Username: u.Username,
				Hash:     u.PasswordHash,
			})
		}
	}
	passwdBody = mosquitto.RenderPasswdFile(entries)

	return aclBody, passwdBody, nil
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

// Apply applies the current rendered configuration, runs a healthcheck, and rolls back on failure.
// Apply serializes concurrent calls via a mutex.
func (s *Service) Apply(ctx context.Context, actor string) (storage.Deployment, error) {
	if s.deployCfg.Mode == "" {
		return storage.Deployment{}, ErrDeployDisabled
	}

	s.mu.Lock()
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

	// Insert pending deployment record.
	d := &storage.Deployment{
		Actor:          actor,
		Status:         "pending",
		ACLSnapshot:    aclSnapshot,
		PasswdSnapshot: passwdSnapshot,
		ACLRendered:    aclRendered,
		PasswdRendered: passwdRendered,
	}
	if err := s.deployStore.InsertDeployment(ctx, d); err != nil {
		return storage.Deployment{}, fmt.Errorf("insert deployment: %w", err)
	}

	// Apply rendered configuration.
	if err := s.applier.Apply(ctx, aclRendered, passwdRendered); err != nil {
		msg := err.Error()
		_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "failed", msg)
		s.emitAudit(ctx, actor, "deployment.failed", d.ID, "failure")
		return s.mustGetDeployment(ctx, d.ID), fmt.Errorf("apply config: %w", err)
	}

	// Healthcheck.
	timeout := s.deployCfg.HealthcheckTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	hcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := s.healthCheck(hcCtx, s.mosquittoCfg)
	if result.OK {
		_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "applied", "")
		s.emitAudit(ctx, actor, "deployment.applied", d.ID, "success")
		return s.mustGetDeployment(ctx, d.ID), nil
	}

	// Healthcheck failed: rollback.
	rollbackErr := s.applier.Apply(ctx, aclSnapshot, passwdSnapshot)
	if rollbackErr != nil {
		msg := fmt.Sprintf("healthcheck failed: %s; rollback also failed: %s", result.Message, rollbackErr.Error())
		_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "rollback_failed", msg)
		s.emitAudit(ctx, actor, "deployment.rollback_failed", d.ID, "failure")
		return s.mustGetDeployment(ctx, d.ID), fmt.Errorf("deploy rollback failed: %w", rollbackErr)
	}

	msg := fmt.Sprintf("healthcheck failed: %s", result.Message)
	_ = s.deployStore.UpdateDeploymentStatus(ctx, d.ID, "rolled_back", msg)
	s.emitAudit(ctx, actor, "deployment.rolled_back", d.ID, "failure")
	return s.mustGetDeployment(ctx, d.ID), nil
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
