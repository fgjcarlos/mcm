package listener

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const (
	// ListenerPreviewTTL is the lifetime of an unapplied listener preview.
	ListenerPreviewTTL = time.Hour
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

// composeAdapter narrows deploy.ComposePortsReader to the listener ComposeReader contract.
type composeAdapter struct {
	reader  deploy.ComposePortsReader
	service string
}

func (a composeAdapter) IsConfigured() bool { return a.reader.IsConfigured() }
func (a composeAdapter) Disabled() bool     { return a.reader.Disabled() }
func (a composeAdapter) HostPorts(ctx context.Context) ([]int, error) {
	return a.reader.HostPorts(ctx)
}
func (a composeAdapter) ComposeService() string { return a.service }

// ListenerAuditFunc records a listener lifecycle audit event.
type ListenerAuditFunc func(ctx context.Context, actor, action, resourceType, resourceID, result string, metadata []byte)

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
	auditFn       ListenerAuditFunc
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
	validationIssues, warnings := listeners.Validate(normalized)
	issues = append(issues, validationIssues...)
	if len(issues) > 0 {
		sort.Slice(issues, func(i, j int) bool { return issues[i].Message < issues[j].Message })
		return ListenerPreviewResult{Issues: issues, Warnings: warnings}, fmt.Errorf("validate listener specs: %w", errors.New(issues[0].Message))
	}
	desiredRendered, err := listeners.RenderAll(normalized)
	if err != nil {
		return ListenerPreviewResult{}, fmt.Errorf("render listener specs: %w", err)
	}
	currentRendered := ""
	if len(current) > 0 {
		currentRendered, err = listeners.RenderAll(current)
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

	target := ""
	if reader, ok := s.composeReader.(interface{ ComposeService() string }); ok {
		target = reader.ComposeService()
	}
	if err := s.restartRunner.Restart(ctx, target); err != nil {
		return fmt.Errorf("%w: %v", ErrListenerRestartFailed, err)
	}
	if err := s.store.ReplaceAllListenerSpecs(ctx, entry.rows); err != nil {
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
		spec := listeners.ListenerSpec{ID: row.ID, Port: row.Port, Bind: row.Bind, Options: cloneOptions(row.Options)}
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
		row := storage.ListenerSpecRow{ID: spec.ID, Port: spec.Port, Bind: spec.Bind, Options: cloneOptions(spec.Options)}
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
