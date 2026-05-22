package storage

import (
	"context"
	"testing"
	"time"
)

func TestLoginAttemptsCountAndStatsByIPAndUsername(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	base := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	window := time.Hour

	mustRecord := func(username string, sourceIP string, success bool, offset time.Duration) {
		t.Helper()
		if err := store.RecordLoginAttempt(ctx, LoginAttemptParams{
			Username:    username,
			SourceIP:    sourceIP,
			Success:     success,
			AttemptedAt: base.Add(offset),
		}); err != nil {
			t.Fatalf("RecordLoginAttempt returned error: %v", err)
		}
	}

	// Two failed attempts in the IP window, one successful (should not count), one before window.
	mustRecord("alice", "10.0.0.1", false, -30*time.Minute)
	mustRecord("alice", "10.0.0.1", false, -10*time.Minute)
	mustRecord("alice", "10.0.0.1", true, -5*time.Minute)
	mustRecord("alice", "10.0.0.1", false, -90*time.Minute) // outside window
	mustRecord("bob", "10.0.0.2", false, -1*time.Minute)    // different IP

	since := base.Add(-window)
	stats, err := store.CountFailedLoginAttemptsByIP(ctx, "10.0.0.1", since)
	if err != nil {
		t.Fatalf("CountFailedLoginAttemptsByIP returned error: %v", err)
	}
	if stats.Count != 2 {
		t.Fatalf("IP stats count = %d, want 2", stats.Count)
	}
	if !stats.OldestAt.Equal(base.Add(-30 * time.Minute)) {
		t.Fatalf("IP stats oldest = %s, want %s", stats.OldestAt, base.Add(-30*time.Minute))
	}

	// Username "alice" failures across IPs in window.
	mustRecord("alice", "10.0.0.3", false, -20*time.Minute)
	stats, err = store.CountFailedLoginAttemptsByUsername(ctx, "alice", since)
	if err != nil {
		t.Fatalf("CountFailedLoginAttemptsByUsername returned error: %v", err)
	}
	if stats.Count != 3 {
		t.Fatalf("username stats count = %d, want 3 (cross-IP)", stats.Count)
	}

	// Empty identifiers return zero stats without error.
	if stats, err := store.CountFailedLoginAttemptsByIP(ctx, "", since); err != nil || stats.Count != 0 {
		t.Fatalf("CountFailedLoginAttemptsByIP empty = %+v err=%v, want zero", stats, err)
	}
	if stats, err := store.CountFailedLoginAttemptsByUsername(ctx, "", since); err != nil || stats.Count != 0 {
		t.Fatalf("CountFailedLoginAttemptsByUsername empty = %+v err=%v, want zero", stats, err)
	}
}

func TestLoginAttemptsResetClearsFailuresForUsernameOrIP(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	base := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	since := base.Add(-time.Hour)

	for i := 0; i < 3; i++ {
		if err := store.RecordLoginAttempt(ctx, LoginAttemptParams{
			Username:    "alice",
			SourceIP:    "10.0.0.1",
			Success:     false,
			AttemptedAt: base.Add(-time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("RecordLoginAttempt returned error: %v", err)
		}
	}

	deleted, err := store.ResetFailedLoginAttempts(ctx, "alice", "10.0.0.1")
	if err != nil {
		t.Fatalf("ResetFailedLoginAttempts returned error: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("ResetFailedLoginAttempts deleted = %d, want 3", deleted)
	}

	if stats, err := store.CountFailedLoginAttemptsByUsername(ctx, "alice", since); err != nil || stats.Count != 0 {
		t.Fatalf("after reset username stats = %+v err=%v, want zero", stats, err)
	}
}

func TestPruneLoginAttemptsRemovesAgedRows(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	base := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)

	if err := store.RecordLoginAttempt(ctx, LoginAttemptParams{Username: "old", SourceIP: "1.1.1.1", AttemptedAt: base.Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("RecordLoginAttempt returned error: %v", err)
	}
	if err := store.RecordLoginAttempt(ctx, LoginAttemptParams{Username: "fresh", SourceIP: "1.1.1.1", AttemptedAt: base.Add(-10 * time.Minute)}); err != nil {
		t.Fatalf("RecordLoginAttempt returned error: %v", err)
	}

	deleted, err := store.PruneLoginAttempts(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("PruneLoginAttempts returned error: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("PruneLoginAttempts deleted = %d, want 1", deleted)
	}

	stats, err := store.CountFailedLoginAttemptsByIP(ctx, "1.1.1.1", base.Add(-3*time.Hour))
	if err != nil {
		t.Fatalf("CountFailedLoginAttemptsByIP returned error: %v", err)
	}
	if stats.Count != 1 {
		t.Fatalf("remaining count = %d, want 1 (older row pruned)", stats.Count)
	}
}
