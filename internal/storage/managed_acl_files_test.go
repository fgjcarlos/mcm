package storage

import (
	"context"
	"testing"
	"time"
)

// TestManagedACLFileRegistryDoesNotTakeTheMutationLock pins that the
// registry of the per-listener ACL files can be written while the caller
// holds the mutation lock.
//
// The deploy service holds the mutation lock while it updates the registry:
// the registry follows the apply, so the deploy would deadlock on itself if
// the registry took the lock again.
func TestManagedACLFileRegistryDoesNotTakeTheMutationLock(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	store.LockMutations()
	defer store.UnlockMutations()

	done := make(chan error, 1)
	go func() {
		if err := store.RecordManagedACLFile(ctx, "/var/lib/mosquitto-config/acl.e2e", "e2e"); err != nil {
			done <- err
			return
		}
		done <- store.DeleteManagedACLFile(ctx, "/var/lib/mosquitto-config/acl.e2e")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the registry write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the registry write blocked while the caller held the mutation lock")
	}
}
