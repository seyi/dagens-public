package agentic

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/seyi/dagens/pkg/resilience"
)

// HealingAction represents what the system should do when issues are detected.
// The framework provides basic actions; agents decide when to trigger them.
type HealingAction int

const (
	// HealingNone means no action is needed.
	HealingNone HealingAction = iota

	// HealingRetry means retry the current operation.
	HealingRetry

	// HealingRestart means reset agent state and try fresh.
	HealingRestart

	// HealingDegrade means reduce scope/capabilities gracefully.
	HealingDegrade

	// HealingEscalate means alert a human supervisor.
	HealingEscalate
)

func (h HealingAction) String() string {
	return [...]string{"none", "retry", "restart", "degrade", "escalate"}[h]
}

// SelfHealingConfig configures self-healing behavior.
type SelfHealingConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

// DefaultSelfHealingConfig returns sensible defaults.
func DefaultSelfHealingConfig() SelfHealingConfig {
	return SelfHealingConfig{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		BackoffFactor:  2.0,
	}
}

// SelfHealingOption is a functional option for configuring SelfHealing.
type SelfHealingOption func(*SelfHealing)

// WithCircuitBreaker sets a circuit breaker instance directly.
func WithCircuitBreaker(cb *resilience.CircuitBreaker) SelfHealingOption {
	return func(sh *SelfHealing) {
		sh.circuitBreaker = cb
	}
}

// WithCircuitBreakerName retrieves a circuit breaker from the global registry by name.
func WithCircuitBreakerName(name string) SelfHealingOption {
	return func(sh *SelfHealing) {
		if name != "" {
			sh.circuitBreaker = resilience.GetCircuitBreaker(name)
		}
	}
}

// SelfHealing provides retry-with-backoff functionality for agents.
type SelfHealing struct {
	config         SelfHealingConfig
	currentRetry   int
	lastFailure    time.Time
	lastError      error
	monitoring     *MonitoringContext
	circuitBreaker *resilience.CircuitBreaker
}

// NewSelfHealing creates a new self-healing instance.
// Optional SelfHealingOption functions can be provided to configure circuit breaker integration.
func NewSelfHealing(config SelfHealingConfig, monitoring *MonitoringContext, opts ...SelfHealingOption) *SelfHealing {
	sh := &SelfHealing{
		config:     config,
		monitoring: monitoring,
	}

	// Apply functional options
	for _, opt := range opts {
		opt(sh)
	}

	return sh
}

// ExecuteWithHealing executes a function with automatic retry on failure.
// If a circuit breaker is configured, each attempt is wrapped with the circuit breaker.
// When the circuit breaker is open, the retry loop aborts immediately with ErrCircuitOpen.
func (sh *SelfHealing) ExecuteWithHealing(ctx context.Context, fn func(ctx context.Context) error) error {
	sh.currentRetry = 0

	for sh.currentRetry <= sh.config.MaxRetries {
		var err error

		// Wrap the execution with circuit breaker if configured
		if sh.circuitBreaker != nil {
			err = sh.circuitBreaker.Execute(ctx, func(c context.Context) error {
				return fn(c)
			})

			// If circuit breaker is open, abort retry loop immediately
			if err == resilience.ErrCircuitOpen {
				if sh.monitoring != nil {
					sh.monitoring.RecordHealth(HealthFailing)
					sh.monitoring.LogWarn("circuit breaker is open, aborting retries", map[string]interface{}{
						"retry": sh.currentRetry,
						"max":   sh.config.MaxRetries,
						"error": err.Error(),
					})
				}
				sh.lastError = err
				return fmt.Errorf("circuit breaker open, aborting execution: %w", err)
			}
		} else {
			// No circuit breaker, execute directly
			err = fn(ctx)
		}

		if err == nil {
			// Success - record healthy status
			if sh.monitoring != nil {
				sh.monitoring.RecordHealth(HealthOK)
			}
			return nil
		}

		// Record failure
		sh.currentRetry++
		sh.lastFailure = time.Now()
		sh.lastError = err

		if sh.monitoring != nil {
			sh.monitoring.RecordHealth(HealthDegraded)
			sh.monitoring.LogWarn("execution failed, attempting retry", map[string]interface{}{
				"retry": sh.currentRetry,
				"max":   sh.config.MaxRetries,
				"error": err.Error(),
			})
		}

		// Check if we've exceeded max retries
		if sh.currentRetry > sh.config.MaxRetries {
			if sh.monitoring != nil {
				sh.monitoring.RecordHealth(HealthFailing)
			}
			return fmt.Errorf("max retries (%d) exceeded: %w", sh.config.MaxRetries, err)
		}

		// Calculate backoff with exponential increase
		backoff := sh.calculateBackoff()

		// Wait for backoff or context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			// Continue to next retry
		}
	}

	return sh.lastError
}

// calculateBackoff returns the backoff duration for the current retry.
func (sh *SelfHealing) calculateBackoff() time.Duration {
	backoff := float64(sh.config.InitialBackoff) * math.Pow(sh.config.BackoffFactor, float64(sh.currentRetry-1))
	if backoff > float64(sh.config.MaxBackoff) {
		backoff = float64(sh.config.MaxBackoff)
	}
	return time.Duration(backoff)
}

// CurrentRetry returns the current retry count.
func (sh *SelfHealing) CurrentRetry() int {
	return sh.currentRetry
}

// LastError returns the last error encountered.
func (sh *SelfHealing) LastError() error {
	return sh.lastError
}

// Reset resets the self-healing state for a new execution.
func (sh *SelfHealing) Reset() {
	sh.currentRetry = 0
	sh.lastError = nil
}

// CircuitBreaker returns the configured circuit breaker, or nil if none is set.
func (sh *SelfHealing) CircuitBreaker() *resilience.CircuitBreaker {
	return sh.circuitBreaker
}