package resilience

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// Backoff Tests
// ============================================================================

func TestExponentialBackoff_NextDelay(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0, // No jitter for predictable tests
		MaxRetries:   5,
	}

	backoff := NewExponentialBackoff(config)

	// Expected delays: 100ms, 200ms, 400ms, 800ms, 1600ms, 3200ms, 6400ms, 10s (capped)
	expected := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
	}

	for i, want := range expected {
		got := backoff.NextDelay(i)
		if got != want {
			t.Errorf("attempt %d: got %v, want %v", i, got, want)
		}
	}
}

func TestExponentialBackoff_MaxDelay(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Second,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0,
	}

	backoff := NewExponentialBackoff(config)

	// After many attempts, delay should be capped at MaxDelay
	delay := backoff.NextDelay(100)
	if delay != 5*time.Second {
		t.Errorf("expected max delay %v, got %v", 5*time.Second, delay)
	}
}

func TestExponentialBackoff_WithJitter(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0.1,
		MaxRetries:   5,
	}

	backoff := NewExponentialBackoff(config)

	// With jitter, delays should vary
	delays := make([]time.Duration, 10)
	for i := 0; i < 10; i++ {
		delays[i] = backoff.NextDelay(0)
	}

	// Check that not all delays are the same (jitter is working)
	allSame := true
	for i := 1; i < len(delays); i++ {
		if delays[i] != delays[0] {
			allSame = false
			break
		}
	}

	if allSame {
		t.Error("expected jitter to produce varying delays")
	}
}

func TestRetrier_Success(t *testing.T) {
	config := DefaultBackoffConfig()
	config.MaxRetries = 3
	retrier := NewRetrier(config)

	callCount := 0
	err := retrier.Do(context.Background(), func(ctx context.Context) error {
		callCount++
		return nil
	})

	if err != nil {
		t.Errorf("expected success, got error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestRetrier_RetryThenSuccess(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
		JitterFactor: 0,
		MaxRetries:   5,
	}
	retrier := NewRetrier(config)

	callCount := 0
	err := retrier.Do(context.Background(), func(ctx context.Context) error {
		callCount++
		if callCount < 3 {
			return errors.New("transient error")
		}
		return nil
	})

	if err != nil {
		t.Errorf("expected success, got error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestRetrier_ExhaustedRetries(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
		JitterFactor: 0,
		MaxRetries:   3,
	}
	retrier := NewRetrier(config)

	callCount := 0
	err := retrier.Do(context.Background(), func(ctx context.Context) error {
		callCount++
		return errors.New("persistent error")
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if callCount != 4 { // Initial attempt + 3 retries
		t.Errorf("expected 4 calls, got %d", callCount)
	}

	var retryErr *RetryError
	if !errors.As(err, &retryErr) {
		t.Error("expected RetryError")
	}
}

func TestRetrier_ContextCanceled(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		MaxRetries:   10,
	}
	retrier := NewRetrier(config)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	callCount := 0
	err := retrier.Do(ctx, func(ctx context.Context) error {
		callCount++
		return errors.New("error")
	})

	if !errors.Is(err, ErrContextCanceled) {
		t.Errorf("expected ErrContextCanceled, got %v", err)
	}
}

func TestRetrier_OnRetryCallback(t *testing.T) {
	config := BackoffConfig{
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
		JitterFactor: 0,
		MaxRetries:   3,
	}
	retrier := NewRetrier(config)

	retryAttempts := make([]int, 0)
	callCount := 0

	err := retrier.Do(context.Background(), func(ctx context.Context) error {
		callCount++
		if callCount < 3 {
			return errors.New("error")
		}
		return nil
	}, WithOnRetry(func(attempt int, err error, delay time.Duration) {
		retryAttempts = append(retryAttempts, attempt)
	}))

	if err != nil {
		t.Errorf("expected success, got error: %v", err)
	}

	if len(retryAttempts) != 2 {
		t.Errorf("expected 2 retry callbacks, got %d", len(retryAttempts))
	}
}

// ============================================================================
// Circuit Breaker Tests
// ============================================================================

func TestCircuitBreaker_InitialState(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig("test"))

	if cb.State() != StateClosed {
		t.Errorf("expected StateClosed, got %v", cb.State())
	}
}

func TestCircuitBreaker_OpenOnFailures(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          1 * time.Second,
	}
	cb := NewCircuitBreaker(config)

	// Cause failures to open the circuit
	for i := 0; i < 3; i++ {
		cb.Execute(context.Background(), func(ctx context.Context) error {
			return errors.New("failure")
		})
	}

	if cb.State() != StateOpen {
		t.Errorf("expected StateOpen, got %v", cb.State())
	}
}

func TestCircuitBreaker_RejectsWhenOpen(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		FailureThreshold: 1,
		Timeout:          1 * time.Hour, // Long timeout
	}
	cb := NewCircuitBreaker(config)

	// Open the circuit
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failure")
	})

	// Should be rejected
	err := cb.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_TransitionToHalfOpen(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		FailureThreshold: 1,
		Timeout:          10 * time.Millisecond,
	}
	cb := NewCircuitBreaker(config)

	// Open the circuit
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failure")
	})

	// Wait for timeout
	time.Sleep(20 * time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Errorf("expected StateHalfOpen, got %v", cb.State())
	}
}

