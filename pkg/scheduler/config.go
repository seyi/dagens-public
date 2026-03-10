package scheduler

import "time"

// SchedulerConfig holds configuration options for the Scheduler.
//
// The config is copied into the scheduler at construction time. Mutating a
// SchedulerConfig after calling NewSchedulerWithConfig has no effect.
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
	// Recommended upper bound: ~10,000 for most deployments. Larger values can
	// materially increase memory pressure under sustained admission load.
	// Default: 100
	JobQueueSize int

	// DefaultWorkerMaxConcurrency is the scheduler-side default capacity used
	// for nodes that do not yet report their own capacity.
	// Default: 1
	DefaultWorkerMaxConcurrency int

	// MaxWorkerConcurrencyCap is the hard upper bound for worker-reported
	// max_concurrency. Larger values are capped to prevent a misconfigured
	// worker from attracting unsafe amounts of traffic.
	// Recommended upper bound: ~500 unless capacity planning explicitly justifies more.
	// Default: 100
	MaxWorkerConcurrencyCap int

	// CapacityTTL defines how long a worker-reported capacity snapshot remains fresh.
	// CapacityTTL should remain greater than or equal to the worker heartbeat
	// interval to avoid false-positive staleness under normal network jitter.
	// A practical baseline is CapacityTTL >= 2x expected heartbeat interval.
	// Default: 5 seconds
	CapacityTTL time.Duration

	// DispatchRejectCooldown defines how long a worker is temporarily avoided
	// after a dispatch is rejected due to a capacity-conflict style error.
	// Default: 5 seconds
	DispatchRejectCooldown time.Duration

	// MaxDispatchAttempts is the maximum number of capacity-conflict dispatch
	// attempts a task is allowed before it fails explicitly. This config is
	// added now so the retry-boundary implementation can use a stable knob.
	// Recommended upper bound: ~10 to avoid prolonged retry churn under saturation.
	// Default: 3
	MaxDispatchAttempts int

	// EnableStageCapacityDeferral enables stage-level capacity waiting before
	// failing with ErrNoWorkerCapacity.
	// Default: false
	EnableStageCapacityDeferral bool

	// StageCapacityDeferralTimeout bounds how long stage selection may wait for
	// capacity before returning ErrNoWorkerCapacity.
	// Default: 2 seconds
	StageCapacityDeferralTimeout time.Duration

	// StageCapacityDeferralPollInterval controls how often capacity is rechecked
	// while deferring stage selection.
	// Default: 100 milliseconds
	StageCapacityDeferralPollInterval time.Duration

	// RecoveryTimeout bounds scheduler startup recovery duration before context
	// cancellation.
	// Default: 5 minutes
	RecoveryTimeout time.Duration

	// EnableResumeRecoveredQueuedJobs enables startup replay to re-enqueue jobs
	// recovered in QUEUED lifecycle state.
	//
	// Safety boundary:
	// - only QUEUED jobs are resumed
	// - RUNNING and DISPATCHED work remains visibility-only (no auto-redispatch)
	//
	// Default: false
	EnableResumeRecoveredQueuedJobs bool

	// LeadershipRetryInterval controls how long the run loop waits before
	// retrying leadership checks when this instance is not the current leader.
	// Default: 200 milliseconds
	LeadershipRetryInterval time.Duration
}

// DefaultSchedulerConfig returns the default scheduler configuration
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		AffinityTTL:                       1 * time.Hour,
		AffinityCleanupInterval:           5 * time.Minute,
		EnableStickiness:                  true,
		JobQueueSize:                      100,
		DefaultWorkerMaxConcurrency:       1,
		MaxWorkerConcurrencyCap:           100,
		CapacityTTL:                       5 * time.Second,
		DispatchRejectCooldown:            5 * time.Second,
		MaxDispatchAttempts:               3,
		EnableStageCapacityDeferral:       false,
		StageCapacityDeferralTimeout:      2 * time.Second,
		StageCapacityDeferralPollInterval: 100 * time.Millisecond,
		RecoveryTimeout:                   5 * time.Minute,
		EnableResumeRecoveredQueuedJobs:   false,
		LeadershipRetryInterval:           200 * time.Millisecond,
	}
}

// Validate checks the configuration and applies defaults for zero values.
// Validate mutates the receiver and is not thread-safe; call before sharing
// config across goroutines.
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
	if c.StageCapacityDeferralTimeout <= 0 {
		c.StageCapacityDeferralTimeout = 2 * time.Second
	}
	if c.StageCapacityDeferralPollInterval <= 0 {
		c.StageCapacityDeferralPollInterval = 100 * time.Millisecond
	}
	if c.RecoveryTimeout <= 0 {
		c.RecoveryTimeout = 5 * time.Minute
	}
	if c.LeadershipRetryInterval <= 0 {
		c.LeadershipRetryInterval = 200 * time.Millisecond
	}

	// Keep cleanup cadence comfortably below expiry to avoid stale affinity
	// entries persisting for long windows.
	if c.AffinityCleanupInterval >= c.AffinityTTL {
		c.AffinityCleanupInterval = c.AffinityTTL / 4
		if c.AffinityCleanupInterval <= 0 {
			c.AffinityCleanupInterval = time.Second
		}
	}

	// Ensure deferred-capacity polling has at least one practical interval
	// before timeout.
	if c.StageCapacityDeferralPollInterval >= c.StageCapacityDeferralTimeout {
		c.StageCapacityDeferralPollInterval = c.StageCapacityDeferralTimeout / 4
		if c.StageCapacityDeferralPollInterval <= 0 {
			c.StageCapacityDeferralPollInterval = 10 * time.Millisecond
		}
	}
}
