package scheduler

import "time"

const (
	defaultAffinityTTL                  = 1 * time.Hour
	defaultAffinityCleanupInterval      = 5 * time.Minute
	defaultJobQueueSize                 = 100
	defaultWorkerMaxConcurrency         = 1
	defaultMaxWorkerConcurrencyCap      = 100
	defaultCapacityTTL                  = 5 * time.Second
	defaultDispatchRejectCooldown       = 5 * time.Second
	defaultMaxDispatchAttempts          = 3
	defaultStageCapacityDeferralTimeout = 2 * time.Second
	defaultStageCapacityDeferralPoll    = 100 * time.Millisecond
	defaultRecoveryTimeout              = 5 * time.Minute
	defaultRecoveryBatchSize            = 128
	defaultLeadershipRetryInterval      = 200 * time.Millisecond
	defaultJobRetentionTTL              = 24 * time.Hour
	defaultJobRetentionCleanupInterval  = 5 * time.Minute
	defaultLeaderRequeuePollInterval    = 500 * time.Millisecond
	defaultFollowerRequeuePollInterval  = 5 * time.Second
	defaultReconcileTimeout             = 5 * time.Second
	defaultAlertRequestTimeout          = 3 * time.Second
	defaultAlertMaxAttempts             = 3
	defaultAlertRetryBaseInterval       = 200 * time.Millisecond

	maxJobQueueSize            = 10000
	maxRecoveryBatchSize       = 4096
	maxDispatchAttempts        = 50
	maxWorkerConcurrencyCapCfg = 10000

	minAffinityCleanupInterval = 100 * time.Millisecond
	minDeferralPollInterval    = 10 * time.Millisecond
	minRetentionCleanup        = 1 * time.Second
	minRecoveryTimeout         = 1 * time.Second
	minAlertRetryBaseInterval  = 50 * time.Millisecond
	minRequeuePollInterval     = 10 * time.Millisecond
	minReconcileTimeout        = 100 * time.Millisecond
)

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

	// RecoveryBatchSize controls how many replayed jobs are applied per locked
	// recovery batch before yielding lock scope.
	// Default: 128
	RecoveryBatchSize int

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

	// JobRetentionTTL controls how long terminal jobs are kept in memory before
	// background cleanup prunes them.
	// Default: 24 hours
	JobRetentionTTL time.Duration

	// JobRetentionCleanupInterval controls how frequently terminal-job cleanup
	// scans run.
	// Default: 5 minutes
	JobRetentionCleanupInterval time.Duration

	// EnableJobRetentionCleanup enables background pruning for terminal jobs.
	// Default: true
	EnableJobRetentionCleanup bool

	// EnableLeaderDurableRequeueLoop enables leader-only reconciliation of
	// durable QUEUED jobs into the in-memory scheduler queue.
	//
	// This hardens takeover behavior: after leadership changes, the new leader
	// can repopulate schedulable queued work from durable state.
	//
	// Default: true
	EnableLeaderDurableRequeueLoop bool

	// LeaderDurableRequeuePollInterval controls how frequently the leader
	// reconciler polls durable state for QUEUED jobs to enqueue.
	// Default: 500 milliseconds
	LeaderDurableRequeuePollInterval time.Duration

	// EnableFollowerDurableRequeueLoop enables follower-only reconciliation of
	// durable transitions into the in-memory visibility state (warm replay).
	//
	// This reduces promotion time during leadership failover by ensuring the
	// new leader's in-memory view is already synchronized with durable state.
	//
	// Default: true
	EnableFollowerDurableRequeueLoop bool

	// FollowerDurableRequeuePollInterval controls how frequently the follower
	// synchronizes durable state for warm replay.
	// Default: 5 seconds
	FollowerDurableRequeuePollInterval time.Duration

	// ReconcileTimeout bounds a single durable reconcile cycle, including
	// listing unfinished jobs and replaying their durable state.
	// Default: 5 seconds
	ReconcileTimeout time.Duration

	// AlertWebhookURL is an optional operator notification endpoint for
	// scheduler critical failures (for example recovery/leadership startup).
	// Empty disables webhook alert emission.
	AlertWebhookURL string

	// AlertRequestTimeout bounds webhook alert send time.
	// Default: 3 seconds
	AlertRequestTimeout time.Duration

	// AlertMaxAttempts controls the maximum webhook delivery attempts per alert.
	// Default: 3
	AlertMaxAttempts int

	// AlertRetryBaseInterval controls the base retry delay for alert delivery.
	// Delay grows exponentially (base * 2^(attempt-1)).
	// Default: 200 milliseconds
	AlertRetryBaseInterval time.Duration
}

