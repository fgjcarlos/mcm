// Package certificate manages the broker TLS certificate profiles.
//
// A profile groups the certificate material the broker serves under one
// name. Every upload creates an immutable revision; activating a revision
// switches a symlink, restarts the broker and verifies that the broker
// really serves the new material. When the verification fails the previous
// revision is restored.
//
// The private key is write-only material: it travels from the upload request
// to the certificate store on disk and never reaches the database, an API
// response, an audit event or a log line.
package certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto/certs"
	"github.com/fgjcarlos/mcm/internal/storage"
)

var (
	// ErrProfileNotFound is returned when a certificate profile does not exist.
	ErrProfileNotFound = errors.New("certificate profile not found")
	// ErrRevisionNotFound is returned when a certificate revision does not exist.
	ErrRevisionNotFound = errors.New("certificate revision not found")
	// ErrInvalidProfileName is returned when a profile name is not a safe path segment.
	ErrInvalidProfileName = errors.New("invalid certificate profile name")
	// ErrRevisionActive is returned when the requested revision is already active.
	ErrRevisionActive = errors.New("certificate revision is already active")
	// ErrInvalidRevision is returned when the stored material cannot be used.
	ErrInvalidRevision = errors.New("invalid certificate revision")
)

// Certificate lifecycle states mirrored from the storage layer.
const (
	// StatePending marks a stored revision that is not active yet.
	StatePending = storage.CertificateRevisionStatePending
	// StateActive marks the revision the broker serves.
	StateActive = storage.CertificateRevisionStateActive
	// StateSuperseded marks a revision replaced by a newer one.
	StateSuperseded = storage.CertificateRevisionStateSuperseded
	// StateFailed marks a revision whose activation was rolled back.
	StateFailed = storage.CertificateRevisionStateFailed
)

// Expiry statuses reported for a certificate.
const (
	// ExpiryValid marks a certificate that is not close to its expiry.
	ExpiryValid = "valid"
	// ExpiryExpiring marks a certificate that expires inside the warning window.
	ExpiryExpiring = "expiring"
	// ExpiryExpired marks a certificate that is already expired.
	ExpiryExpired = "expired"
	// ExpiryUnknown marks a certificate without a known validity window.
	ExpiryUnknown = "unknown"
)

// Audit actions recorded by the certificate service.
const (
	// AuditActionRevisionCreated records a stored revision.
	AuditActionRevisionCreated = "certificate.revision.created"
	// AuditActionRevisionApplied records a successful activation.
	AuditActionRevisionApplied = "certificate.revision.applied"
	// AuditActionRevisionFailed records an activation that was rolled back.
	AuditActionRevisionFailed = "certificate.revision.failed"
)

// CertificateRepository persists certificate profiles and revisions.
type CertificateRepository interface {
	UpsertCertificateProfile(ctx context.Context, name string, description string, now time.Time) (storage.CertificateProfile, error)
	GetCertificateProfileByName(ctx context.Context, name string) (storage.CertificateProfile, error)
	ListCertificateProfiles(ctx context.Context) ([]storage.CertificateProfile, error)
	InsertCertificateRevision(ctx context.Context, params storage.CreateCertificateRevisionParams) (storage.CertificateRevision, error)
	GetCertificateRevision(ctx context.Context, profileID int64, revisionID string) (storage.CertificateRevision, error)
	ListCertificateRevisions(ctx context.Context, profileID int64) ([]storage.CertificateRevision, error)
	UpdateCertificateRevisionState(ctx context.Context, id int64, state string, failureReason string, activatedAt time.Time) error
}

// BrokerRestarter restarts the broker so it serves the new material.
type BrokerRestarter interface {
	Restart(ctx context.Context) error
}

// ProfileBindingReader lists the broker listeners bound to a certificate profile.
type ProfileBindingReader interface {
	ListListenerSpecs(ctx context.Context) ([]storage.ListenerSpecRow, error)
}

