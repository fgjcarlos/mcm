package listener

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/fgjcarlos/mcm/internal/certificate"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const (
	// ListenerPreviewTTL is the lifetime of an unapplied listener preview.
	ListenerPreviewTTL = time.Hour
	// listenerRecoveryTimeout bounds the compensating broker restart that
	// runs when the listener specs could not be persisted and the previous
	// broker config could not be restored either.
	listenerRecoveryTimeout = 30 * time.Second
)

var (
	// ErrListenerPreviewNotFound is returned when a listener preview does not exist.
	ErrListenerPreviewNotFound = errors.New("listener preview not found")
	// ErrListenerPreviewExpired is returned when a listener preview has expired.
	ErrListenerPreviewExpired = errors.New("listener preview expired")
	// ErrListenerPreviewConsumed is returned when a listener preview was already applied.
	ErrListenerPreviewConsumed = errors.New("listener preview consumed")
	// ErrListenerRestartNotConfirmed is returned when a restart-required preview lacks confirmation.
	ErrListenerRestartNotConfirmed = errors.New("listener restart not confirmed")
	// ErrListenerComposeUnmapped is returned when a requested listener port is not published by Compose.
	ErrListenerComposeUnmapped = errors.New("listener port is not mapped by compose")
	// ErrListenerApplyInProgress is returned when another listener apply is active.
	ErrListenerApplyInProgress = errors.New("listener apply in progress")
)

// ListenerRepository persists listener specification rows.
type ListenerRepository interface {
	ListListenerSpecs(ctx context.Context) ([]storage.ListenerSpecRow, error)
	ReplaceAllListenerSpecs(ctx context.Context, rows []storage.ListenerSpecRow) error
}

// ComposeReader exposes the configured Compose host ports required by listener previews.
type ComposeReader interface {
	IsConfigured() bool
	Disabled() bool
	HostPorts(ctx context.Context) ([]int, error)
}

// ListenerAuditFunc records a listener lifecycle audit event.
type ListenerAuditFunc func(ctx context.Context, actor, action, resourceType, resourceID, result string, metadata []byte)

// CertificateResolver resolves the broker-side paths of a certificate
// profile.
//
// The resolved paths point at the active revision of the profile, so they
// stay stable across rotations: a rotation replaces the material behind
// the paths, it does not change the rendered configuration.
type CertificateResolver interface {
	ResolveProfile(ctx context.Context, profileName string) (certificate.ResolvedProfile, error)
}

// ListenerConfWriter renders and persists listener specs to the broker
// mosquitto.conf file. Implementations must be idempotent: applying the
// same listener specs twice leaves the file content unchanged. Snapshot
// returns the on-disk bytes captured BEFORE the apply; Restore writes
// them back atomically when the post-write restart fails so the broker
// can never see a half-applied configuration.
//
// nil writers are tolerated for tests and for the dev compose path
// where MCM_MOSQUITTO_CONFIG_DIR is unset: Apply then skips the file
// splice and falls back to the previous restart + SQLite behaviour.
type ListenerConfWriter interface {
	Write(specs []listeners.ListenerSpec) error
	Snapshot() ([]byte, error)
	Restore(snapshot []byte) error
}

// ListenerPreviewResult is the immutable outcome of rendering a listener preview.
type ListenerPreviewResult struct {
	RevisionID    string                    `json:"revision_id"`
	BaseHash      string                    `json:"base_hash"`
	RenderedHash  string                    `json:"rendered_hash"`
	NeedsRestart  bool                      `json:"needs_restart"`
	Diff          string                    `json:"diff"`
	Rendered      string                    `json:"rendered"`
	Issues        []listeners.Issue         `json:"issues"`
	Warnings      []listeners.OptionWarning `json:"warnings"`
	ComposeStatus ComposePreviewStatus      `json:"compose_status"`
	CreatedAt     time.Time                 `json:"created_at"`
}

// ComposePreviewStatus reports Compose port coverage for a listener preview.
type ComposePreviewStatus struct {
	ComposePath   string `json:"compose_path"`
	MappedPorts   []int  `json:"mapped_ports"`
	UnmappedPorts []int  `json:"unmapped_ports"`
	Disabled      bool   `json:"disabled"`
}

// PreviewOptions controls listener preview safety checks.
type PreviewOptions struct {
	Confirm bool
}

