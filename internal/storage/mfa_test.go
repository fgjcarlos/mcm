package storage

import (
	"context"
	"errors"
	"testing"
)

func TestConsumeTOTPTimeStepRejectsReplayAndOlderSteps(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	user, err := store.CreateAdminUser(ctx, CreateAdminUserParams{Username: "admin", PasswordHash: "hash"})
	if err != nil {
		t.Fatalf("CreateAdminUser returned error: %v", err)
	}
	if err := store.ConsumeTOTPTimeStep(ctx, user.ID, 42); err != nil {
		t.Fatalf("first ConsumeTOTPTimeStep returned error: %v", err)
	}
	if err := store.ConsumeTOTPTimeStep(ctx, user.ID, 42); !errors.Is(err, ErrTOTPCodeReused) {
		t.Fatalf("replay ConsumeTOTPTimeStep error = %v, want ErrTOTPCodeReused", err)
	}
	if err := store.ConsumeTOTPTimeStep(ctx, user.ID, 41); !errors.Is(err, ErrTOTPCodeReused) {
		t.Fatalf("older ConsumeTOTPTimeStep error = %v, want ErrTOTPCodeReused", err)
	}
	if err := store.ConsumeTOTPTimeStep(ctx, user.ID, 43); err != nil {
		t.Fatalf("newer ConsumeTOTPTimeStep returned error: %v", err)
	}
}
