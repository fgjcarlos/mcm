package storage

import (
	"context"
	"errors"
	"testing"
)

func TestCreateMQTTUser(t *testing.T) {
	t.Parallel()

	t.Run("success creates user with correct fields", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		created, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{
			Username:     "alice",
			PasswordHash: "$pbkdf2-sha512$...",
		})
		if err != nil {
			t.Fatalf("CreateMQTTUser returned error: %v", err)
		}
		if created.ID == 0 {
			t.Error("want ID > 0, got 0")
		}
		if created.Username != "alice" {
			t.Errorf("username = %q, want %q", created.Username, "alice")
		}
		if created.PasswordHash != "$pbkdf2-sha512$..." {
			t.Errorf("password_hash = %q, want stored hash", created.PasswordHash)
		}
		if created.Disabled {
			t.Error("want disabled = false, got true")
		}
		if created.CreatedAt.IsZero() {
			t.Error("want non-zero created_at")
		}
		if created.UpdatedAt.IsZero() {
			t.Error("want non-zero updated_at")
		}
	})

	t.Run("duplicate username returns error", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		if _, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "alice", PasswordHash: "hash1"}); err != nil {
			t.Fatalf("first CreateMQTTUser returned error: %v", err)
		}
		if _, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "alice", PasswordHash: "hash2"}); err == nil {
			t.Error("second CreateMQTTUser with duplicate username: want error, got nil")
		}
	})
}

func TestGetMQTTUser(t *testing.T) {
	t.Parallel()

	t.Run("found returns correct user", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		created, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "charlie", PasswordHash: "hashcharlie"})
		if err != nil {
			t.Fatalf("CreateMQTTUser returned error: %v", err)
		}

		got, err := store.GetMQTTUser(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetMQTTUser returned error: %v", err)
		}
		if got.ID != created.ID || got.Username != "charlie" || got.PasswordHash != "hashcharlie" {
			t.Errorf("unexpected user: %#v", got)
		}
	})

	t.Run("not found returns ErrMQTTUserNotFound", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()

		_, err := store.GetMQTTUser(context.Background(), 99999)
		if !errors.Is(err, ErrMQTTUserNotFound) {
			t.Errorf("error = %v, want ErrMQTTUserNotFound", err)
		}
	})
}

func TestGetMQTTUserByUsername(t *testing.T) {
	t.Parallel()

	t.Run("found returns correct user", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		_, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "diana", PasswordHash: "hashdiana"})
		if err != nil {
			t.Fatalf("CreateMQTTUser returned error: %v", err)
		}

		got, err := store.GetMQTTUserByUsername(ctx, "diana")
		if err != nil {
			t.Fatalf("GetMQTTUserByUsername returned error: %v", err)
		}
		if got.Username != "diana" || got.PasswordHash != "hashdiana" {
			t.Errorf("unexpected user: %#v", got)
		}
	})

	t.Run("not found returns ErrMQTTUserNotFound", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()

		_, err := store.GetMQTTUserByUsername(context.Background(), "nobody")
		if !errors.Is(err, ErrMQTTUserNotFound) {
			t.Errorf("error = %v, want ErrMQTTUserNotFound", err)
		}
	})
}

func TestListMQTTUsers(t *testing.T) {
	t.Parallel()

	t.Run("empty returns nil", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()

		users, err := store.ListMQTTUsers(context.Background())
		if err != nil {
			t.Fatalf("ListMQTTUsers returned error: %v", err)
		}
		if users != nil {
			t.Errorf("got %v, want nil", users)
		}
	})

	t.Run("multiple users sorted by username", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		for _, name := range []string{"zebra", "apple", "mango"} {
			if _, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: name, PasswordHash: "h"}); err != nil {
				t.Fatalf("CreateMQTTUser %q returned error: %v", name, err)
			}
		}

		users, err := store.ListMQTTUsers(ctx)
		if err != nil {
			t.Fatalf("ListMQTTUsers returned error: %v", err)
		}
		if len(users) != 3 {
			t.Fatalf("got %d users, want 3", len(users))
		}
		want := []string{"apple", "mango", "zebra"}
		for i, u := range users {
			if u.Username != want[i] {
				t.Errorf("users[%d].Username = %q, want %q", i, u.Username, want[i])
			}
		}
	})
}

