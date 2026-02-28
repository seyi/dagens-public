package resilience

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/telemetry"
)

// Circuit breaker errors
var (
	ErrCircuitOpen     = errors.New("circuit breaker is open")
	ErrCircuitTimeout  = errors.New("circuit breaker timeout")
	ErrTooManyRequests = errors.New("too many requests in half-open state")
)

// Telemetry attribute keys
const (
	AttrCBName  = "cb.name"
	AttrCBFrom  = "cb.state.from"
	AttrCBTo    = "cb.state.to"
	AttrCBError = "cb.error"
)

// Telemetry metric names
const (
	MetricCBStateTransitions = "cb.state_transitions_total"
	MetricCBExecutions       = "cb.executions_total"
	MetricCBDuration        = "cb.execution_duration_seconds"
)

// CircuitState represents the state of a circuit breaker
type CircuitState int

const (
	// StateClosed means the circuit is closed and requests flow normally
	StateClosed CircuitState = iota
	// StateOpen means the circuit is open and requests are rejected
	StateOpen
	// StateHalfOpen means the circuit is testing if the service recovered
	StateHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreakerConfig configures circuit breaker behavior
type CircuitBreakerConfig struct {
	// Name identifies this circuit breaker
	Name string

	// FailureThreshold is the number of failures before opening the circuit
	FailureThreshold int64

	// SuccessThreshold is the number of successes in half-open before closing
	SuccessThreshold int64

	// Timeout is how long to wait before transitioning from open to half-open
	Timeout time.Duration

	// HalfOpenMaxRequests is the max requests allowed in half-open state
	HalfOpenMaxRequests int64

	// OnStateChange is called when the circuit state changes
	OnStateChange func(name string, from, to CircuitState)

	// IsFailure determines if an error should count as a failure
	IsFailure func(error) bool
}

// DefaultCircuitBreakerConfig returns sensible defaults
func DefaultCircuitBreakerConfig(name string) CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Name:                name,
		FailureThreshold:    5,
		SuccessThreshold:    2,
		Timeout:             30 * time.Second,
		HalfOpenMaxRequests: 3,
		IsFailure:           func(err error) bool { return err != nil },
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	config CircuitBreakerConfig

	state           CircuitState
	failureCount    int64
	successCount    int64
	halfOpenCount   int64
	lastFailureTime time.Time

	mu sync.RWMutex
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	if config.IsFailure == nil {
		config.IsFailure = func(err error) bool { return err != nil }
	}

	return &CircuitBreaker{
		config: config,
		state:  StateClosed,
	}
}

// State returns the current circuit state
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	// Check if we should transition from open to half-open
	if cb.state == StateOpen {
		if time.Since(cb.lastFailureTime) > cb.config.Timeout {
			return StateHalfOpen
		}
	}

	return cb.state
}

// Counts returns the current failure and success counts
func (cb *CircuitBreaker) Counts() (failures, successes int64) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failureCount, cb.successCount
}

// Execute runs the function through the circuit breaker
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func(context.Context) error) error {
	collector := telemetry.GetGlobalTelemetry()
	collector.GetMeter().Counter(MetricCBExecutions).Add(1)

	// Check if we can proceed
	if err := cb.beforeRequest(); err != nil {
		return err
	}

	// Execute the function
	start := time.Now()
	err := fn(ctx)
	duration := time.Since(start)

	// Record metric
	collector.GetMeter().Histogram(MetricCBDuration).Record(duration.Seconds())

	// Record the result
	cb.afterRequest(err, duration)

	return err
}

// ExecuteWithResult runs a function that returns a result through the circuit breaker
func ExecuteWithResult[T any](ctx context.Context, cb *CircuitBreaker, fn func(context.Context) (T, error)) (T, error) {
	var zero T

	if err := cb.beforeRequest(); err != nil {
		return zero, err
	}

	start := time.Now()
	result, err := fn(ctx)
	duration := time.Since(start)

	cb.afterRequest(err, duration)

	return result, err
}

// beforeRequest checks if the request can proceed
func (cb *CircuitBreaker) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if timeout has passed
		if time.Since(cb.lastFailureTime) > cb.config.Timeout {
			// Transition to half-open
			cb.toHalfOpen()
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		// Limit requests in half-open state
		if cb.halfOpenCount >= cb.config.HalfOpenMaxRequests {
			return ErrTooManyRequests
		}
		atomic.AddInt64(&cb.halfOpenCount, 1)
		return nil
	}

	return nil
}

// afterRequest records the result of a request
func (cb *CircuitBreaker) afterRequest(err error, duration time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	isFailure := cb.config.IsFailure(err)

	switch cb.state {
	case StateClosed:
		if isFailure {
			cb.failureCount++
			cb.lastFailureTime = time.Now()

			if cb.failureCount >= cb.config.FailureThreshold {
				cb.toOpen()
			}
		} else {
			// Reset failure count on success
			cb.failureCount = 0
		}

	case StateHalfOpen:
		if isFailure {
			// Failure in half-open state -> back to open
			cb.toOpen()
		} else {
			cb.successCount++

			if cb.successCount >= cb.config.SuccessThreshold {
				cb.toClosed()
			}
		}
	}
}