// CertificateAuditFunc records a certificate lifecycle audit event.
//
// The metadata never contains private key material.
type CertificateAuditFunc func(ctx context.Context, actor, action, resourceType, resourceID, result string, metadata []byte)

// Service manages the certificate profiles of the broker.
type Service struct {
	store          *certs.Store
	repo           CertificateRepository
	restart        BrokerRestarter
	bindings       ProfileBindingReader
	audit          CertificateAuditFunc
	clock          func() time.Time
	newRevisionID  func() (string, error)
	brokerHost     string
	brokerCertsDir string
	expiryWarning  time.Duration
	verifyTimeout  time.Duration
}

// ServiceOption configures a Service.
type ServiceOption func(*Service)

// WithCertificateClock sets the service clock, primarily for deterministic tests.
func WithCertificateClock(clock func() time.Time) ServiceOption {
	return func(s *Service) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithCertificateRevisionID sets the revision identifier generator.
func WithCertificateRevisionID(newRevisionID func() (string, error)) ServiceOption {
	return func(s *Service) {
		if newRevisionID != nil {
			s.newRevisionID = newRevisionID
		}
	}
}

// WithCertificateAudit records the certificate lifecycle audit events.
func WithCertificateAudit(audit CertificateAuditFunc) ServiceOption {
	return func(s *Service) {
		s.audit = audit
	}
}

// WithCertificateRestarter injects the broker restarter used after an activation.
func WithCertificateRestarter(restarter BrokerRestarter) ServiceOption {
	return func(s *Service) {
		s.restart = restarter
	}
}

// WithCertificateBindings injects the reader that lists the listeners bound to a profile.
func WithCertificateBindings(bindings ProfileBindingReader) ServiceOption {
	return func(s *Service) {
		s.bindings = bindings
	}
}

// WithBrokerHost sets the host MCM uses to reach the broker listeners.
func WithBrokerHost(host string) ServiceOption {
	return func(s *Service) {
		s.brokerHost = strings.TrimSpace(host)
	}
}

// WithBrokerCertsDir sets the broker-side directory that holds the certificate material.
func WithBrokerCertsDir(dir string) ServiceOption {
	return func(s *Service) {
		s.brokerCertsDir = strings.TrimSpace(dir)
	}
}

// WithExpiryWarningWindow sets the horizon used to flag expiring certificates.
func WithExpiryWarningWindow(window time.Duration) ServiceOption {
	return func(s *Service) {
		if window > 0 {
			s.expiryWarning = window
		}
	}
}

// WithVerifyTimeout sets the timeout of one TLS verification handshake.
func WithVerifyTimeout(timeout time.Duration) ServiceOption {
	return func(s *Service) {
		if timeout > 0 {
			s.verifyTimeout = timeout
		}
	}
}

// NewService returns a certificate service.
//
// The store holds the material on disk, the repository persists the metadata
// and the restarter reloads the broker.
func NewService(store *certs.Store, repo CertificateRepository, options ...ServiceOption) *Service {
	service := &Service{
		store:         store,
		repo:          repo,
		clock:         time.Now,
		newRevisionID: defaultRevisionID,
		expiryWarning: 30 * 24 * time.Hour,
		verifyTimeout: 5 * time.Second,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// RevisionView is the API projection of a certificate revision.
//
// It never contains private key material.
type RevisionView struct {
	RevisionID        string        `json:"revision_id"`
	State             string        `json:"state"`
	Actor             string        `json:"actor"`
	MaterialHash      string        `json:"material_hash"`
	Subject           string        `json:"subject"`
	Issuer            string        `json:"issuer"`
	DNSNames          []string      `json:"dns_names"`
	IPAddresses       []string      `json:"ip_addresses"`
	NotBefore         time.Time     `json:"not_before"`
	NotAfter          time.Time     `json:"not_after"`
	FingerprintSHA256 string        `json:"fingerprint_sha256"`
	KeyAlgorithm      string        `json:"key_algorithm"`
	IsCA              bool          `json:"is_ca"`
	HasCA             bool          `json:"has_ca"`
	HasChain          bool          `json:"has_chain"`
	SerialNumber      string        `json:"serial_number"`
	ExpiryStatus      string        `json:"expiry_status"`
	FailureReason     string        `json:"failure_reason"`
	CreatedAt         time.Time     `json:"created_at"`
	ActivatedAt       time.Time     `json:"activated_at"`
	Issues            []certs.Issue `json:"issues"`
	Warnings          []certs.Issue `json:"warnings"`
}

// ProfileView is the API projection of a certificate profile.
type ProfileView struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	ActiveRevision string         `json:"active_revision"`
	ExpiryStatus   string         `json:"expiry_status"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Revisions      []RevisionView `json:"revisions"`
}

// FilesView lists the broker-side paths of one material set.
//
// The paths are stable across rotations because the broker always reads the
// active symlink.
type FilesView struct {
	CAPath    string `json:"ca_path"`
	CertPath  string `json:"cert_path"`
	KeyPath   string `json:"key_path"`
	ChainPath string `json:"chain_path"`
}

// PreviewView describes what an activation would change.
type PreviewView struct {
	Profile           string        `json:"profile"`
	RevisionID        string        `json:"revision_id"`
	CurrentRevision   string        `json:"current_revision"`
	Subject           string        `json:"subject"`
	Issuer            string        `json:"issuer"`
	DNSNames          []string      `json:"dns_names"`
	NotBefore         time.Time     `json:"not_before"`
	NotAfter          time.Time     `json:"not_after"`
	FingerprintSHA256 string        `json:"fingerprint_sha256"`
	ExpiryStatus      string        `json:"expiry_status"`
	Files             FilesView     `json:"files"`
	RestartRequired   bool          `json:"restart_required"`
	Warnings          []certs.Issue `json:"warnings"`
}

// ApplyResult reports the outcome of an activation.
type ApplyResult struct {
	Profile          string            `json:"profile"`
	RevisionID       string            `json:"revision_id"`
	PreviousRevision string            `json:"previous_revision"`
	Restarted        bool              `json:"restarted"`
	Recovered        bool              `json:"recovered"`
	RecoveryNote     string            `json:"recovery_note"`
	Verification     []TLSVerification `json:"verification"`
}

// ValidationError reports the blocking problems of a rejected upload.
type ValidationError struct {
	Issues   []certs.Issue
	Warnings []certs.Issue
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "the certificate material was rejected"
	}
	messages := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		messages = append(messages, issue.Message)
	}
	return "the certificate material was rejected: " + strings.Join(messages, "; ")
}

// CreateRevision validates and stores a new revision of a profile.
//
// The material is stored on disk and recorded in the database as a pending
// revision. It is not served until Apply activates it.
func (s *Service) CreateRevision(ctx context.Context, actor string, profileName string, description string, input certs.Input, requireCA bool) (RevisionView, error) {
	if err := validateProfileName(profileName); err != nil {
		return RevisionView{}, err
	}
	now := s.clock().UTC()
	result := certs.Validate(input, now, certs.Options{
		ExpiryWarningWindow: s.expiryWarning,
		RequireCA:           requireCA,
	})
	if len(result.Issues) > 0 {
		return RevisionView{}, &ValidationError{Issues: result.Issues, Warnings: result.Warnings}
	}

	revisionID, err := s.newRevisionID()
	if err != nil {
		return RevisionView{}, fmt.Errorf("create revision id: %w", err)
	}
	if _, err := s.store.SaveRevision(profileName, revisionID, input); err != nil {
		return RevisionView{}, err
	}

	profile, err := s.repo.UpsertCertificateProfile(ctx, profileName, description, now)
	if err != nil {
		s.discardRevision(profileName, revisionID)
		return RevisionView{}, fmt.Errorf("store certificate profile: %w", err)
	}
	revision, err := s.repo.InsertCertificateRevision(ctx, storage.CreateCertificateRevisionParams{
		ProfileID:         profile.ID,
		RevisionID:        revisionID,
		Actor:             actor,
		State:             StatePending,
		MaterialHash:      result.Metadata.MaterialHash,
		Subject:           result.Metadata.Subject,
		Issuer:            result.Metadata.Issuer,
		DNSNames:          result.Metadata.DNSNames,
		IPAddresses:       result.Metadata.IPAddresses,
		NotBefore:         result.Metadata.NotBefore,
		NotAfter:          result.Metadata.NotAfter,
		FingerprintSHA256: result.Metadata.FingerprintSHA256,
		KeyAlgorithm:      result.Metadata.KeyAlgorithm,
		IsCA:              result.Metadata.IsCA,
		HasCA:             result.Metadata.HasCA,
		HasChain:          result.Metadata.HasChain,
		SerialNumber:      result.Metadata.SerialNumber,
		CreatedAt:         now,
	})
	if err != nil {
		s.discardRevision(profileName, revisionID)
		return RevisionView{}, fmt.Errorf("store certificate revision: %w", err)
	}

	s.auditEvent(ctx, actor, AuditActionRevisionCreated, "certificate_revision", revisionID, "success", map[string]any{
		"profile":       profileName,
		"revision_id":   revisionID,
		"material_hash": result.Metadata.MaterialHash,
		"expiry_status": s.expiryStatus(result.Metadata.NotAfter, now),
	})

	view := s.viewFor(revision, result.Warnings)
	return view, nil
}

// ListProfiles returns every profile with its revisions.
//
// The active revision is read from the certificate store, so the reported
// state always matches the material the broker reads.
func (s *Service) ListProfiles(ctx context.Context) ([]ProfileView, error) {
	profiles, err := s.repo.ListCertificateProfiles(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]ProfileView, 0, len(profiles))
	for _, profile := range profiles {
		revisions, err := s.repo.ListCertificateRevisions(ctx, profile.ID)
		if err != nil {
			return nil, err
		}
		view := ProfileView{
			Name:        profile.Name,
			Description: profile.Description,
			CreatedAt:   profile.CreatedAt,
			UpdatedAt:   profile.UpdatedAt,
			Revisions:   make([]RevisionView, 0, len(revisions)),
		}
		if active, err := s.store.ActiveRevision(profile.Name); err == nil {
			view.ActiveRevision = active
		}
		for _, revision := range revisions {
			view.Revisions = append(view.Revisions, s.viewFor(revision, nil))
		}
		view.ExpiryStatus = s.profileExpiryStatus(view)
		views = append(views, view)
	}
	return views, nil
}

// Preview describes the activation of a revision without changing anything.
func (s *Service) Preview(ctx context.Context, actor string, profileName string, revisionID string) (PreviewView, error) {
	profile, revision, err := s.loadRevision(ctx, profileName, revisionID)
	if err != nil {
		return PreviewView{}, err
	}
	current, _ := s.store.ActiveRevision(profile.Name)

	preview := PreviewView{
		Profile:           profile.Name,
		RevisionID:        revision.RevisionID,
		CurrentRevision:   current,
		Subject:           revision.Subject,
		Issuer:            revision.Issuer,
		DNSNames:          revision.DNSNames,
		NotBefore:         revision.NotBefore,
		NotAfter:          revision.NotAfter,
		FingerprintSHA256: revision.FingerprintSHA256,
		ExpiryStatus:      s.expiryStatus(revision.NotAfter, s.clock().UTC()),
		Files:             s.brokerFiles(profile.Name),
		RestartRequired:   true,
	}
	return preview, nil
}

// Apply activates a revision, restarts the broker and verifies the result.
//
// When the restart or the verification fails the previous material is
// restored, the broker is restarted again and the revision is marked failed.
func (s *Service) Apply(ctx context.Context, actor string, profileName string, revisionID string) (ApplyResult, error) {
	profile, revision, err := s.loadRevision(ctx, profileName, revisionID)
	if err != nil {
		return ApplyResult{}, err
	}
	current, _ := s.store.ActiveRevision(profile.Name)
	if current == revision.RevisionID {
		return ApplyResult{}, ErrRevisionActive
	}

	result := ApplyResult{
		Profile:          profile.Name,
		RevisionID:       revision.RevisionID,
		PreviousRevision: current,
	}
	if err := s.store.Activate(profile.Name, revision.RevisionID); err != nil {
		return result, err
	}

	if err := s.restartBroker(ctx); err != nil {
		return s.rollback(ctx, actor, profile.Name, revision, current, result, fmt.Sprintf("the broker restart failed: %v", err))
	}
	result.Restarted = true

	verifications := s.verifyProfile(ctx, profile.Name)
	result.Verification = verifications
	for _, verification := range verifications {
		if verification.Status == TLSStatusFailed {
			reason := fmt.Sprintf("the broker did not serve the new certificate on port %d: %s", verification.Port, verification.Detail)
			return s.rollback(ctx, actor, profile.Name, revision, current, result, reason)
		}
	}

	now := s.clock().UTC()
	if err := s.repo.UpdateCertificateRevisionState(ctx, revision.ID, StateActive, "", now); err != nil {
		return result, fmt.Errorf("mark certificate revision active: %w", err)
	}
	if current != "" {
		if previous, err := s.repo.GetCertificateRevision(ctx, profile.ID, current); err == nil {
			if err := s.repo.UpdateCertificateRevisionState(ctx, previous.ID, StateSuperseded, "", time.Time{}); err != nil {
				return result, fmt.Errorf("mark previous certificate revision superseded: %w", err)
			}
		}
	}

	s.auditEvent(ctx, actor, AuditActionRevisionApplied, "certificate_revision", revision.RevisionID, "success", map[string]any{
		"profile":      profile.Name,
		"revision_id":  revision.RevisionID,
		"previous":     current,
		"verification": verificationSummary(verifications),
	})
	return result, nil
}

// ResolvedProfile describes the broker-side paths of the active material of
// a profile.
//
// The listener service uses it to render the TLS directives of a listener.
type ResolvedProfile struct {
	Profile   string
	CAPath    string
	CertPath  string
	KeyPath   string
	ChainPath string
	HasCA     bool
}

// ResolveProfile returns the active material of a profile.
//
// It fails when the profile has no active revision: a listener must never
// reference material the broker cannot read.
func (s *Service) ResolveProfile(ctx context.Context, profileName string) (ResolvedProfile, error) {
	if err := validateProfileName(profileName); err != nil {
		return ResolvedProfile{}, err
	}
	profile, err := s.repo.GetCertificateProfileByName(ctx, profileName)
	if err != nil {
		if errors.Is(err, storage.ErrCertificateProfileNotFound) {
			return ResolvedProfile{}, ErrProfileNotFound
		}
		return ResolvedProfile{}, err
	}
	revisionID, err := s.store.ActiveRevision(profileName)
	if err != nil {
		return ResolvedProfile{}, ErrProfileNotFound
	}
	revision, err := s.repo.GetCertificateRevision(ctx, profile.ID, revisionID)
	if err != nil {
		if errors.Is(err, storage.ErrCertificateRevisionNotFound) {
			return ResolvedProfile{}, ErrProfileNotFound
		}
		return ResolvedProfile{}, err
	}
	files := s.brokerFiles(profileName)
	return ResolvedProfile{
		Profile:   profileName,
		CAPath:    files.CAPath,
		CertPath:  files.CertPath,
		KeyPath:   files.KeyPath,
		ChainPath: files.ChainPath,
		HasCA:     revision.HasCA,
	}, nil
}

// loadRevision returns a profile and one of its revisions.
func (s *Service) loadRevision(ctx context.Context, profileName string, revisionID string) (storage.CertificateProfile, storage.CertificateRevision, error) {
	if err := validateProfileName(profileName); err != nil {
		return storage.CertificateProfile{}, storage.CertificateRevision{}, err
	}
	profile, err := s.repo.GetCertificateProfileByName(ctx, profileName)
	if err != nil {
		if errors.Is(err, storage.ErrCertificateProfileNotFound) {
			return storage.CertificateProfile{}, storage.CertificateRevision{}, ErrProfileNotFound
		}
		return storage.CertificateProfile{}, storage.CertificateRevision{}, err
	}
	revision, err := s.repo.GetCertificateRevision(ctx, profile.ID, revisionID)
	if err != nil {
		if errors.Is(err, storage.ErrCertificateRevisionNotFound) {
			return storage.CertificateProfile{}, storage.CertificateRevision{}, ErrRevisionNotFound
		}
		return storage.CertificateProfile{}, storage.CertificateRevision{}, err
	}
	return profile, revision, nil
}

// rollback restores the previous material after a failed activation.
func (s *Service) rollback(ctx context.Context, actor string, profileName string, revision storage.CertificateRevision, previous string, result ApplyResult, reason string) (ApplyResult, error) {
	result.Recovered = true
	result.RecoveryNote = reason

	if previous != "" {
		if err := s.store.Activate(profileName, previous); err != nil {
			result.RecoveryNote = fmt.Sprintf("%s; the previous material could not be restored: %v", reason, err)
		}
	} else if err := s.store.Deactivate(profileName); err != nil {
		result.RecoveryNote = fmt.Sprintf("%s; the active material could not be cleared: %v", reason, err)
	}
	if err := s.restartBroker(ctx); err != nil {
		result.RecoveryNote = fmt.Sprintf("%s; the broker restart after the recovery failed: %v", result.RecoveryNote, err)
	}
	if err := s.repo.UpdateCertificateRevisionState(ctx, revision.ID, StateFailed, reason, time.Time{}); err != nil {
		result.RecoveryNote = fmt.Sprintf("%s; the failed revision could not be recorded: %v", result.RecoveryNote, err)
	}

	s.auditEvent(ctx, actor, AuditActionRevisionFailed, "certificate_revision", revision.RevisionID, "failure", map[string]any{
		"profile":     profileName,
		"revision_id": revision.RevisionID,
		"reason":      reason,
		"recovered":   result.Recovered,
	})
	return result, fmt.Errorf("%s", reason)
}

// restartBroker restarts the broker when a restarter is configured.
func (s *Service) restartBroker(ctx context.Context) error {
	if s.restart == nil {
		return nil
	}
	return s.restart.Restart(ctx)
}

// verifyProfile verifies every listener bound to the profile.
func (s *Service) verifyProfile(ctx context.Context, profileName string) []TLSVerification {
	if s.bindings == nil {
		return nil
	}
	specs, err := s.bindings.ListListenerSpecs(ctx)
	if err != nil {
		return []TLSVerification{{
			Status: TLSStatusUnverified,
			Reason: "the listeners bound to the profile could not be read",
			Detail: err.Error(),
		}}
	}
	verifications := make([]TLSVerification, 0, len(specs))
	for _, spec := range specs {
		if spec.TLSProfile != profileName {
			continue
		}
		verifications = append(verifications, s.verifyListener(ctx, profileName, spec))
	}
	return verifications
}

// discardRevision removes material that could not be recorded.
func (s *Service) discardRevision(profileName string, revisionID string) {
	_ = s.store.RemoveRevision(profileName, revisionID)
}

// viewFor projects a stored revision into its API view.
func (s *Service) viewFor(revision storage.CertificateRevision, warnings []certs.Issue) RevisionView {
	now := s.clock().UTC()
	return RevisionView{
		RevisionID:        revision.RevisionID,
		State:             revision.State,
		Actor:             revision.Actor,
		MaterialHash:      revision.MaterialHash,
		Subject:           revision.Subject,
		Issuer:            revision.Issuer,
		DNSNames:          revision.DNSNames,
		IPAddresses:       revision.IPAddresses,
		NotBefore:         revision.NotBefore,
		NotAfter:          revision.NotAfter,
		FingerprintSHA256: revision.FingerprintSHA256,
		KeyAlgorithm:      revision.KeyAlgorithm,
		IsCA:              revision.IsCA,
		HasCA:             revision.HasCA,
		HasChain:          revision.HasChain,
		SerialNumber:      revision.SerialNumber,
		ExpiryStatus:      s.expiryStatus(revision.NotAfter, now),
		FailureReason:     revision.FailureReason,
		CreatedAt:         revision.CreatedAt,
		ActivatedAt:       revision.ActivatedAt,
		Issues:            []certs.Issue{},
		Warnings:          warnings,
	}
}

// profileExpiryStatus reports the expiry status of the active revision, or of
// the newest revision when no revision is active.
func (s *Service) profileExpiryStatus(profile ProfileView) string {
	for _, revision := range profile.Revisions {
		if revision.RevisionID == profile.ActiveRevision {
			return revision.ExpiryStatus
		}
	}
	if len(profile.Revisions) > 0 {
		return profile.Revisions[0].ExpiryStatus
	}
	return ExpiryUnknown
}

// expiryStatus classifies a certificate against the warning window.
func (s *Service) expiryStatus(notAfter time.Time, now time.Time) string {
	if notAfter.IsZero() {
		return ExpiryUnknown
	}
	if !now.Before(notAfter) {
		return ExpiryExpired
	}
	if notAfter.Sub(now) <= s.expiryWarning {
		return ExpiryExpiring
	}
	return ExpiryValid
}

// brokerFiles returns the broker-side paths of a profile.
func (s *Service) brokerFiles(profileName string) FilesView {
	base := s.brokerCertsDir
	if base == "" {
		return FilesView{}
	}
	active := fmt.Sprintf("%s/%s/active", strings.TrimSuffix(base, "/"), profileName)
	return FilesView{
		CAPath:    fmt.Sprintf("%s/%s", active, certs.CAFileName),
		CertPath:  fmt.Sprintf("%s/%s", active, certs.ServerCertFileName),
		KeyPath:   fmt.Sprintf("%s/%s", active, certs.ServerKeyFileName),
		ChainPath: fmt.Sprintf("%s/%s", active, certs.ChainFileName),
	}
}

// auditEvent records a certificate lifecycle event.
func (s *Service) auditEvent(ctx context.Context, actor string, action string, resourceType string, resourceID string, result string, metadata map[string]any) {
	if s.audit == nil {
		return
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		payload = []byte(`{}`)
	}
	s.audit(ctx, actor, action, resourceType, resourceID, result, payload)
}

// validateProfileName rejects names that cannot become a path segment.
func validateProfileName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: the name is required", ErrInvalidProfileName)
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("%w: %q", ErrInvalidProfileName, name)
	}
	return nil
}

// verificationSummary renders the verification results for the audit event.
func verificationSummary(verifications []TLSVerification) []string {
	summary := make([]string, 0, len(verifications))
	for _, verification := range verifications {
		summary = append(summary, fmt.Sprintf("%d:%s", verification.Port, verification.Status))
	}
	return summary
}

// defaultRevisionID generates a revision identifier.
func defaultRevisionID() (string, error) {
	return time.Now().UTC().Format("20060102T150405Z") + "-" + randomSuffix(), nil
}
