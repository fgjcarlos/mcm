package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCertificateStore_ProfileUpsertAndList(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	created, err := store.UpsertCertificateProfile(ctx, "production", "broker certificates", time.Time{})
	if err != nil {
		t.Fatalf("UpsertCertificateProfile returned error: %v", err)
	}
	if created.ID == 0 || created.Name != "production" {
		t.Fatalf("UpsertCertificateProfile = %#v, want a persisted profile", created)
	}

	updated, err := store.UpsertCertificateProfile(ctx, "production", "renewed certificates", time.Time{})
	if err != nil {
		t.Fatalf("UpsertCertificateProfile returned error: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("UpsertCertificateProfile created a second profile: %#v", updated)
	}
	if updated.Description != "renewed certificates" {
		t.Fatalf("UpsertCertificateProfile description = %q, want the updated text", updated.Description)
	}

	profiles, err := store.ListCertificateProfiles(ctx)
	if err != nil {
		t.Fatalf("ListCertificateProfiles returned error: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("ListCertificateProfiles = %d profiles, want 1", len(profiles))
	}
}

func TestCertificateStore_GetProfileByName(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	if _, err := store.GetCertificateProfileByName(ctx, "missing"); !errors.Is(err, ErrCertificateProfileNotFound) {
		t.Fatalf("GetCertificateProfileByName error = %v, want ErrCertificateProfileNotFound", err)
	}

	if _, err := store.UpsertCertificateProfile(ctx, "production", "", time.Time{}); err != nil {
		t.Fatalf("UpsertCertificateProfile returned error: %v", err)
	}
	profile, err := store.GetCertificateProfileByName(ctx, "production")
	if err != nil {
		t.Fatalf("GetCertificateProfileByName returned error: %v", err)
	}
	if profile.Name != "production" {
		t.Fatalf("GetCertificateProfileByName = %#v, want the production profile", profile)
	}
}

func TestCertificateStore_InsertAndReadRevision(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	profile, err := store.UpsertCertificateProfile(ctx, "production", "", time.Time{})
	if err != nil {
		t.Fatalf("UpsertCertificateProfile returned error: %v", err)
	}
	notBefore := time.Now().UTC().Add(-time.Hour).Truncate(time.Nanosecond)
	notAfter := notBefore.Add(90 * 24 * time.Hour)

	inserted, err := store.InsertCertificateRevision(ctx, CreateCertificateRevisionParams{
		ProfileID:         profile.ID,
		RevisionID:        "rev-1",
		Actor:             "operator",
		MaterialHash:      "abc123",
		Subject:           "CN=broker.example.com",
		Issuer:            "CN=mcm-root",
		DNSNames:          []string{"broker.example.com"},
		IPAddresses:       []string{"10.0.0.10"},
		NotBefore:         notBefore,
		NotAfter:          notAfter,
		FingerprintSHA256: "fingerprint",
		KeyAlgorithm:      "ECDSA-P256",
		HasCA:             true,
		HasChain:          true,
		SerialNumber:      "42",
	})
	if err != nil {
		t.Fatalf("InsertCertificateRevision returned error: %v", err)
	}
	if inserted.State != CertificateRevisionStatePending {
		t.Fatalf("InsertCertificateRevision state = %q, want %q", inserted.State, CertificateRevisionStatePending)
	}

	got, err := store.GetCertificateRevision(ctx, profile.ID, "rev-1")
	if err != nil {
		t.Fatalf("GetCertificateRevision returned error: %v", err)
	}
	if got.MaterialHash != "abc123" || got.Actor != "operator" {
		t.Fatalf("GetCertificateRevision = %#v, want the stored metadata", got)
	}
	if len(got.DNSNames) != 1 || got.DNSNames[0] != "broker.example.com" {
		t.Fatalf("GetCertificateRevision dns names = %+v, want the stored list", got.DNSNames)
	}
	if len(got.IPAddresses) != 1 || got.IPAddresses[0] != "10.0.0.10" {
		t.Fatalf("GetCertificateRevision ip addresses = %+v, want the stored list", got.IPAddresses)
	}
	if !got.NotAfter.Equal(notAfter) {
		t.Fatalf("GetCertificateRevision not after = %v, want %v", got.NotAfter, notAfter)
	}
	if !got.HasCA || !got.HasChain {
		t.Fatalf("GetCertificateRevision = %#v, want the CA and chain flags", got)
	}
}

func TestCertificateStore_GetRevisionNotFound(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	profile, err := store.UpsertCertificateProfile(ctx, "production", "", time.Time{})
	if err != nil {
		t.Fatalf("UpsertCertificateProfile returned error: %v", err)
	}
	if _, err := store.GetCertificateRevision(ctx, profile.ID, "rev-404"); !errors.Is(err, ErrCertificateRevisionNotFound) {
		t.Fatalf("GetCertificateRevision error = %v, want ErrCertificateRevisionNotFound", err)
	}
}

func TestCertificateStore_ListRevisionsNewestFirst(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	profile, err := store.UpsertCertificateProfile(ctx, "production", "", time.Time{})
	if err != nil {
		t.Fatalf("UpsertCertificateProfile returned error: %v", err)
	}
	for _, revisionID := range []string{"rev-1", "rev-2"} {
		if _, err := store.InsertCertificateRevision(ctx, CreateCertificateRevisionParams{
			ProfileID:  profile.ID,
			RevisionID: revisionID,
			Actor:      "operator",
			CreatedAt:  time.Now().UTC(),
		}); err != nil {
			t.Fatalf("InsertCertificateRevision returned error: %v", err)
		}
	}

	revisions, err := store.ListCertificateRevisions(ctx, profile.ID)
	if err != nil {
		t.Fatalf("ListCertificateRevisions returned error: %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("ListCertificateRevisions = %d revisions, want 2", len(revisions))
	}
	if revisions[0].RevisionID != "rev-2" {
		t.Fatalf("ListCertificateRevisions newest = %q, want rev-2", revisions[0].RevisionID)
	}
}

func TestCertificateStore_UpdateRevisionState(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	profile, err := store.UpsertCertificateProfile(ctx, "production", "", time.Time{})
	if err != nil {
		t.Fatalf("UpsertCertificateProfile returned error: %v", err)
	}
	revision, err := store.InsertCertificateRevision(ctx, CreateCertificateRevisionParams{
		ProfileID:  profile.ID,
		RevisionID: "rev-1",
		Actor:      "operator",
	})
	if err != nil {
		t.Fatalf("InsertCertificateRevision returned error: %v", err)
	}

	activatedAt := time.Now().UTC().Truncate(time.Nanosecond)
	if err := store.UpdateCertificateRevisionState(ctx, revision.ID, CertificateRevisionStateActive, "", activatedAt); err != nil {
		t.Fatalf("UpdateCertificateRevisionState returned error: %v", err)
	}
	got, err := store.GetCertificateRevision(ctx, profile.ID, "rev-1")
	if err != nil {
		t.Fatalf("GetCertificateRevision returned error: %v", err)
	}
	if got.State != CertificateRevisionStateActive {
		t.Fatalf("UpdateCertificateRevisionState state = %q, want %q", got.State, CertificateRevisionStateActive)
	}
	if !got.ActivatedAt.Equal(activatedAt) {
		t.Fatalf("UpdateCertificateRevisionState activated at = %v, want %v", got.ActivatedAt, activatedAt)
	}

	if err := store.UpdateCertificateRevisionState(ctx, revision.ID, CertificateRevisionStateFailed, "handshake failed", time.Time{}); err != nil {
		t.Fatalf("UpdateCertificateRevisionState returned error: %v", err)
	}
	got, err = store.GetCertificateRevision(ctx, profile.ID, "rev-1")
	if err != nil {
		t.Fatalf("GetCertificateRevision returned error: %v", err)
	}
	if got.State != CertificateRevisionStateFailed || got.FailureReason != "handshake failed" {
		t.Fatalf("UpdateCertificateRevisionState = %#v, want the failed state", got)
	}
	if !got.ActivatedAt.Equal(activatedAt) {
		t.Fatalf("UpdateCertificateRevisionState cleared the activation timestamp: %v", got.ActivatedAt)
	}
}

func TestCertificateStore_RevisionIDIsUniquePerProfile(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	profile, err := store.UpsertCertificateProfile(ctx, "production", "", time.Time{})
	if err != nil {
		t.Fatalf("UpsertCertificateProfile returned error: %v", err)
	}
	params := CreateCertificateRevisionParams{ProfileID: profile.ID, RevisionID: "rev-1", Actor: "operator"}
	if _, err := store.InsertCertificateRevision(ctx, params); err != nil {
		t.Fatalf("InsertCertificateRevision returned error: %v", err)
	}
	if _, err := store.InsertCertificateRevision(ctx, params); err == nil {
		t.Fatal("InsertCertificateRevision accepted a duplicate revision id")
	}
}
