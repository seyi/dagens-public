// Package resilience provides production-grade resilience patterns for distributed systems.
// This includes exponential backoff, circuit breakers, rate limiters, and retry policies.
package resilience

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"
)

// Common errors
var (
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")
	ErrContextCanceled    = errors.New("context canceled during backoff")
	ErrOperationFailed    = errors.New("operation failed after all retries")
)

// BackoffStrategy defines how to calculate backoff delays
type BackoffStrategy interface {
	// NextDelay returns the delay for the given attempt (0-indexed)
	NextDelay(attempt int) time.Duration
	// Reset resets the backoff state
	Reset()
}

// BackoffConfig configures exponential backoff behavior
type BackoffConfig struct {
	// InitialDelay is the delay for the first retry
	InitialDelay time.Duration
	// MaxDelay is the maximum delay between retries
	MaxDelay time.Duration
	// Multiplier is the factor by which delay increases
	Multiplier float64
	// JitterFactor adds randomness to delays (0.0-1.0)
	JitterFactor float64
	// MaxRetries is the maximum number of retry attempts (0 = infinite)
	MaxRetries int
}

// DefaultBackoffConfig returns sensible defaults for production use
func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0.1,
		MaxRetries:   5,
	}
}

// ExponentialBackoff implements exponential backoff with jitter
type ExponentialBackoff struct {
	config  BackoffConfig
	attempt int
	mu      sync.Mutex
	rng     *rand.Rand
}

// NewExponentialBackoff creates a new exponential backoff strategy
func NewExponentialBackoff(config BackoffConfig) *ExponentialBackoff {
	return &ExponentialBackoff{
		config:  config,
		attempt: 0,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NextDelay calculates the next backoff delay with optional jitter
func (b *ExponentialBackoff) NextDelay(attempt int) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Calculate base delay: initial * multiplier^attempt
	delay := float64(b.config.InitialDelay) * math.Pow(b.config.Multiplier, float64(attempt))

	// Cap at maximum delay
	if delay > float64(b.config.MaxDelay) {
		delay = float64(b.config.MaxDelay)
	}

	// Add jitter if configured
	if b.config.JitterFactor > 0 {
		jitter := delay * b.config.JitterFactor * (b.rng.Float64()*2 - 1) // -jitter to +jitter
		delay += jitter
	}

	// Ensure non-negative
	if delay < 0 {
		delay = 0
	}

	return time.Duration(delay)
}

// Reset resets the backoff state
func (b *ExponentialBackoff) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.attempt = 0
}

// Retrier executes operations with retry logic
type Retrier struct {
	config  BackoffConfig
	backoff *ExponentialBackoff
}

// NewRetrier creates a new retrier with the given config
func NewRetrier(config BackoffConfig) *Retrier {
	return &Retrier{
		config:  config,
		backoff: NewExponentialBackoff(config),
	}
}

// RetryFunc is a function that can be retried
type RetryFunc func(ctx context.Context) error

// RetryFuncWithResult is a function that returns a result and can be retried
type RetryFuncWithResult[T any] func(ctx context.Context) (T, error)

// IsRetryable determines if an error should trigger a retry
type IsRetryable func(error) bool

// DefaultIsRetryable retries all non-nil errors
func DefaultIsRetryable(err error) bool {
	return err != nil
}

// RetryOption configures retry behavior
type RetryOption func(*retryOptions)

type retryOptions struct {
	isRetryable IsRetryable
	onRetry     func(attempt int, err error, delay time.Duration)
}

// WithRetryableCheck sets a custom function to determine if an error is retryable
func WithRetryableCheck(fn IsRetryable) RetryOption {
	return func(o *retryOptions) {
		o.isRetryable = fn
	}
}

// WithOnRetry sets a callback invoked before each retry
func WithOnRetry(fn func(attempt int, err error, delay time.Duration)) RetryOption {
	return func(o *retryOptions) {
		o.onRetry = fn
	}
}

