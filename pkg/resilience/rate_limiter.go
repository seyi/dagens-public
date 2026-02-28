package resilience

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Rate limiter errors
var (
	ErrRateLimited     = errors.New("rate limit exceeded")
	ErrLimiterClosed   = errors.New("rate limiter is closed")
)

// RateLimiter is the interface for rate limiting implementations
type RateLimiter interface {
	// Allow reports whether an event can happen now
	Allow() bool
	// AllowN reports whether n events can happen now
	AllowN(n int) bool
	// Wait blocks until an event can happen or context is canceled
	Wait(ctx context.Context) error
	// WaitN blocks until n events can happen or context is canceled
	WaitN(ctx context.Context, n int) error
	// Limit returns the current rate limit
	Limit() float64
	// Burst returns the current burst limit
	Burst() int
}

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	rate       float64     // tokens per second
	burst      int         // maximum burst size
	tokens     float64     // current tokens
	lastUpdate time.Time   // last token update time
	mu         sync.Mutex
	closed     bool
}

// NewTokenBucket creates a new token bucket rate limiter
func NewTokenBucket(rate float64, burst int) *TokenBucket {
	return &TokenBucket{
		rate:       rate,
		burst:      burst,
		tokens:     float64(burst), // Start with full bucket
		lastUpdate: time.Now(),
	}
}

// Allow checks if one token is available
func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

// AllowN checks if n tokens are available
func (tb *TokenBucket) AllowN(n int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.closed {
		return false
	}

	tb.refill()

	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}

	return false
}

// Wait blocks until a token is available
func (tb *TokenBucket) Wait(ctx context.Context) error {
	return tb.WaitN(ctx, 1)
}

// WaitN blocks until n tokens are available
func (tb *TokenBucket) WaitN(ctx context.Context, n int) error {
	for {
		tb.mu.Lock()
		if tb.closed {
			tb.mu.Unlock()
			return ErrLimiterClosed
		}

		tb.refill()

		if tb.tokens >= float64(n) {
			tb.tokens -= float64(n)
			tb.mu.Unlock()
			return nil
		}

		// Calculate wait time for tokens
		deficit := float64(n) - tb.tokens
		waitTime := time.Duration(deficit / tb.rate * float64(time.Second))
		tb.mu.Unlock()

		// Wait with context awareness
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			// Try again
		}
	}
}

// Limit returns the rate limit
func (tb *TokenBucket) Limit() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.rate
}

// Burst returns the burst limit
func (tb *TokenBucket) Burst() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.burst
}

// SetLimit updates the rate limit
func (tb *TokenBucket) SetLimit(rate float64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.rate = rate
}

// SetBurst updates the burst limit
func (tb *TokenBucket) SetBurst(burst int) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.burst = burst
}

// Close closes the rate limiter
func (tb *TokenBucket) Close() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.closed = true
}

// refill adds tokens based on elapsed time (must be called with lock held)
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastUpdate).Seconds()
	tb.lastUpdate = now

	// Add tokens based on elapsed time
	tb.tokens += elapsed * tb.rate

	// Cap at burst limit
	if tb.tokens > float64(tb.burst) {
		tb.tokens = float64(tb.burst)
	}
}

// SlidingWindowLimiter implements a sliding window rate limiter
type SlidingWindowLimiter struct {
	rate       int           // max requests per window
	window     time.Duration // window duration
	timestamps []time.Time   // request timestamps
	mu         sync.Mutex
}

// NewSlidingWindowLimiter creates a new sliding window rate limiter
func NewSlidingWindowLimiter(rate int, window time.Duration) *SlidingWindowLimiter {
	return &SlidingWindowLimiter{
		rate:       rate,
		window:     window,
		timestamps: make([]time.Time, 0, rate),
	}
}

// Allow checks if a request is allowed
func (sw *SlidingWindowLimiter) Allow() bool {
	return sw.AllowN(1)
}

// AllowN checks if n requests are allowed
func (sw *SlidingWindowLimiter) AllowN(n int) bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	sw.cleanup()

	if len(sw.timestamps)+n <= sw.rate {
		now := time.Now()
		for i := 0; i < n; i++ {
			sw.timestamps = append(sw.timestamps, now)
		}
		return true
	}

	return false
}

// Wait blocks until a request is allowed
func (sw *SlidingWindowLimiter) Wait(ctx context.Context) error {
	return sw.WaitN(ctx, 1)
}

// WaitN blocks until n requests are allowed
func (sw *SlidingWindowLimiter) WaitN(ctx context.Context, n int) error {
	for {
		sw.mu.Lock()
		sw.cleanup()

		if len(sw.timestamps)+n <= sw.rate {
			now := time.Now()
			for i := 0; i < n; i++ {
				sw.timestamps = append(sw.timestamps, now)
			}
			sw.mu.Unlock()
			return nil
		}

		// Calculate wait time until oldest entry expires
		var waitTime time.Duration
		if len(sw.timestamps) > 0 {
			waitTime = sw.window - time.Since(sw.timestamps[0])
			if waitTime < 0 {
				waitTime = time.Millisecond
			}
		} else {
			waitTime = time.Millisecond
		}
		sw.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			// Try again
		}
	}
}

// Limit returns the rate limit
func (sw *SlidingWindowLimiter) Limit() float64 {
	return float64(sw.rate) / sw.window.Seconds()
}

// Burst returns the burst limit
func (sw *SlidingWindowLimiter) Burst() int {
	return sw.rate
}