func TestCircuitBreaker_CloseAfterSuccessInHalfOpen(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:                "test",
		FailureThreshold:    1,
		SuccessThreshold:    2,
		Timeout:             10 * time.Millisecond,
		HalfOpenMaxRequests: 10,
	}
	cb := NewCircuitBreaker(config)

	// Open the circuit
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failure")
	})

	// Wait for timeout
	time.Sleep(20 * time.Millisecond)

	// Successful requests in half-open state
	for i := 0; i < 2; i++ {
		cb.Execute(context.Background(), func(ctx context.Context) error {
			return nil
		})
	}

	if cb.State() != StateClosed {
		t.Errorf("expected StateClosed, got %v", cb.State())
	}
}

func TestCircuitBreaker_ReopenOnFailureInHalfOpen(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:                "test",
		FailureThreshold:    1,
		SuccessThreshold:    2,
		Timeout:             10 * time.Millisecond,
		HalfOpenMaxRequests: 10,
	}
	cb := NewCircuitBreaker(config)

	// Open the circuit
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failure")
	})

	// Wait for timeout
	time.Sleep(20 * time.Millisecond)

	// Fail in half-open state
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failure")
	})

	if cb.State() != StateOpen {
		t.Errorf("expected StateOpen, got %v", cb.State())
	}
}

func TestCircuitBreaker_StateChangeCallback(t *testing.T) {
	transitions := make([]string, 0)
	var mu sync.Mutex

	config := CircuitBreakerConfig{
		Name:             "test",
		FailureThreshold: 1,
		Timeout:          10 * time.Millisecond,
		OnStateChange: func(name string, from, to CircuitState) {
			mu.Lock()
			transitions = append(transitions, from.String()+"->"+to.String())
			mu.Unlock()
		},
	}
	cb := NewCircuitBreaker(config)

	// Open the circuit
	cb.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("failure")
	})

	mu.Lock()
	if len(transitions) != 1 || transitions[0] != "closed->open" {
		t.Errorf("expected [closed->open], got %v", transitions)
	}
	mu.Unlock()
}

func TestCircuitBreakerRegistry(t *testing.T) {
	registry := NewCircuitBreakerRegistry()

	cb1 := registry.Get("service-a")
	cb2 := registry.Get("service-a")
	cb3 := registry.Get("service-b")

	// Same name should return same instance
	if cb1 != cb2 {
		t.Error("expected same circuit breaker for same name")
	}

	// Different name should return different instance
	if cb1 == cb3 {
		t.Error("expected different circuit breaker for different name")
	}
}

// ============================================================================
// Rate Limiter Tests
// ============================================================================