// Do executes the function with retries
func (r *Retrier) Do(ctx context.Context, fn RetryFunc, opts ...RetryOption) error {
	options := &retryOptions{
		isRetryable: DefaultIsRetryable,
	}
	for _, opt := range opts {
		opt(options)
	}

	var lastErr error
	for attempt := 0; r.config.MaxRetries == 0 || attempt <= r.config.MaxRetries; attempt++ {
		// Check context before attempting
		select {
		case <-ctx.Done():
			return ErrContextCanceled
		default:
		}

		// Execute the function
		err := fn(ctx)
		if err == nil {
			r.backoff.Reset()
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !options.isRetryable(err) {
			return err
		}

		// Check if we've exceeded max retries
		if r.config.MaxRetries > 0 && attempt >= r.config.MaxRetries {
			break
		}

		// Calculate delay
		delay := r.backoff.NextDelay(attempt)

		// Invoke retry callback if set
		if options.onRetry != nil {
			options.onRetry(attempt, err, delay)
		}

		// Wait with context awareness
		select {
		case <-ctx.Done():
			return ErrContextCanceled
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return &RetryError{
		Attempts: r.config.MaxRetries + 1,
		LastErr:  lastErr,
	}
}

// DoWithResult executes the function with retries and returns the result
func DoWithResult[T any](ctx context.Context, r *Retrier, fn RetryFuncWithResult[T], opts ...RetryOption) (T, error) {
	var result T
	var lastErr error

	options := &retryOptions{
		isRetryable: DefaultIsRetryable,
	}
	for _, opt := range opts {
		opt(options)
	}

	for attempt := 0; r.config.MaxRetries == 0 || attempt <= r.config.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return result, ErrContextCanceled
		default:
		}

		res, err := fn(ctx)
		if err == nil {
			r.backoff.Reset()
			return res, nil
		}

		lastErr = err

		if !options.isRetryable(err) {
			return result, err
		}

		if r.config.MaxRetries > 0 && attempt >= r.config.MaxRetries {
			break
		}

		delay := r.backoff.NextDelay(attempt)

		if options.onRetry != nil {
			options.onRetry(attempt, err, delay)
		}

		select {
		case <-ctx.Done():
			return result, ErrContextCanceled
		case <-time.After(delay):
		}
	}

	return result, &RetryError{
		Attempts: r.config.MaxRetries + 1,
		LastErr:  lastErr,
	}
}

// RetryError is returned when all retries are exhausted
type RetryError struct {
	Attempts int
	LastErr  error
}

func (e *RetryError) Error() string {
	return "max retries exceeded: " + e.LastErr.Error()
}

func (e *RetryError) Unwrap() error {
	return e.LastErr
}

// Convenience functions

// Retry executes the function with default backoff settings
func Retry(ctx context.Context, fn RetryFunc, opts ...RetryOption) error {
	return NewRetrier(DefaultBackoffConfig()).Do(ctx, fn, opts...)
}

// RetryWithConfig executes the function with custom backoff settings
func RetryWithConfig(ctx context.Context, config BackoffConfig, fn RetryFunc, opts ...RetryOption) error {
	return NewRetrier(config).Do(ctx, fn, opts...)
}

// LinearBackoff implements linear backoff (constant delay increase)
type LinearBackoff struct {
	config  BackoffConfig
	attempt int
	mu      sync.Mutex
	rng     *rand.Rand
}

// NewLinearBackoff creates a new linear backoff strategy
func NewLinearBackoff(config BackoffConfig) *LinearBackoff {
	return &LinearBackoff{
		config:  config,
		attempt: 0,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NextDelay calculates the next backoff delay linearly
func (b *LinearBackoff) NextDelay(attempt int) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Calculate delay: initial + (initial * attempt)
	delay := float64(b.config.InitialDelay) * (1 + float64(attempt))

	// Cap at maximum delay
	if delay > float64(b.config.MaxDelay) {
		delay = float64(b.config.MaxDelay)
	}

	// Add jitter if configured
	if b.config.JitterFactor > 0 {
		jitter := delay * b.config.JitterFactor * (b.rng.Float64()*2 - 1)
		delay += jitter
	}

	if delay < 0 {
		delay = 0
	}

	return time.Duration(delay)
}

// Reset resets the backoff state
func (b *LinearBackoff) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.attempt = 0
}

// ConstantBackoff implements constant delay between retries
type ConstantBackoff struct {
	delay        time.Duration
	jitterFactor float64
	rng          *rand.Rand
	mu           sync.Mutex
}

// NewConstantBackoff creates a new constant backoff strategy
func NewConstantBackoff(delay time.Duration, jitterFactor float64) *ConstantBackoff {
	return &ConstantBackoff{
		delay:        delay,
		jitterFactor: jitterFactor,
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NextDelay returns the constant delay with optional jitter
func (b *ConstantBackoff) NextDelay(attempt int) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	delay := float64(b.delay)

	if b.jitterFactor > 0 {
		jitter := delay * b.jitterFactor * (b.rng.Float64()*2 - 1)
		delay += jitter
	}

	if delay < 0 {
		delay = 0
	}

	return time.Duration(delay)
}

// Reset is a no-op for constant backoff
func (b *ConstantBackoff) Reset() {}
