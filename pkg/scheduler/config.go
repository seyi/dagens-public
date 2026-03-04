package scheduler

import "time"

// SchedulerConfig holds configuration options for the Scheduler
type SchedulerConfig struct {
	// AffinityTTL is how long unused affinities survive before expiration
	// Default: 1 hour
	AffinityTTL time.Duration

	// AffinityCleanupInterval is how often the cleanup goroutine runs
	// Default: 5 minutes
	AffinityCleanupInterval time.Duration

	// EnableStickiness enables/disables sticky scheduling based on PartitionKey
	// Default: true
	EnableStickiness bool

	// JobQueueSize is the buffer size for the job queue channel
	// Default: 100
	JobQueueSize int

	// DefaultWorkerMaxConcurrency is the scheduler-side default capacity used
	// for nodes that do not yet report their own capacity.
	// Default: 1
	DefaultWorkerMaxConcurrency int

	// MaxWorkerConcurrencyCap is the hard upper bound for worker-reported
	// max_concurrency. Larger values are capped to prevent a misconfigured
	// worker from attracting unsafe amounts of traffic.
	// Default: 100
	MaxWorkerConcurrencyCap int

	// CapacityTTL defines how long a worker-reported capacity snapshot remains fresh.
	// CapacityTTL should remain greater than or equal to the worker heartbeat
	// interval to avoid false-positive staleness under normal network jitter.
	// Default: 5 seconds
	CapacityTTL time.Duration

	// DispatchRejectCooldown defines how long a worker is temporarily avoided
	// after a dispatch is rejected due to a capacity-conflict style error.
	// Default: 5 seconds
	DispatchRejectCooldown time.Duration

	// MaxDispatchAttempts is the maximum number of capacity-conflict dispatch
	// attempts a task is allowed before it fails explicitly. This config is
	// added now so the retry-boundary implementation can use a stable knob.
	// Default: 3
	MaxDispatchAttempts int
}

// DefaultSchedulerConfig returns the default scheduler configuration
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		AffinityTTL:             1 * time.Hour,
		AffinityCleanupInterval: 5 * time.Minute,
		EnableStickiness:        true,
		JobQueueSize:            100,
		DefaultWorkerMaxConcurrency: 1,
		MaxWorkerConcurrencyCap: 100,
		CapacityTTL:             5 * time.Second,
		DispatchRejectCooldown:  5 * time.Second,
		MaxDispatchAttempts:     3,
	}
}

// Validate checks the configuration and applies defaults for zero values
func (c *SchedulerConfig) Validate() {
	if c.AffinityTTL <= 0 {
		c.AffinityTTL = 1 * time.Hour
	}
	if c.AffinityCleanupInterval <= 0 {
		c.AffinityCleanupInterval = 5 * time.Minute
	}
	if c.JobQueueSize <= 0 {
		c.JobQueueSize = 100
	}
	if c.DefaultWorkerMaxConcurrency <= 0 {
		c.DefaultWorkerMaxConcurrency = 1
	}
	if c.MaxWorkerConcurrencyCap <= 0 {
		c.MaxWorkerConcurrencyCap = 100
	}
	if c.CapacityTTL <= 0 {
		c.CapacityTTL = 5 * time.Second
	}
	if c.DispatchRejectCooldown <= 0 {
		c.DispatchRejectCooldown = 5 * time.Second
	}
	if c.MaxDispatchAttempts <= 0 {
		c.MaxDispatchAttempts = 3
	}
}
