package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrCertificateProfileNotFound is returned when a certificate profile does not exist.
var ErrCertificateProfileNotFound = errors.New("certificate profile not found")

// ErrCertificateRevisionNotFound is returned when a certificate revision does not exist.
var ErrCertificateRevisionNotFound = errors.New("certificate revision not found")

// Certificate profile lifecycle states.
const (
	// CertificateRevisionStatePending marks a stored revision that is not active yet.
	CertificateRevisionStatePending = "pending"
	// CertificateRevisionStateActive marks the revision the broker serves.
	CertificateRevisionStateActive = "active"
	// CertificateRevisionStateSuperseded marks a revision replaced by a newer one.
	CertificateRevisionStateSuperseded = "superseded"
	// CertificateRevisionStateFailed marks a revision whose activation was rolled back.
	CertificateRevisionStateFailed = "failed"
)

// CertificateProfile is the stored certificate profile model.
//
// A profile groups the certificate material that the broker serves under one
// name; every upload creates a new immutable revision of the profile.
type CertificateProfile struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CertificateRevision is the stored certificate revision model.
//
// The row holds public metadata only: the private key never reaches the
// database, it lives in the certificate store on disk.
type CertificateRevision struct {
	ID                int64     `json:"id"`
	ProfileID         int64     `json:"profile_id"`
	RevisionID        string    `json:"revision_id"`
	Actor             string    `json:"actor"`
	State             string    `json:"state"`
	MaterialHash      string    `json:"material_hash"`
	Subject           string    `json:"subject"`
	Issuer            string    `json:"issuer"`
	DNSNames          []string  `json:"dns_names"`
	IPAddresses       []string  `json:"ip_addresses"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	KeyAlgorithm      string    `json:"key_algorithm"`
	IsCA              bool      `json:"is_ca"`
	HasCA             bool      `json:"has_ca"`
	HasChain          bool      `json:"has_chain"`
	SerialNumber      string    `json:"serial_number"`
	FailureReason     string    `json:"failure_reason"`
	CreatedAt         time.Time `json:"created_at"`
	ActivatedAt       time.Time `json:"activated_at"`
}

// CreateCertificateRevisionParams holds the fields of a new certificate revision.
type CreateCertificateRevisionParams struct {
	ProfileID         int64
	RevisionID        string
	Actor             string
	State             string
	MaterialHash      string
	Subject           string
	Issuer            string
	DNSNames          []string
	IPAddresses       []string
	NotBefore         time.Time
	NotAfter          time.Time
	FingerprintSHA256 string
	KeyAlgorithm      string
	IsCA              bool
	HasCA             bool
	HasChain          bool
	SerialNumber      string
	CreatedAt         time.Time
}

// UpsertCertificateProfile creates the profile or updates its description.
func (s *Store) UpsertCertificateProfile(ctx context.Context, name string, description string, now time.Time) (CertificateProfile, error) {
	s.LockMutations()
	defer s.UnlockMutations()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	existing, err := scanCertificateProfile(s.db.QueryRowContext(ctx, certificateProfileSelect+` WHERE name = ?`, name))
	switch {
	case err == nil:
		if _, err := s.db.ExecContext(ctx,
			`UPDATE certificate_profiles SET description = ?, updated_at = ? WHERE id = ?`,
			description, now.UTC().Format(time.RFC3339Nano), existing.ID,
		); err != nil {
			return CertificateProfile{}, fmt.Errorf("update certificate profile: %w", err)
		}
		existing.Description = description
		existing.UpdatedAt = now
		return existing, nil
	case errors.Is(err, sql.ErrNoRows):
	default:
		return CertificateProfile{}, fmt.Errorf("get certificate profile: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
INSERT INTO certificate_profiles(name, description, created_at, updated_at)
VALUES(?, ?, ?, ?)`,
		name, description, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return CertificateProfile{}, fmt.Errorf("insert certificate profile: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CertificateProfile{}, fmt.Errorf("get certificate profile id: %w", err)
	}
	return CertificateProfile{
		ID:          id,
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// GetCertificateProfileByName returns the certificate profile with name.
func (s *Store) GetCertificateProfileByName(ctx context.Context, name string) (CertificateProfile, error) {
	profile, err := scanCertificateProfile(s.db.QueryRowContext(ctx, certificateProfileSelect+` WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return CertificateProfile{}, ErrCertificateProfileNotFound
	}
	if err != nil {
		return CertificateProfile{}, fmt.Errorf("get certificate profile: %w", err)
	}
	return profile, nil
}

// ListCertificateProfiles returns every certificate profile ordered by name.
func (s *Store) ListCertificateProfiles(ctx context.Context) ([]CertificateProfile, error) {
	rows, err := s.db.QueryContext(ctx, certificateProfileSelect+` ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("list certificate profiles: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var profiles []CertificateProfile
	for rows.Next() {
		profile, err := scanCertificateProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("scan certificate profile: %w", err)
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate certificate profiles: %w", err)
	}
	return profiles, nil
}

// InsertCertificateRevision persists a new certificate revision.
func (s *Store) InsertCertificateRevision(ctx context.Context, params CreateCertificateRevisionParams) (CertificateRevision, error) {
	s.LockMutations()
	defer s.UnlockMutations()

	now := time.Now().UTC()
	if params.CreatedAt.IsZero() {
		params.CreatedAt = now
	}
	if params.State == "" {
		params.State = CertificateRevisionStatePending
	}
	dnsNames, err := marshalStringList(params.DNSNames)
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("marshal certificate dns names: %w", err)
	}
	ipAddresses, err := marshalStringList(params.IPAddresses)
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("marshal certificate ip addresses: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
INSERT INTO certificate_revisions(
	profile_id, revision_id, actor, state, material_hash, subject, issuer,
	dns_names, ip_addresses, not_before, not_after, fingerprint_sha256,
	key_algorithm, is_ca, has_ca, has_chain, serial_number, failure_reason,
	created_at, activated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, '')`,
		params.ProfileID, params.RevisionID, params.Actor, params.State, params.MaterialHash,
		params.Subject, params.Issuer, dnsNames, ipAddresses,
		formatOptionalTime(params.NotBefore), formatOptionalTime(params.NotAfter),
		params.FingerprintSHA256, params.KeyAlgorithm, params.IsCA, params.HasCA,
		params.HasChain, params.SerialNumber, params.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("insert certificate revision: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("get certificate revision id: %w", err)
	}
	revision := CertificateRevision{
		ID:                id,
		ProfileID:         params.ProfileID,
		RevisionID:        params.RevisionID,
		Actor:             params.Actor,
		State:             params.State,
		MaterialHash:      params.MaterialHash,
		Subject:           params.Subject,
		Issuer:            params.Issuer,
		DNSNames:          params.DNSNames,
		IPAddresses:       params.IPAddresses,
		NotBefore:         params.NotBefore,
		NotAfter:          params.NotAfter,
		FingerprintSHA256: params.FingerprintSHA256,
		KeyAlgorithm:      params.KeyAlgorithm,
		IsCA:              params.IsCA,
		HasCA:             params.HasCA,
		HasChain:          params.HasChain,
		SerialNumber:      params.SerialNumber,
		CreatedAt:         params.CreatedAt,
	}
	return revision, nil
}

// GetCertificateRevision returns one revision of a profile.
func (s *Store) GetCertificateRevision(ctx context.Context, profileID int64, revisionID string) (CertificateRevision, error) {
	revision, err := scanCertificateRevision(s.db.QueryRowContext(ctx,
		certificateRevisionSelect+` WHERE profile_id = ? AND revision_id = ?`, profileID, revisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return CertificateRevision{}, ErrCertificateRevisionNotFound
	}
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("get certificate revision: %w", err)
	}
	return revision, nil
}

// ListCertificateRevisions returns the revisions of a profile, newest first.
func (s *Store) ListCertificateRevisions(ctx context.Context, profileID int64) ([]CertificateRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		certificateRevisionSelect+` WHERE profile_id = ? ORDER BY created_at DESC, id DESC`, profileID)
	if err != nil {
		return nil, fmt.Errorf("list certificate revisions: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var revisions []CertificateRevision
	for rows.Next() {
		revision, err := scanCertificateRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("scan certificate revision: %w", err)
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate certificate revisions: %w", err)
	}
	return revisions, nil
}

// UpdateCertificateRevisionState moves a revision to another lifecycle state.
//
// The activation timestamp is only written when activatedAt is not zero, so a
// failed activation keeps the recorded activation of the revision it replaced.
func (s *Store) UpdateCertificateRevisionState(ctx context.Context, id int64, state string, failureReason string, activatedAt time.Time) error {
	s.LockMutations()
	defer s.UnlockMutations()

	if activatedAt.IsZero() {
		_, err := s.db.ExecContext(ctx,
			`UPDATE certificate_revisions SET state = ?, failure_reason = ? WHERE id = ?`,
			state, failureReason, id)
		if err != nil {
			return fmt.Errorf("update certificate revision state: %w", err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE certificate_revisions SET state = ?, failure_reason = ?, activated_at = ? WHERE id = ?`,
		state, failureReason, activatedAt.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("update certificate revision state: %w", err)
	}
	return nil
}

const certificateProfileSelect = `SELECT id, name, description, created_at, updated_at FROM certificate_profiles`

const certificateRevisionSelect = `SELECT id, profile_id, revision_id, actor, state, material_hash,
	subject, issuer, dns_names, ip_addresses, not_before, not_after, fingerprint_sha256,
	key_algorithm, is_ca, has_ca, has_chain, serial_number, failure_reason, created_at, activated_at
FROM certificate_revisions`

func scanCertificateProfile(row interface{ Scan(...any) error }) (CertificateProfile, error) {
	var profile CertificateProfile
	var createdAt, updatedAt string
	if err := row.Scan(&profile.ID, &profile.Name, &profile.Description, &createdAt, &updatedAt); err != nil {
		return CertificateProfile{}, err
	}
	var err error
	profile.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return CertificateProfile{}, fmt.Errorf("parse certificate profile created_at: %w", err)
	}
	profile.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return CertificateProfile{}, fmt.Errorf("parse certificate profile updated_at: %w", err)
	}
	return profile, nil
}

func scanCertificateRevision(row interface{ Scan(...any) error }) (CertificateRevision, error) {
	var revision CertificateRevision
	var dnsNames, ipAddresses, notBefore, notAfter, createdAt, activatedAt string
	if err := row.Scan(
		&revision.ID, &revision.ProfileID, &revision.RevisionID, &revision.Actor, &revision.State,
		&revision.MaterialHash, &revision.Subject, &revision.Issuer, &dnsNames, &ipAddresses,
		&notBefore, &notAfter, &revision.FingerprintSHA256, &revision.KeyAlgorithm,
		&revision.IsCA, &revision.HasCA, &revision.HasChain, &revision.SerialNumber,
		&revision.FailureReason, &createdAt, &activatedAt,
	); err != nil {
		return CertificateRevision{}, err
	}
	if err := json.Unmarshal([]byte(dnsNames), &revision.DNSNames); err != nil {
		return CertificateRevision{}, fmt.Errorf("unmarshal certificate dns names: %w", err)
	}
	if err := json.Unmarshal([]byte(ipAddresses), &revision.IPAddresses); err != nil {
		return CertificateRevision{}, fmt.Errorf("unmarshal certificate ip addresses: %w", err)
	}
	var err error
	revision.NotBefore, err = parseOptionalTime(notBefore)
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("parse certificate not_before: %w", err)
	}
	revision.NotAfter, err = parseOptionalTime(notAfter)
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("parse certificate not_after: %w", err)
	}
	revision.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("parse certificate revision created_at: %w", err)
	}
	revision.ActivatedAt, err = parseOptionalTime(activatedAt)
	if err != nil {
		return CertificateRevision{}, fmt.Errorf("parse certificate revision activated_at: %w", err)
	}
	return revision, nil
}

func marshalStringList(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal string list: %w", err)
	}
	return string(encoded), nil
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}
