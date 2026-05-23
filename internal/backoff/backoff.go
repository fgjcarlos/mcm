package backoff

import (
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// Backoff provides capped exponential backoff with jitter. It is safe for
// concurrent use. Call Next to get the next delay, and Reset after a
// successful operation to restart the sequence.
type Backoff struct {
	mu      sync.Mutex
	attempt int
	base    time.Duration
	cap     time.Duration
	jitter  float64
}

// New returns a Backoff that starts at base, doubles each attempt up to cap,
// and adds ±jitter fraction (e.g., 0.25 means ±25%).
func New(base, cap time.Duration, jitter float64) *Backoff {
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	return &Backoff{base: base, cap: cap, jitter: jitter}
}

// Next returns the next backoff delay and advances the attempt counter.
func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	exp := math.Pow(2, float64(b.attempt))
	delay := time.Duration(float64(b.base) * exp)
	if delay > b.cap || delay <= 0 {
		delay = b.cap
	}

	if b.jitter > 0 {
		delta := float64(delay) * b.jitter
		delay = time.Duration(float64(delay) + (rand.Float64()*2-1)*delta)
		if delay < 0 {
			delay = b.base
		}
	}

	b.attempt++
	return delay
}

// Reset restarts the backoff sequence from the initial base delay.
func (b *Backoff) Reset() {
	b.mu.Lock()
	b.attempt = 0
	b.mu.Unlock()
}

// Attempt returns the current attempt number (0-indexed, pre-increment).
func (b *Backoff) Attempt() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.attempt
}
