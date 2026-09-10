package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
)

// TestRedactPasswdHashes covers issue #296 (acceptance criterion 3):
// the deploy passwd diff must NEVER leak bcrypt hashes back to the
// HTTP client. The redactor replaces each line with a marker that
// shows the hash prefix + length + algorithm — enough to identify the
// hash on disk but useless for offline cracking.
func TestRedactPasswdHashes(t *testing.T) {
	t.Parallel()

	t.Run("replaces bcrypt hash with redacted marker", func(t *testing.T) {
		t.Parallel()
		body := "alice:$2a$10$abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUV\n"
		got := redactPasswdHashes(body)
		// The hash itself must not appear in the output.
		if strings.Contains(got, "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUV") {
			t.Errorf("redacted output still contains the original hash:\n%s", got)
		}
		// The username + a marker should be present.
		if !strings.Contains(got, "alice:") {
			t.Errorf("expected username preserved, got:\n%s", got)
		}
		if !strings.Contains(got, "REDACTED") {
			t.Errorf("expected REDACTED marker, got:\n%s", got)
		}
		// Prefix should be visible (first 7 chars of the hash).
		if !strings.Contains(got, "$2a$10$") {
			t.Errorf("expected bcrypt prefix to be visible, got:\n%s", got)
		}
	})

	t.Run("preserves username lines without a hash", func(t *testing.T) {
		t.Parallel()
		body := "# comment line\n"
		got := redactPasswdHashes(body)
		if got != body {
			t.Errorf("non-hash line was modified:\nwant: %q\ngot:  %q", body, got)
		}
	})

	t.Run("handles multiple users", func(t *testing.T) {
		t.Parallel()
		body := "alice:$2a$10$abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUV\nbob:$2b$12$zyxwvutsrqponmlkjihgfedcba9876543210ZYXWVUTSRQPONMLKJIHGFEDCBA\n"
		got := redactPasswdHashes(body)
		// Neither original hash should appear in the output.
		if strings.Contains(got, "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUV") {
			t.Errorf("redacted output still contains alice's hash:\n%s", got)
		}
		if strings.Contains(got, "zyxwvutsrqponmlkjihgfedcba9876543210ZYXWVUTSRQPONMLKJIHGFEDCBA") {
			t.Errorf("redacted output still contains bob's hash:\n%s", got)
		}
		// Both usernames should be present.
		if !strings.Contains(got, "alice:") {
			t.Errorf("alice username missing:\n%s", got)
		}
		if !strings.Contains(got, "bob:") {
			t.Errorf("bob username missing:\n%s", got)
		}
	})

	t.Run("preserves total user count for the summary caller", func(t *testing.T) {
		t.Parallel()
		// Three users — the count is exposed so the UI can render
		// "3 users in passwd" without leaking the hashes themselves.
		body := "u1:$2a$10$x\n" +
			"u2:$2b$10$x\n" +
			"u3:$2y$10$x\n"
		got := redactPasswdHashes(body)
		if strings.Count(got, "REDACTED") != 3 {
			t.Errorf("expected 3 REDACTED markers, got %d in:\n%s", strings.Count(got, "REDACTED"), got)
		}
	})
}

// TestSummarizeChanges covers issue #296 (acceptance criterion 3):
// the deploy preview must summarise the change set (added / removed /
// rotated users and topics) so the UI can render a one-line summary
// without exposing the broker config in full.
func TestSummarizeChanges(t *testing.T) {
	t.Parallel()

	t.Run("no diffs yields an empty summary", func(t *testing.T) {
		t.Parallel()
		summary := summarizeChanges("", "", "", "")
		if summary.UsersAdded != 0 || summary.UsersRemoved != 0 {
			t.Errorf("expected zero deltas, got %+v", summary)
		}
	})

	t.Run("adds a user", func(t *testing.T) {
		t.Parallel()
		current := "alice:$2a$10$oldhash\n"
		rendered := "alice:$2a$10$oldhash\nbob:$2a$10$newhash\n"
		summary := summarizeChanges(current, rendered, current, rendered)
		if summary.UsersAdded != 1 {
			t.Errorf("UsersAdded = %d, want 1", summary.UsersAdded)
		}
		if summary.UsersRemoved != 0 {
			t.Errorf("UsersRemoved = %d, want 0", summary.UsersRemoved)
		}
		if !summary.HasPasswdChanges {
			t.Errorf("HasPasswdChanges should be true after add")
		}
	})

	t.Run("removes a user", func(t *testing.T) {
		t.Parallel()
		current := "alice:$2a$10$x\nbob:$2a$10$y\n"
		rendered := "alice:$2a$10$x\n"
		summary := summarizeChanges(current, rendered, current, rendered)
		if summary.UsersRemoved != 1 {
			t.Errorf("UsersRemoved = %d, want 1", summary.UsersRemoved)
		}
		if summary.UsersAdded != 0 {
			t.Errorf("UsersAdded = %d, want 0", summary.UsersAdded)
		}
	})

	t.Run("rotates a password (same user, different hash)", func(t *testing.T) {
		t.Parallel()
		current := "alice:$2a$10$oldhash\n"
		rendered := "alice:$2a$12$rotatedhash\n"
		summary := summarizeChanges(current, rendered, current, rendered)
		if summary.UsersRotated != 1 {
			t.Errorf("UsersRotated = %d, want 1", summary.UsersRotated)
		}
		if summary.UsersAdded != 0 || summary.UsersRemoved != 0 {
			t.Errorf("rotation should not count as add/remove, got %+v", summary)
		}
	})

	t.Run("ACL topic changes", func(t *testing.T) {
		t.Parallel()
		current := "user alice\ntopic read sensors/#\n"
		rendered := "user alice\ntopic read sensors/#\ntopic readwrite alerts\n"
		summary := summarizeChanges(current, rendered, current, rendered)
		if summary.TopicsAdded != 1 {
			t.Errorf("TopicsAdded = %d, want 1", summary.TopicsAdded)
		}
	})

	t.Run("no passwd changes does not flag HasPasswdChanges", func(t *testing.T) {
		t.Parallel()
		summary := summarizeChanges("a:b:c\n", "a:b:c\n", "", "")
		if summary.HasPasswdChanges {
			t.Errorf("identical passwd should not flag changes")
		}
	})
}