// DefaultSchedulerConfig returns the default scheduler configuration
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		AffinityTTL:                        defaultAffinityTTL,
		AffinityCleanupInterval:            defaultAffinityCleanupInterval,
		EnableStickiness:                   true,
		JobQueueSize:                       defaultJobQueueSize,
		DefaultWorkerMaxConcurrency:        defaultWorkerMaxConcurrency,
		MaxWorkerConcurrencyCap:            defaultMaxWorkerConcurrencyCap,
		CapacityTTL:                        defaultCapacityTTL,
		DispatchRejectCooldown:             defaultDispatchRejectCooldown,
		MaxDispatchAttempts:                defaultMaxDispatchAttempts,
		EnableStageCapacityDeferral:        false,
		StageCapacityDeferralTimeout:       defaultStageCapacityDeferralTimeout,
		StageCapacityDeferralPollInterval:  defaultStageCapacityDeferralPoll,
		RecoveryTimeout:                    defaultRecoveryTimeout,
		RecoveryBatchSize:                  defaultRecoveryBatchSize,
		EnableResumeRecoveredQueuedJobs:    false,
		LeadershipRetryInterval:            defaultLeadershipRetryInterval,
		JobRetentionTTL:                    defaultJobRetentionTTL,
		JobRetentionCleanupInterval:        defaultJobRetentionCleanupInterval,
		EnableJobRetentionCleanup:          true,
		EnableLeaderDurableRequeueLoop:     true,
		LeaderDurableRequeuePollInterval:   defaultLeaderRequeuePollInterval,
		EnableFollowerDurableRequeueLoop:   true,
		FollowerDurableRequeuePollInterval: defaultFollowerRequeuePollInterval,
		ReconcileTimeout:                   defaultReconcileTimeout,
		AlertRequestTimeout:                defaultAlertRequestTimeout,
		AlertMaxAttempts:                   defaultAlertMaxAttempts,
		AlertRetryBaseInterval:             defaultAlertRetryBaseInterval,
	}
}