// ListenerServiceOption configures a ListenerService.
type ListenerServiceOption func(*ListenerService)

// WithListenerClock sets the service clock, primarily for deterministic expiry tests.
func WithListenerClock(clock func() time.Time) ListenerServiceOption {
	return func(s *ListenerService) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithListenerRevisionID sets the preview revision identifier generator.
func WithListenerRevisionID(newRevisionID func() (string, error)) ListenerServiceOption {
	return func(s *ListenerService) {
		if newRevisionID != nil {
			s.newRevisionID = newRevisionID
		}
	}
}

// WithListenerConfWriter injects the broker conf writer used to splice
// listener directives into mosquitto.conf. When w is nil the apply path
// falls back to the legacy behaviour (restart + SQLite only); this
// matches the dev compose default where MCM_MOSQUITTO_CONFIG_DIR is
// unset and there is nothing to splice.
func WithListenerConfWriter(w ListenerConfWriter) ListenerServiceOption {
	return func(s *ListenerService) {
		s.confWriter = w
	}
}

// WithListenerCertificateResolver injects the resolver that maps a
// certificate profile to the broker-side paths of its active material.
//
// Without a resolver a listener cannot be bound to a certificate profile:
// the service refuses the binding instead of rendering a path the broker
// cannot read.
func WithListenerCertificateResolver(resolver CertificateResolver) ListenerServiceOption {
	return func(s *ListenerService) {
		s.resolver = resolver
	}
}

// WithListenerCatalog injects the directive catalog used to gate the
// per-listener TLS directives.
//
// Without a catalog the service refuses every certificate binding, because
// it cannot prove that the broker understands the directives it would
// render.
func WithListenerCatalog(cat *catalog.Catalog) ListenerServiceOption {
	return func(s *ListenerService) {
		s.catalog = cat
	}
}

type previewEntry struct {
	result   ListenerPreviewResult
	rows     []storage.ListenerSpecRow
	consumed bool
	expires  time.Time
}

// ListenerService previews and applies listener configuration changes.
type ListenerService struct {
	mu            sync.Mutex
	store         ListenerRepository
	composeReader ComposeReader
	restartRunner ListenerRestartRunner
	confWriter    ListenerConfWriter
	auditFn       ListenerAuditFunc
	resolver      CertificateResolver
	catalog       *catalog.Catalog
	clock         func() time.Time
	newRevisionID func() (string, error)
	inProgress    bool
	previews      map[string]previewEntry
}

// NewListenerService constructs a listener preview and apply service.
func NewListenerService(store ListenerRepository, composeReader ComposeReader, restartRunner ListenerRestartRunner, auditFn ListenerAuditFunc, opts ...ListenerServiceOption) *ListenerService {
	s := &ListenerService{
		store:         store,
		composeReader: composeReader,
		restartRunner: restartRunner,
		auditFn:       auditFn,
		clock:         func() time.Time { return time.Now().UTC() },
		newRevisionID: newListenerRevisionID,
		previews:      make(map[string]previewEntry),
	}
	if s.restartRunner == nil {
		s.restartRunner = &NoopRestartRunner{}
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// List returns listener specifications currently persisted by the repository.
func (s *ListenerService) List(ctx context.Context) ([]storage.ListenerSpecRow, error) {
	if s.store == nil {
		return nil, errors.New("listener repository is required")
	}
	return s.store.ListListenerSpecs(ctx)
}

// Preview validates, renders, and stores an immutable listener change preview.
func (s *ListenerService) Preview(ctx context.Context, desired []listeners.ListenerSpec, opts PreviewOptions) (ListenerPreviewResult, error) {
	if s.store == nil {
		return ListenerPreviewResult{}, errors.New("listener repository is required")
	}
	currentRows, err := s.store.ListListenerSpecs(ctx)
	if err != nil {
		return ListenerPreviewResult{}, fmt.Errorf("list listener specs: %w", err)
	}
	current, err := convertAndNormalize(currentRows)
	if err != nil {
		return ListenerPreviewResult{}, fmt.Errorf("convert current listener specs: %w", err)
	}
	normalized, issues := normalizeDesired(desired)
	// The certificate material of the bound profiles is resolved before the
	// validation: the rendered block carries the broker-side paths, and the
	// validator has to see them to check the TLS directives as a whole.
	// The helper reports every binding problem as an issue and returns no
	// error, so the desired resolution cannot fail here.
	resolved, resolveIssues, _ := s.resolveTLSMaterial(ctx, normalized)
	issues = append(issues, resolveIssues...)
	// A broken binding of the CURRENT configuration does not block the
	// change: the operator must be able to repair it by rebinding the
	// listener. It is surfaced as a warning instead.
	currentResolved, currentIssues, err := s.resolveTLSMaterial(ctx, current)
	if err != nil {
		return ListenerPreviewResult{Issues: issues, Warnings: []listeners.OptionWarning{}}, err
	}
	validationIssues, warnings := listeners.Validate(resolved)
	for _, issue := range currentIssues {
		warnings = append(warnings, listeners.OptionWarning{
			Kind:       "tls_binding",
			ListenerID: issue.ListenerID,
			Key:        issue.Directive,
			Message:    issue.Message,
		})
	}
	issues = append(issues, validationIssues...)
	if len(issues) > 0 {
		sort.Slice(issues, func(i, j int) bool { return issues[i].Message < issues[j].Message })
		return ListenerPreviewResult{Issues: issues, Warnings: warnings}, fmt.Errorf("validate listener specs: %w", errors.New(issues[0].Message))
	}
	desiredRendered, err := listeners.RenderAll(resolved)
	if err != nil {
		return ListenerPreviewResult{}, fmt.Errorf("render listener specs: %w", err)
	}
	currentRendered := ""
	if len(currentResolved) > 0 {
		currentRendered, err = listeners.RenderAll(currentResolved)
		if err != nil {
			return ListenerPreviewResult{}, fmt.Errorf("render current listener specs: %w", err)
		}
	}
	diff, err := listenerUnifiedDiff(currentRendered, desiredRendered)
	if err != nil {
		return ListenerPreviewResult{}, err
	}
	now := s.clock().UTC()
	revisionID, err := s.newRevisionID()
	if err != nil {
		return ListenerPreviewResult{}, fmt.Errorf("generate listener preview revision: %w", err)
	}
	result := ListenerPreviewResult{
		RevisionID:   revisionID,
		BaseHash:     listenerHash(currentRendered),
		RenderedHash: listenerHash(desiredRendered),
		NeedsRestart: listenerHash(currentRendered) != listenerHash(desiredRendered) || currentRendered == "",
		Diff:         diff,
		Rendered:     desiredRendered,
		Issues:       []listeners.Issue{},
		Warnings:     warnings,
		CreatedAt:    now,
	}
	result.ComposeStatus, err = s.composeStatus(ctx, normalized)
	if err != nil {
		return ListenerPreviewResult{}, err
	}
	rows := listenerRows(normalized)
	s.mu.Lock()
	s.gcPreviewsLocked(now)
	s.previews[revisionID] = previewEntry{result: result, rows: rows, expires: now.Add(ListenerPreviewTTL)}
	s.mu.Unlock()
	if len(result.ComposeStatus.UnmappedPorts) > 0 && !opts.Confirm {
		return result, ErrListenerComposeUnmapped
	}
	return result, nil
}

// Apply restarts the broker if necessary and persists the immutable preview rows.
//
// The contract is split into three ordered steps when a conf writer is
// configured:
//
//  1. Snapshot the current mosquitto.conf bytes BEFORE writing anything.
//  2. Splice the rendered listener directives into mosquitto.conf via
//     the writer. If this fails, the broker is untouched and SQLite is
//     untouched — the apply is rejected with the writer error wrapped in
//     ErrListenerRestartFailed so the audit log flags the failure.
//  3. Restart the broker. If the restart fails, the snapshot from step 1
//     is restored BEFORE the function returns, so the broker never sees a
//     half-applied configuration. SQLite is not touched on the rollback
//     path either, so the preview remains valid for retry.
//
// When no conf writer is configured the legacy behaviour (restart +
// SQLite) is preserved. This matches the dev compose default where
// MCM_MOSQUITTO_CONFIG_DIR is unset and there is nothing to splice.
func (s *ListenerService) Apply(ctx context.Context, revisionID string, confirm bool) error {
	now := s.clock().UTC()
	s.mu.Lock()
	entry, found := s.previews[revisionID]
	if !found {
		s.mu.Unlock()
		return ErrListenerPreviewNotFound
	}
	if now.After(entry.expires) {
		delete(s.previews, revisionID)
		s.mu.Unlock()
		return ErrListenerPreviewExpired
	}
	if entry.consumed {
		s.mu.Unlock()
		return ErrListenerPreviewConsumed
	}
	if entry.result.NeedsRestart && !confirm {
		s.mu.Unlock()
		return ErrListenerRestartNotConfirmed
	}
	if s.inProgress {
		s.mu.Unlock()
		return ErrListenerApplyInProgress
	}
	s.inProgress = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inProgress = false
		s.mu.Unlock()
	}()

	specs, err := rowsToSpecs(entry.rows)
	if err != nil {
		return fmt.Errorf("decode listener rows: %w", err)
	}
	// The certificate material is resolved again on apply: the preview and
	// the apply must render exactly the same block, and the active
	// revision of a profile can change between both.
	specs, resolveIssues, err := s.resolveTLSMaterial(ctx, specs)
	if err != nil {
		return fmt.Errorf("resolve certificate material: %w", err)
	}
	if len(resolveIssues) > 0 {
		return fmt.Errorf("%w: %s", ErrListenerRestartFailed, resolveIssues[0].Message)
	}

	var snapshotTaken bool
	var snapshotBytes []byte
	if s.confWriter != nil {
		snapshotBytes, err = s.confWriter.Snapshot()
		if err != nil {
			return fmt.Errorf("%w: snapshot mosquitto.conf: %v", ErrListenerRestartFailed, err)
		}
		snapshotTaken = true
		if writeErr := s.confWriter.Write(specs); writeErr != nil {
			if snapshotTaken {
				if restoreErr := s.confWriter.Restore(snapshotBytes); restoreErr != nil {
					return fmt.Errorf("%w: write mosquitto.conf: %v (restore also failed: %v)", ErrListenerRestartFailed, writeErr, restoreErr)
				}
			}
			return fmt.Errorf("%w: write mosquitto.conf: %v", ErrListenerRestartFailed, writeErr)
		}
	}

	target := ""
	if reader, ok := s.composeReader.(interface{ ComposeService() string }); ok {
		target = reader.ComposeService()
	}
	restartErr := s.restartRunner.Restart(ctx, target)
	if restartErr != nil {
		if snapshotTaken {
			if restoreErr := s.confWriter.Restore(snapshotBytes); restoreErr != nil {
				return fmt.Errorf("%w: %v (restore also failed: %v)", ErrListenerRestartFailed, restartErr, restoreErr)
			}
		}
		return fmt.Errorf("%w: %v", ErrListenerRestartFailed, restartErr)
	}

	if err := s.store.ReplaceAllListenerSpecs(ctx, entry.rows); err != nil {
		if snapshotTaken {
			if restoreErr := s.confWriter.Restore(snapshotBytes); restoreErr != nil {
				return fmt.Errorf("replace listener specs: %w (restore also failed: %w)", err, restoreErr)
			}
			recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), listenerRecoveryTimeout)
			recoveryErr := s.restartRunner.Restart(recoveryCtx, target)
			cancel()
			if recoveryErr != nil {
				return fmt.Errorf("replace listener specs: %w (compensating restart also failed: %w)", err, recoveryErr)
			}
		}
		return fmt.Errorf("replace listener specs: %w", err)
	}
	s.mu.Lock()
	entry = s.previews[revisionID]
	entry.consumed = true
	s.previews[revisionID] = entry
	s.mu.Unlock()
	if s.auditFn != nil {
		s.auditFn(ctx, "system", "listener.apply", "listener_config", revisionID, "applied", nil)
	}
	return nil
}