func (cb *CircuitBreaker) recordStateTransition(from, to CircuitState) {
	collector := telemetry.GetGlobalTelemetry()
	
	// Record metric
	collector.GetMeter().Counter(MetricCBStateTransitions).Add(1)
	
	// Record log
	collector.GetLogger().Info("circuit breaker state changed", map[string]interface{}{
		AttrCBName: cb.config.Name,
		AttrCBFrom: from.String(),
		AttrCBTo:   to.String(),
	})
}

// State transitions
func (cb *CircuitBreaker) toOpen() {
	from := cb.state
	cb.state = StateOpen
	cb.lastFailureTime = time.Now()
	cb.halfOpenCount = 0
	cb.successCount = 0

	cb.recordStateTransition(from, StateOpen)

	if cb.config.OnStateChange != nil {
		cb.config.OnStateChange(cb.config.Name, from, StateOpen)
	}
}

func (cb *CircuitBreaker) toHalfOpen() {
	from := cb.state
	cb.state = StateHalfOpen
	cb.halfOpenCount = 0
	cb.successCount = 0
	cb.failureCount = 0

	cb.recordStateTransition(from, StateHalfOpen)

	if cb.config.OnStateChange != nil {
		cb.config.OnStateChange(cb.config.Name, from, StateHalfOpen)
	}
}

func (cb *CircuitBreaker) toClosed() {
	from := cb.state
	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.halfOpenCount = 0

	cb.recordStateTransition(from, StateClosed)

	if cb.config.OnStateChange != nil {
		cb.config.OnStateChange(cb.config.Name, from, StateClosed)
	}
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.halfOpenCount = 0
}

// CircuitBreakerRegistry manages multiple circuit breakers
type CircuitBreakerRegistry struct {
	breakers map[string]*CircuitBreaker
	mu       sync.RWMutex
}

// NewCircuitBreakerRegistry creates a new registry
func NewCircuitBreakerRegistry() *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// Get returns or creates a circuit breaker for the given name
func (r *CircuitBreakerRegistry) Get(name string) *CircuitBreaker {
	r.mu.RLock()
	cb, exists := r.breakers[name]
	r.mu.RUnlock()

	if exists {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists = r.breakers[name]; exists {
		return cb
	}

	cb = NewCircuitBreaker(DefaultCircuitBreakerConfig(name))
	r.breakers[name] = cb
	return cb
}

// GetWithConfig returns or creates a circuit breaker with custom config
func (r *CircuitBreakerRegistry) GetWithConfig(config CircuitBreakerConfig) *CircuitBreaker {
	r.mu.RLock()
	cb, exists := r.breakers[config.Name]
	r.mu.RUnlock()

	if exists {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if cb, exists = r.breakers[config.Name]; exists {
		return cb
	}

	cb = NewCircuitBreaker(config)
	r.breakers[config.Name] = cb
	return cb
}

// All returns all circuit breakers in the registry
func (r *CircuitBreakerRegistry) All() map[string]*CircuitBreaker {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*CircuitBreaker, len(r.breakers))
	for k, v := range r.breakers {
		result[k] = v
	}
	return result
}

// Stats returns statistics for all circuit breakers
func (r *CircuitBreakerRegistry) Stats() map[string]CircuitBreakerStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := make(map[string]CircuitBreakerStats, len(r.breakers))
	for name, cb := range r.breakers {
		failures, successes := cb.Counts()
		stats[name] = CircuitBreakerStats{
			Name:         name,
			State:        cb.State().String(),
			FailureCount: failures,
			SuccessCount: successes,
		}
	}
	return stats
}

// CircuitBreakerStats holds statistics for a circuit breaker
type CircuitBreakerStats struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	FailureCount int64  `json:"failure_count"`
	SuccessCount int64  `json:"success_count"`
}

// Global registry
var globalRegistry = NewCircuitBreakerRegistry()

// GetCircuitBreaker returns a circuit breaker from the global registry
func GetCircuitBreaker(name string) *CircuitBreaker {
	return globalRegistry.Get(name)
}

// GetCircuitBreakerWithConfig returns a circuit breaker with custom config from the global registry
func GetCircuitBreakerWithConfig(config CircuitBreakerConfig) *CircuitBreaker {
	return globalRegistry.GetWithConfig(config)
}

// GetAllCircuitBreakers returns all circuit breakers from the global registry
func GetAllCircuitBreakers() map[string]*CircuitBreaker {
	return globalRegistry.All()
}

// GetCircuitBreakerStats returns stats for all circuit breakers
func GetCircuitBreakerStats() map[string]CircuitBreakerStats {
	return globalRegistry.Stats()
}
