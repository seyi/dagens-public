package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// HealthStatus represents the overall health status
type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusDegraded  HealthStatus = "degraded"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

// ComponentHealth represents the health of a single component
type ComponentHealth struct {
	Name      string            `json:"name"`
	Status    HealthStatus      `json:"status"`
	Message   string            `json:"message,omitempty"`
	Latency   time.Duration     `json:"latency_ms,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CheckedAt time.Time         `json:"checked_at"`
}

// HealthResponse is the response for health endpoints
type HealthResponse struct {
	Status     HealthStatus       `json:"status"`
	Version    string             `json:"version,omitempty"`
	Uptime     time.Duration      `json:"uptime_seconds,omitempty"`
	Components []ComponentHealth  `json:"components,omitempty"`
	Timestamp  time.Time          `json:"timestamp"`
}

// HealthChecker is a function that checks component health
type HealthChecker func(ctx context.Context) ComponentHealth

// HealthRegistry manages health checks for all components
type HealthRegistry struct {
	checkers  map[string]HealthChecker
	version   string
	startTime time.Time
	timeout   time.Duration
	mu        sync.RWMutex
}

// NewHealthRegistry creates a new health registry
func NewHealthRegistry(version string, checkTimeout time.Duration) *HealthRegistry {
	return &HealthRegistry{
		checkers:  make(map[string]HealthChecker),
		version:   version,
		startTime: time.Now(),
		timeout:   checkTimeout,
	}
}

// Register adds a health checker
func (h *HealthRegistry) Register(name string, checker HealthChecker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers[name] = checker
}

// Unregister removes a health checker
func (h *HealthRegistry) Unregister(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.checkers, name)
}

// Check runs all health checks and returns the overall status
func (h *HealthRegistry) Check(ctx context.Context) HealthResponse {
	h.mu.RLock()
	checkers := make(map[string]HealthChecker, len(h.checkers))
	for k, v := range h.checkers {
		checkers[k] = v
	}
	h.mu.RUnlock()

	// Run all checks concurrently
	results := make(chan ComponentHealth, len(checkers))
	var wg sync.WaitGroup

	checkCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	for name, checker := range checkers {
		wg.Add(1)
		go func(name string, checker HealthChecker) {
			defer wg.Done()

			start := time.Now()
			result := checker(checkCtx)
			result.Name = name
			result.Latency = time.Since(start)
			result.CheckedAt = time.Now()

			results <- result
		}(name, checker)
	}

	// Wait for all checks to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	components := make([]ComponentHealth, 0, len(checkers))
	overallStatus := HealthStatusHealthy

	for result := range results {
		components = append(components, result)

		// Determine overall status
		switch result.Status {
		case HealthStatusUnhealthy:
			overallStatus = HealthStatusUnhealthy
		case HealthStatusDegraded:
			if overallStatus != HealthStatusUnhealthy {
				overallStatus = HealthStatusDegraded
			}
		}
	}

	return HealthResponse{
		Status:     overallStatus,
		Version:    h.version,
		Uptime:     time.Since(h.startTime),
		Components: components,
		Timestamp:  time.Now(),
	}
}

// LivenessCheck returns a simple liveness check (is the service running?)
func (h *HealthRegistry) LivenessCheck() HealthResponse {
	return HealthResponse{
		Status:    HealthStatusHealthy,
		Version:   h.version,
		Uptime:    time.Since(h.startTime),
		Timestamp: time.Now(),
	}
}

// ReadinessCheck returns whether the service is ready to accept traffic
func (h *HealthRegistry) ReadinessCheck(ctx context.Context) HealthResponse {
	return h.Check(ctx)
}

// Handler returns HTTP handlers for health endpoints
func (h *HealthRegistry) Handler() http.Handler {
	mux := http.NewServeMux()

	// /health - Full health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		response := h.Check(ctx)

		w.Header().Set("Content-Type", "application/json")

		switch response.Status {
		case HealthStatusHealthy:
			w.WriteHeader(http.StatusOK)
		case HealthStatusDegraded:
			w.WriteHeader(http.StatusOK)
		case HealthStatusUnhealthy:
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		json.NewEncoder(w).Encode(response)
	})

	// /health/live - Kubernetes liveness probe
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		response := h.LivenessCheck()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	})

	// /health/ready - Kubernetes readiness probe
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		response := h.ReadinessCheck(ctx)

		w.Header().Set("Content-Type", "application/json")

		if response.Status == HealthStatusUnhealthy {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		json.NewEncoder(w).Encode(response)
	})

	return mux
}

// Common health checkers

// RedisHealthChecker creates a health checker for Redis
func RedisHealthChecker(pingFn func(ctx context.Context) error) HealthChecker {
	return func(ctx context.Context) ComponentHealth {
		start := time.Now()
		err := pingFn(ctx)

		if err != nil {
			return ComponentHealth{
				Status:  HealthStatusUnhealthy,
				Message: err.Error(),
				Latency: time.Since(start),
			}
		}

		latency := time.Since(start)
		status := HealthStatusHealthy

		// Degrade if latency is high
		if latency > 100*time.Millisecond {
			status = HealthStatusDegraded
		}

		return ComponentHealth{
			Status:  status,
			Latency: latency,
		}
	}
}

// DatabaseHealthChecker creates a health checker for databases
func DatabaseHealthChecker(pingFn func(ctx context.Context) error) HealthChecker {
	return RedisHealthChecker(pingFn) // Same logic
}

// LLMProviderHealthChecker creates a health checker for LLM providers
func LLMProviderHealthChecker(name string, checkFn func(ctx context.Context) error) HealthChecker {
	return func(ctx context.Context) ComponentHealth {
		start := time.Now()
		err := checkFn(ctx)

		if err != nil {
			return ComponentHealth{
				Status:  HealthStatusDegraded, // LLM failure is degraded, not unhealthy
				Message: err.Error(),
				Latency: time.Since(start),
				Metadata: map[string]string{
					"provider": name,
				},
			}
		}

		return ComponentHealth{
			Status:  HealthStatusHealthy,
			Latency: time.Since(start),
			Metadata: map[string]string{
				"provider": name,
			},
		}
	}
}

// DiskSpaceHealthChecker creates a health checker for disk space
func DiskSpaceHealthChecker(path string, minFreeBytes uint64) HealthChecker {
	return func(ctx context.Context) ComponentHealth {
		// Simplified - in production, use syscall to get disk stats
		return ComponentHealth{
			Status: HealthStatusHealthy,
			Metadata: map[string]string{
				"path": path,
			},
		}
	}
}

// MemoryHealthChecker creates a health checker for memory usage
func MemoryHealthChecker(maxUsagePercent float64) HealthChecker {
	return func(ctx context.Context) ComponentHealth {
		// Simplified - in production, use runtime.MemStats
		return ComponentHealth{
			Status: HealthStatusHealthy,
		}
	}
}

// Global health registry
var globalHealthRegistry *HealthRegistry
var healthOnce sync.Once

// GetHealthRegistry returns the global health registry
func GetHealthRegistry() *HealthRegistry {
	healthOnce.Do(func() {
		globalHealthRegistry = NewHealthRegistry("0.1.0", 10*time.Second)
	})
	return globalHealthRegistry
}

// RegisterHealthCheck registers a health check with the global registry
func RegisterHealthCheck(name string, checker HealthChecker) {
	GetHealthRegistry().Register(name, checker)
}

// UnregisterHealthCheck removes a health check from the global registry
func UnregisterHealthCheck(name string) {
	GetHealthRegistry().Unregister(name)
}

// HealthHandler returns the global health HTTP handler
func HealthHandler() http.Handler {
	return GetHealthRegistry().Handler()
}
