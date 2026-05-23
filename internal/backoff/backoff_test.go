package backoff

import (
	"testing"
	"time"
)

func TestBackoffSequenceAndCap(t *testing.T) {
	t.Parallel()
	b := New(1*time.Second, 60*time.Second, 0)

	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		60 * time.Second, // capped
		60 * time.Second, // stays capped
	}

	for i, expected := range want {
		got := b.Next()
		if got != expected {
			t.Fatalf("attempt %d: got %v, want %v", i, got, expected)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	t.Parallel()
	b := New(1*time.Second, 60*time.Second, 0)

	b.Next() // 1s
	b.Next() // 2s
	b.Next() // 4s

	if attempt := b.Attempt(); attempt != 3 {
		t.Fatalf("attempt = %d, want 3", attempt)
	}

	b.Reset()

	if attempt := b.Attempt(); attempt != 0 {
		t.Fatalf("after reset: attempt = %d, want 0", attempt)
	}

	got := b.Next()
	if got != 1*time.Second {
		t.Fatalf("after reset: got %v, want 1s", got)
	}
}

func TestBackoffJitterBounds(t *testing.T) {
	t.Parallel()
	b := New(10*time.Second, 60*time.Second, 0.25)

	for i := 0; i < 100; i++ {
		b.Reset()
		delay := b.Next()
		// Base is 10s, jitter ±25% → [7.5s, 12.5s]
		if delay < 7500*time.Millisecond || delay > 12500*time.Millisecond {
			t.Fatalf("iteration %d: delay %v outside [7.5s, 12.5s]", i, delay)
		}
	}
}

func TestBackoffJitterVariation(t *testing.T) {
	t.Parallel()
	b := New(10*time.Second, 60*time.Second, 0.25)

	seen := make(map[time.Duration]struct{})
	for i := 0; i < 50; i++ {
		b.Reset()
		seen[b.Next()] = struct{}{}
	}

	if len(seen) < 2 {
		t.Fatalf("expected variation in jittered delays, got %d unique values", len(seen))
	}
}

func TestBackoffCapAtZeroDelay(t *testing.T) {
	t.Parallel()
	b := New(0, 5*time.Second, 0)

	got := b.Next()
	if got != 5*time.Second {
		t.Fatalf("got %v, want 5s (capped)", got)
	}
}