// cleanup removes expired timestamps (must be called with lock held)
func (sw *SlidingWindowLimiter) cleanup() {
	now := time.Now()
	cutoff := now.Add(-sw.window)

	// Find first non-expired timestamp
	i := 0
	for i < len(sw.timestamps) && sw.timestamps[i].Before(cutoff) {
		i++
	}

	// Remove expired timestamps
	if i > 0 {
		sw.timestamps = sw.timestamps[i:]
	}
}

// LeakyBucket implements a leaky bucket rate limiter
type LeakyBucket struct {
	rate       float64     // leak rate (requests per second)
	burst      int         // bucket capacity
	water      float64     // current water level
	lastUpdate time.Time
	mu         sync.Mutex
}

// NewLeakyBucket creates a new leaky bucket rate limiter
func NewLeakyBucket(rate float64, burst int) *LeakyBucket {
	return &LeakyBucket{
		rate:       rate,
		burst:      burst,
		water:      0,
		lastUpdate: time.Now(),
	}
}

// Allow checks if a request is allowed
func (lb *LeakyBucket) Allow() bool {
	return lb.AllowN(1)
}

// AllowN checks if n requests are allowed
func (lb *LeakyBucket) AllowN(n int) bool {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	lb.leak()

	if lb.water+float64(n) <= float64(lb.burst) {
		lb.water += float64(n)
		return true
	}

	return false
}

// Wait blocks until a request is allowed
func (lb *LeakyBucket) Wait(ctx context.Context) error {
	return lb.WaitN(ctx, 1)
}

// WaitN blocks until n requests are allowed
func (lb *LeakyBucket) WaitN(ctx context.Context, n int) error {
	for {
		lb.mu.Lock()
		lb.leak()

		if lb.water+float64(n) <= float64(lb.burst) {
			lb.water += float64(n)
			lb.mu.Unlock()
			return nil
		}

		// Calculate wait time
		excess := lb.water + float64(n) - float64(lb.burst)
		waitTime := time.Duration(excess / lb.rate * float64(time.Second))
		lb.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			// Try again
		}
	}
}

// Limit returns the rate limit
func (lb *LeakyBucket) Limit() float64 {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.rate
}

// Burst returns the burst limit
func (lb *LeakyBucket) Burst() int {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.burst
}

// leak removes water based on elapsed time (must be called with lock held)
func (lb *LeakyBucket) leak() {
	now := time.Now()
	elapsed := now.Sub(lb.lastUpdate).Seconds()
	lb.lastUpdate = now

	lb.water -= elapsed * lb.rate
	if lb.water < 0 {
		lb.water = 0
	}
}

// PerKeyRateLimiter provides rate limiting per key (e.g., per tenant, per API key)
type PerKeyRateLimiter struct {
	factory  func() RateLimiter
	limiters map[string]RateLimiter
	mu       sync.RWMutex
}

// NewPerKeyRateLimiter creates a new per-key rate limiter
func NewPerKeyRateLimiter(factory func() RateLimiter) *PerKeyRateLimiter {
	return &PerKeyRateLimiter{
		factory:  factory,
		limiters: make(map[string]RateLimiter),
	}
}

// Allow checks if a request for the given key is allowed
func (pk *PerKeyRateLimiter) Allow(key string) bool {
	return pk.getLimiter(key).Allow()
}

// AllowN checks if n requests for the given key are allowed
func (pk *PerKeyRateLimiter) AllowN(key string, n int) bool {
	return pk.getLimiter(key).AllowN(n)
}

// Wait blocks until a request for the given key is allowed
func (pk *PerKeyRateLimiter) Wait(ctx context.Context, key string) error {
	return pk.getLimiter(key).Wait(ctx)
}

// WaitN blocks until n requests for the given key are allowed
func (pk *PerKeyRateLimiter) WaitN(ctx context.Context, key string, n int) error {
	return pk.getLimiter(key).WaitN(ctx, n)
}

// getLimiter returns or creates a limiter for the given key
func (pk *PerKeyRateLimiter) getLimiter(key string) RateLimiter {
	pk.mu.RLock()
	limiter, exists := pk.limiters[key]
	pk.mu.RUnlock()

	if exists {
		return limiter
	}

	pk.mu.Lock()
	defer pk.mu.Unlock()

	// Double-check
	if limiter, exists = pk.limiters[key]; exists {
		return limiter
	}

	limiter = pk.factory()
	pk.limiters[key] = limiter
	return limiter
}

// Keys returns all keys with rate limiters
func (pk *PerKeyRateLimiter) Keys() []string {
	pk.mu.RLock()
	defer pk.mu.RUnlock()

	keys := make([]string, 0, len(pk.limiters))
	for k := range pk.limiters {
		keys = append(keys, k)
	}
	return keys
}

// Remove removes the rate limiter for a key
func (pk *PerKeyRateLimiter) Remove(key string) {
	pk.mu.Lock()
	defer pk.mu.Unlock()
	delete(pk.limiters, key)
}

// Convenience constructors

// NewRateLimiter creates a token bucket rate limiter with the specified rate
func NewRateLimiter(requestsPerSecond float64, burst int) RateLimiter {
	return NewTokenBucket(requestsPerSecond, burst)
}

// NewPerTenantRateLimiter creates a per-tenant rate limiter
func NewPerTenantRateLimiter(requestsPerSecond float64, burst int) *PerKeyRateLimiter {
	return NewPerKeyRateLimiter(func() RateLimiter {
		return NewTokenBucket(requestsPerSecond, burst)
	})
}

// NewPerAPIKeyRateLimiter creates a per-API-key rate limiter
func NewPerAPIKeyRateLimiter(requestsPerSecond float64, burst int) *PerKeyRateLimiter {
	return NewPerTenantRateLimiter(requestsPerSecond, burst)
}