func TestPreviewCreatesImmutableRevision(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "current acl\n")
	writeFile(t, passwdPath, "alice:$2a$10$oldhash\n")
	aclStore := &fakeACLStore{rules: []acl.Rule{{Principal: "alice", TopicFilter: "sensors/#", Permission: "read"}}}
	svc := newTestService(&fakeApplier{}, aclStore, &fakeMQTTUserLister{}, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), newFakePasswordLookup(), deployCfg)

	result, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	if result.RevisionID == "" {
		t.Fatal("Preview returned an empty revision ID")
	}
	if result.BaseACLHash == "" || result.BasePasswdHash == "" || result.RenderedACLHash == "" || result.RenderedPasswdHash == "" {
		t.Fatalf("Preview hashes are incomplete: %+v", result)
	}
	if strings.Contains(result.PasswdDiff, "oldhash") || strings.Contains(result.PasswdDiff, "$2a$10$oldhash") {
		t.Fatalf("passwd diff leaked a hash: %s", result.PasswdDiff)
	}
	if result.PasswdBody != "" && strings.Contains(result.PasswdDiff, result.PasswdBody) {
		t.Fatal("passwd diff must not contain the unredacted rendered body")
	}

	revision, err := svc.revisionStore.GetPreviewRevision(context.Background(), result.RevisionID)
	if err != nil {
		t.Fatalf("GetPreviewRevision returned error: %v", err)
	}
	if revision.ACLRendered != result.ACLBody || revision.PasswdRendered != result.PasswdBody {
		t.Fatal("revision did not persist the exact rendered bodies")
	}
}

func TestApplyRejectsChangedBase(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "current acl\n")
	writeFile(t, passwdPath, "")
	svc := newTestService(&fakeApplier{}, &fakeACLStore{}, &fakeMQTTUserLister{}, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), newFakePasswordLookup(), deployCfg)

	preview, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	writeFile(t, aclPath, "external change\n")

	_, err = svc.Apply(context.Background(), "operator", preview.RevisionID)
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("Apply error = %v, want ErrRevisionMismatch", err)
	}
}

func TestApplyRejectsDesiredStateChangedAfterPreview(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")
	aclStore := &fakeACLStore{}
	svc := newTestService(&fakeApplier{}, aclStore, &fakeMQTTUserLister{}, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), newFakePasswordLookup(), deployCfg)

	preview, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	aclStore.rules = []acl.Rule{{Principal: "alice", TopicFilter: "sensors/#", Permission: "read"}}

	_, err = svc.Apply(context.Background(), "operator", preview.RevisionID)
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("Apply error = %v, want ErrRevisionMismatch", err)
	}
}

func TestApplyRejectsExpiredRevision(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")
	svc := newTestService(&fakeApplier{}, &fakeACLStore{}, &fakeMQTTUserLister{}, newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), newFakePasswordLookup(), deployCfg)

	preview, err := svc.Preview(context.Background(), "operator")
	if err != nil {
		t.Fatalf("Preview returned error: %v", err)
	}
	memoryStore, ok := svc.revisionStore.(*memoryPreviewRevisionStore)
	if !ok {
		t.Fatalf("revision store = %T, want memoryPreviewRevisionStore", svc.revisionStore)
	}
	memoryStore.mu.Lock()
	revision := memoryStore.revisions[preview.RevisionID]
	revision.CreatedAt = time.Now().UTC().Add(-previewRevisionTTL - time.Minute)
	memoryStore.revisions[preview.RevisionID] = revision
	memoryStore.mu.Unlock()

	_, err = svc.Apply(context.Background(), "operator", preview.RevisionID)
	if !errors.Is(err, ErrRevisionExpired) {
		t.Fatalf("Apply error = %v, want ErrRevisionExpired", err)
	}
}
