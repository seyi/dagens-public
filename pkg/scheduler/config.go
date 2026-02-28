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
}

// DefaultSchedulerConfig returns the default scheduler configuration
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		AffinityTTL:             1 * time.Hour,
		AffinityCleanupInterval: 5 * time.Minute,
		EnableStickiness:        true,
		JobQueueSize:            100,
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
}