// rowsToSpecs converts persisted listener rows back into the in-memory
// specs used by the conf writer. The round-trip mirrors listenerRows in
// the preview path so the writer sees exactly the topology the operator
// approved.
func rowsToSpecs(rows []storage.ListenerSpecRow) ([]listeners.ListenerSpec, error) {
	specs := make([]listeners.ListenerSpec, 0, len(rows))
	for _, row := range rows {
		spec := listeners.ListenerSpec{
			ID:         row.ID,
			Port:       row.Port,
			Bind:       row.Bind,
			Options:    cloneOptions(row.Options),
			TLSProfile: row.TLSProfile,
		}
		for _, protocol := range row.Protocols {
			spec.Protocols = append(spec.Protocols, listeners.Protocol(protocol))
		}
		if err := listeners.Normalize(&spec); err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// resolveTLSMaterial injects the broker-side TLS directives of the bound
// certificate profile into the copy of the specs used for rendering.
//
// The persisted rows keep the profile name and never the resolved paths:
// the paths point at the active revision, so a rotation replaces the
// material behind them without changing the rendered configuration.
//
// A spec without a profile is returned unchanged: a listener may still
// carry TLS directives that the operator manages by hand.
func (s *ListenerService) resolveTLSMaterial(ctx context.Context, specs []listeners.ListenerSpec) ([]listeners.ListenerSpec, []listeners.Issue, error) {
	resolved := make([]listeners.ListenerSpec, 0, len(specs))
	var issues []listeners.Issue
	for _, spec := range specs {
		resolvedSpec, specIssues, err := s.resolveTLSMaterialFor(ctx, spec)
		if err != nil {
			return nil, issues, err
		}
		issues = append(issues, specIssues...)
		resolved = append(resolved, resolvedSpec)
	}
	return resolved, issues, nil
}

// resolveTLSMaterialFor resolves the certificate profile of one spec.
func (s *ListenerService) resolveTLSMaterialFor(ctx context.Context, spec listeners.ListenerSpec) (listeners.ListenerSpec, []listeners.Issue, error) {
	resolved := spec
	resolved.Options = cloneOptions(spec.Options)
	if resolved.Options == nil {
		resolved.Options = map[string]string{}
	}
	var issues []listeners.Issue
	profile := strings.TrimSpace(spec.TLSProfile)
	if profile == "" {
		return resolved, issues, nil
	}
	// The profile owns the TLS paths: a hand-written path would point at
	// material the rotation does not manage.
	for _, key := range []string{"cafile", "capath", "certfile", "keyfile"} {
		if _, exists := resolved.Options[key]; exists {
			issues = append(issues, listeners.Issue{
				Kind:       "tls_conflict",
				ListenerID: spec.ID,
				Directive:  key,
				Message:    fmt.Sprintf("the %q directive is managed by the certificate profile %q", key, profile),
			})
		}
	}
	if s.catalog == nil || !s.catalog.SupportsListenerDirective("certfile") {
		issues = append(issues, listeners.Issue{
			Kind:       "tls_unsupported",
			ListenerID: spec.ID,
			Directive:  "certfile",
			Message:    "the broker version does not accept TLS directives inside a listener block",
		})
		return resolved, issues, nil
	}
	if s.resolver == nil {
		issues = append(issues, listeners.Issue{
			Kind:       "tls_unavailable",
			ListenerID: spec.ID,
			Directive:  "tls_profile",
			Message:    "the certificate surface is not available",
		})
		return resolved, issues, nil
	}
	material, err := s.resolver.ResolveProfile(ctx, profile)
	if err != nil {
		issues = append(issues, listeners.Issue{
			Kind:       "tls_profile_unavailable",
			ListenerID: spec.ID,
			Directive:  "tls_profile",
			Message:    fmt.Sprintf("the certificate profile %q is not available: %v", profile, err),
		})
		return resolved, issues, nil
	}
	resolved.Options["certfile"] = material.CertPath
	resolved.Options["keyfile"] = material.KeyPath
	if material.HasCA && material.CAPath != "" {
		resolved.Options["cafile"] = material.CAPath
	}
	// Mutual TLS needs a certificate authority to verify the clients.
	if optionEnabled(resolved.Options, "require_certificate") && !material.HasCA {
		issues = append(issues, listeners.Issue{
			Kind:       "tls_ca_required",
			ListenerID: spec.ID,
			Directive:  "require_certificate",
			Message:    "mutual TLS requires a certificate profile with a certificate authority",
		})
	}
	return resolved, issues, nil
}

// optionEnabled reports whether an option is set to a true value.
func optionEnabled(options map[string]string, key string) bool {
	switch strings.ToLower(strings.TrimSpace(options[key])) {
	case "true", "yes", "on", "1":
		return true
	default:
		return false
	}
}

// ComposeAllHostPorts returns every configured Compose host port for UI collision hints.
// It is intentionally independent of the listener preview/apply safety path.
func (s *ListenerService) ComposeAllHostPorts(ctx context.Context) ([]int, error) {
	if s.composeReader == nil || s.composeReader.Disabled() {
		return nil, nil
	}
	ports, err := s.composeReader.HostPorts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read compose host ports: %w", err)
	}
	ports = append([]int(nil), ports...)
	sort.Ints(ports)
	return ports, nil
}

func (s *ListenerService) composeStatus(ctx context.Context, desired []listeners.ListenerSpec) (ComposePreviewStatus, error) {
	status := ComposePreviewStatus{MappedPorts: []int{}, UnmappedPorts: []int{}}
	if s.composeReader == nil {
		return status, nil
	}
	status.Disabled = s.composeReader.Disabled()
	if !s.composeReader.IsConfigured() {
		return status, nil
	}
	ports, err := s.composeReader.HostPorts(ctx)
	if err != nil {
		return status, fmt.Errorf("read compose host ports: %w", err)
	}
	status.MappedPorts = append(status.MappedPorts, ports...)
	mapped := make(map[int]struct{}, len(ports))
	for _, port := range ports {
		mapped[port] = struct{}{}
	}
	for _, spec := range desired {
		if _, ok := mapped[spec.Port]; !ok {
			status.UnmappedPorts = append(status.UnmappedPorts, spec.Port)
		}
	}
	sort.Ints(status.MappedPorts)
	sort.Ints(status.UnmappedPorts)
	status.UnmappedPorts = uniquePorts(status.UnmappedPorts)
	return status, nil
}

func (s *ListenerService) gcPreviewsLocked(now time.Time) {
	for id, preview := range s.previews {
		if now.After(preview.expires) {
			delete(s.previews, id)
		}
	}
}

func convertAndNormalize(rows []storage.ListenerSpecRow) ([]listeners.ListenerSpec, error) {
	specs := make([]listeners.ListenerSpec, 0, len(rows))
	for _, row := range rows {
		spec := listeners.ListenerSpec{ID: row.ID, Port: row.Port, Bind: row.Bind, Options: cloneOptions(row.Options), TLSProfile: row.TLSProfile}
		for _, protocol := range row.Protocols {
			spec.Protocols = append(spec.Protocols, listeners.Protocol(protocol))
		}
		if err := listeners.Normalize(&spec); err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func normalizeDesired(desired []listeners.ListenerSpec) ([]listeners.ListenerSpec, []listeners.Issue) {
	normalized := make([]listeners.ListenerSpec, 0, len(desired))
	var issues []listeners.Issue
	for _, desiredSpec := range desired {
		spec := desiredSpec
		spec.Protocols = append([]listeners.Protocol(nil), desiredSpec.Protocols...)
		spec.Options = cloneOptions(desiredSpec.Options)
		if err := listeners.Normalize(&spec); err != nil {
			issues = append(issues, listeners.Issue{Kind: "normalize", ListenerID: desiredSpec.ID, Message: err.Error()})
		}
		normalized = append(normalized, spec)
	}
	return normalized, issues
}

func listenerRows(specs []listeners.ListenerSpec) []storage.ListenerSpecRow {
	rows := make([]storage.ListenerSpecRow, 0, len(specs))
	for _, spec := range specs {
		row := storage.ListenerSpecRow{ID: spec.ID, Port: spec.Port, Bind: spec.Bind, Options: cloneOptions(spec.Options), TLSProfile: spec.TLSProfile}
		for _, protocol := range spec.Protocols {
			row.Protocols = append(row.Protocols, string(protocol))
		}
		rows = append(rows, row)
	}
	return rows
}

func cloneOptions(options map[string]string) map[string]string {
	if options == nil {
		return nil
	}
	cloned := make(map[string]string, len(options))
	for key, value := range options {
		cloned[key] = value
	}
	return cloned
}

func uniquePorts(ports []int) []int {
	if len(ports) < 2 {
		return ports
	}
	out := ports[:1]
	for _, port := range ports[1:] {
		if port != out[len(out)-1] {
			out = append(out, port)
		}
	}
	return out
}

func listenerUnifiedDiff(current, rendered string) (string, error) {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(current), B: difflib.SplitLines(rendered), FromFile: "current", ToFile: "rendered", Context: 3})
	if err != nil {
		return "", fmt.Errorf("generate listener diff: %w", err)
	}
	return diff, nil
}

func listenerHash(body string) string {
	hash := sha256.Sum256([]byte(body))
	return hex.EncodeToString(hash[:])
}

func newListenerRevisionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