// Validate checks the configuration and applies defaults for numeric/time fields.
//
// Boolean fields are intentionally not auto-defaulted here; they retain the
// zero-value unless callers set them explicitly. For production defaults,
// prefer DefaultSchedulerConfig() and override specific fields.
// Validate mutates the receiver and is not thread-safe; call before sharing
// config across goroutines.
func (c *SchedulerConfig) Validate() {
	if c.AffinityTTL <= 0 {
		c.AffinityTTL = defaultAffinityTTL
	}
	if c.AffinityCleanupInterval <= 0 {
		c.AffinityCleanupInterval = defaultAffinityCleanupInterval
	}
	if c.JobQueueSize <= 0 {
		c.JobQueueSize = defaultJobQueueSize
	} else if c.JobQueueSize > maxJobQueueSize {
		c.JobQueueSize = maxJobQueueSize
	}
	if c.DefaultWorkerMaxConcurrency <= 0 {
		c.DefaultWorkerMaxConcurrency = defaultWorkerMaxConcurrency
	}
	if c.MaxWorkerConcurrencyCap <= 0 {
		c.MaxWorkerConcurrencyCap = defaultMaxWorkerConcurrencyCap
	} else if c.MaxWorkerConcurrencyCap > maxWorkerConcurrencyCapCfg {
		c.MaxWorkerConcurrencyCap = maxWorkerConcurrencyCapCfg
	}
	if c.CapacityTTL <= 0 {
		c.CapacityTTL = defaultCapacityTTL
	}
	if c.DispatchRejectCooldown <= 0 {
		c.DispatchRejectCooldown = defaultDispatchRejectCooldown
	}
	if c.MaxDispatchAttempts <= 0 {
		c.MaxDispatchAttempts = defaultMaxDispatchAttempts
	} else if c.MaxDispatchAttempts > maxDispatchAttempts {
		c.MaxDispatchAttempts = maxDispatchAttempts
	}
	if c.StageCapacityDeferralTimeout <= 0 {
		c.StageCapacityDeferralTimeout = defaultStageCapacityDeferralTimeout
	}
	if c.StageCapacityDeferralPollInterval <= 0 {
		c.StageCapacityDeferralPollInterval = defaultStageCapacityDeferralPoll
	}
	if c.RecoveryTimeout <= 0 {
		c.RecoveryTimeout = defaultRecoveryTimeout
	} else if c.RecoveryTimeout < minRecoveryTimeout {
		c.RecoveryTimeout = minRecoveryTimeout
	}
	if c.RecoveryBatchSize <= 0 {
		c.RecoveryBatchSize = defaultRecoveryBatchSize
	} else if c.RecoveryBatchSize > maxRecoveryBatchSize {
		c.RecoveryBatchSize = maxRecoveryBatchSize
	}
	if c.LeadershipRetryInterval <= 0 {
		c.LeadershipRetryInterval = defaultLeadershipRetryInterval
	}
	if c.JobRetentionTTL <= 0 {
		c.JobRetentionTTL = defaultJobRetentionTTL
	}
	if c.JobRetentionCleanupInterval <= 0 {
		c.JobRetentionCleanupInterval = defaultJobRetentionCleanupInterval
	}
	if c.LeaderDurableRequeuePollInterval <= 0 {
		c.LeaderDurableRequeuePollInterval = defaultLeaderRequeuePollInterval
	} else if c.LeaderDurableRequeuePollInterval < minRequeuePollInterval {
		c.LeaderDurableRequeuePollInterval = minRequeuePollInterval
	}
	if c.FollowerDurableRequeuePollInterval <= 0 {
		c.FollowerDurableRequeuePollInterval = defaultFollowerRequeuePollInterval
	} else if c.FollowerDurableRequeuePollInterval < minRequeuePollInterval {
		c.FollowerDurableRequeuePollInterval = minRequeuePollInterval
	}
	if c.ReconcileTimeout <= 0 {
		c.ReconcileTimeout = defaultReconcileTimeout
	} else if c.ReconcileTimeout < minReconcileTimeout {
		c.ReconcileTimeout = minReconcileTimeout
	}
	if c.AlertRequestTimeout <= 0 {
		c.AlertRequestTimeout = defaultAlertRequestTimeout
	}
	if c.AlertMaxAttempts <= 0 {
		c.AlertMaxAttempts = defaultAlertMaxAttempts
	}
	if c.AlertRetryBaseInterval <= 0 {
		c.AlertRetryBaseInterval = defaultAlertRetryBaseInterval
	} else if c.AlertRetryBaseInterval < minAlertRetryBaseInterval {
		c.AlertRetryBaseInterval = minAlertRetryBaseInterval
	}

	// Keep cleanup cadence comfortably below expiry to avoid stale affinity
	// entries persisting for long windows.
	if c.AffinityCleanupInterval >= c.AffinityTTL {
		c.AffinityCleanupInterval = adjustBoundedInterval(c.AffinityCleanupInterval, c.AffinityTTL, minAffinityCleanupInterval)
	}

	// Ensure deferred-capacity polling has at least one practical interval
	// before timeout.
	if c.StageCapacityDeferralPollInterval >= c.StageCapacityDeferralTimeout {
		c.StageCapacityDeferralPollInterval = adjustBoundedInterval(c.StageCapacityDeferralPollInterval, c.StageCapacityDeferralTimeout, minDeferralPollInterval)
	}

	if c.JobRetentionCleanupInterval >= c.JobRetentionTTL {
		c.JobRetentionCleanupInterval = adjustBoundedInterval(c.JobRetentionCleanupInterval, c.JobRetentionTTL, minRetentionCleanup)
	}
}

// Validated returns a validated copy of the receiver.
//
// Usage:
//
//	cfg := userCfg.Validated()
//	scheduler := NewSchedulerWithConfig(reg, exec, cfg)
//
// This avoids mutating shared config values directly.
func (c SchedulerConfig) Validated() SchedulerConfig {
	c.Validate()
	return c
}

func adjustBoundedInterval(interval, bound, minimum time.Duration) time.Duration {
	if interval < bound {
		return interval
	}
	adjusted := bound / 4
	if adjusted < minimum {
		return minimum
	}
	return adjusted
}