func TestUpdateMQTTUser(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (*Store, MQTTUser) {
		t.Helper()
		store := newTestStore(t)
		created, err := store.CreateMQTTUser(context.Background(), CreateMQTTUserParams{
			Username:     "eve",
			PasswordHash: "hasheve",
		})
		if err != nil {
			t.Fatalf("CreateMQTTUser returned error: %v", err)
		}
		return store, created
	}

	newStr := func(s string) *string { return &s }
	newBool := func(b bool) *bool { return &b }

	tests := []struct {
		name      string
		params    UpdateMQTTUserParams
		checkUser func(t *testing.T, u MQTTUser)
	}{
		{
			name:   "update username only",
			params: UpdateMQTTUserParams{Username: newStr("eve2")},
			checkUser: func(t *testing.T, u MQTTUser) {
				t.Helper()
				if u.Username != "eve2" {
					t.Errorf("Username = %q, want %q", u.Username, "eve2")
				}
				if u.PasswordHash != "hasheve" {
					t.Errorf("PasswordHash changed unexpectedly: %q", u.PasswordHash)
				}
				if u.Disabled {
					t.Error("Disabled changed unexpectedly to true")
				}
			},
		},
		{
			name:   "update disabled only",
			params: UpdateMQTTUserParams{Disabled: newBool(true)},
			checkUser: func(t *testing.T, u MQTTUser) {
				t.Helper()
				if !u.Disabled {
					t.Error("Disabled = false, want true")
				}
				if u.Username != "eve" {
					t.Errorf("Username changed unexpectedly: %q", u.Username)
				}
			},
		},
		{
			name:   "update username and password",
			params: UpdateMQTTUserParams{Username: newStr("eve3"), PasswordHash: newStr("newhash")},
			checkUser: func(t *testing.T, u MQTTUser) {
				t.Helper()
				if u.Username != "eve3" {
					t.Errorf("Username = %q, want %q", u.Username, "eve3")
				}
				if u.PasswordHash != "newhash" {
					t.Errorf("PasswordHash = %q, want %q", u.PasswordHash, "newhash")
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, created := setup(t)
			defer store.Close()

			updated, err := store.UpdateMQTTUser(context.Background(), created.ID, tc.params)
			if err != nil {
				t.Fatalf("UpdateMQTTUser returned error: %v", err)
			}
			tc.checkUser(t, updated)
		})
	}

	t.Run("not found returns ErrMQTTUserNotFound", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()

		_, err := store.UpdateMQTTUser(context.Background(), 99999, UpdateMQTTUserParams{})
		if !errors.Is(err, ErrMQTTUserNotFound) {
			t.Errorf("error = %v, want ErrMQTTUserNotFound", err)
		}
	})
}

func TestDeleteMQTTUser(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	created, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{
		Username:     "frank",
		PasswordHash: "hashfrank",
	})
	if err != nil {
		t.Fatalf("CreateMQTTUser returned error: %v", err)
	}

	t.Run("success then get returns ErrMQTTUserNotFound", func(t *testing.T) {
		if err := store.DeleteMQTTUser(ctx, created.ID); err != nil {
			t.Fatalf("DeleteMQTTUser returned error: %v", err)
		}
		_, err := store.GetMQTTUser(ctx, created.ID)
		if !errors.Is(err, ErrMQTTUserNotFound) {
			t.Errorf("GetMQTTUser after delete = %v, want ErrMQTTUserNotFound", err)
		}
	})

	t.Run("delete non-existent returns ErrMQTTUserNotFound", func(t *testing.T) {
		if err := store.DeleteMQTTUser(ctx, 99999); !errors.Is(err, ErrMQTTUserNotFound) {
			t.Errorf("error = %v, want ErrMQTTUserNotFound", err)
		}
	})
}
