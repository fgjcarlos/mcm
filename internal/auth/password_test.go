package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordUsesArgon2idAndDoesNotStorePlaintext(t *testing.T) {
	hash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if hash == "secret-password" {
		t.Fatal("HashPassword returned plaintext password")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("HashPassword produced unexpected format: %q", hash)
	}

	match, err := VerifyPassword(hash, "secret-password")
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if !match {
		t.Fatal("VerifyPassword returned false, want true")
	}

	match, err = VerifyPassword(hash, "wrong-password")
	if err != nil {
		t.Fatalf("VerifyPassword with wrong password returned error: %v", err)
	}
	if match {
		t.Fatal("VerifyPassword returned true for wrong password")
	}
}