func TestTokenBucket_Allow(t *testing.T) {
	limiter := NewTokenBucket(10, 5) // 10 req/s, burst of 5

	// Should allow up to burst
	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Errorf("expected Allow() to succeed at iteration %d", i)
		}
	}

	// Should be rate limited
	if limiter.Allow() {
		t.Error("expected Allow() to fail after burst exhausted")
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	limiter := NewTokenBucket(100, 5) // 100 req/s, burst of 5

	// Exhaust tokens
	for i := 0; i < 5; i++ {
		limiter.Allow()
	}

	// Wait for refill
	time.Sleep(30 * time.Millisecond) // Should refill ~3 tokens

	// Should allow some requests
	if !limiter.Allow() {
		t.Error("expected Allow() to succeed after refill")
	}
}

func TestTokenBucket_Wait(t *testing.T) {
	limiter := NewTokenBucket(100, 1) // 100 req/s, burst of 1

	// First should succeed immediately
	start := time.Now()
	err := limiter.Wait(context.Background())
	if err != nil {
		t.Errorf("expected success, got error: %v", err)
	}

	// Second should wait ~10ms
	err = limiter.Wait(context.Background())
	if err != nil {
		t.Errorf("expected success, got error: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed < 8*time.Millisecond {
		t.Errorf("expected wait of at least 8ms, got %v", elapsed)
	}
}

func TestTokenBucket_WaitContext(t *testing.T) {
	limiter := NewTokenBucket(1, 1) // 1 req/s

	// Exhaust
	limiter.Allow()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := limiter.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestSlidingWindowLimiter_Allow(t *testing.T) {
	limiter := NewSlidingWindowLimiter(5, 100*time.Millisecond)

	// Should allow up to limit
	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Errorf("expected Allow() to succeed at iteration %d", i)
		}
	}

	// Should be rate limited
	if limiter.Allow() {
		t.Error("expected Allow() to fail after limit")
	}
}

func TestSlidingWindowLimiter_Expiry(t *testing.T) {
	limiter := NewSlidingWindowLimiter(3, 50*time.Millisecond)

	// Use up limit
	for i := 0; i < 3; i++ {
		limiter.Allow()
	}

	// Wait for expiry
	time.Sleep(60 * time.Millisecond)

	// Should be able to make requests again
	if !limiter.Allow() {
		t.Error("expected Allow() to succeed after window expiry")
	}
}

func TestLeakyBucket_Allow(t *testing.T) {
	limiter := NewLeakyBucket(10, 5) // 10 leak/s, capacity of 5

	// Should allow up to capacity
	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Errorf("expected Allow() to succeed at iteration %d", i)
		}
	}

	// Should be rate limited
	if limiter.Allow() {
		t.Error("expected Allow() to fail after capacity reached")
	}
}

func TestPerKeyRateLimiter(t *testing.T) {
	limiter := NewPerTenantRateLimiter(100, 2)

	// Different keys should have independent limits
	for i := 0; i < 2; i++ {
		if !limiter.Allow("tenant-a") {
			t.Errorf("expected Allow() for tenant-a at %d", i)
		}
		if !limiter.Allow("tenant-b") {
			t.Errorf("expected Allow() for tenant-b at %d", i)
		}
	}

	// Both should be limited now
	if limiter.Allow("tenant-a") {
		t.Error("expected tenant-a to be limited")
	}
	if limiter.Allow("tenant-b") {
		t.Error("expected tenant-b to be limited")
	}
}

// ============================================================================
// Concurrent Tests
// ============================================================================

func TestCircuitBreaker_Concurrent(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		FailureThreshold: 100,
		Timeout:          1 * time.Second,
	}
	cb := NewCircuitBreaker(config)

	var wg sync.WaitGroup
	var successCount int64

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := cb.Execute(context.Background(), func(ctx context.Context) error {
				return nil
			})
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	if successCount != 100 {
		t.Errorf("expected 100 successes, got %d", successCount)
	}
}

func TestTokenBucket_Concurrent(t *testing.T) {
	limiter := NewTokenBucket(1000, 100)

	var wg sync.WaitGroup
	var allowedCount int64

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow() {
				atomic.AddInt64(&allowedCount, 1)
			}
		}()
	}

	wg.Wait()

	// Should allow approximately burst amount
	if allowedCount > 150 || allowedCount < 50 {
		t.Errorf("expected ~100 allowed, got %d", allowedCount)
	}
}
