package scheduler

import (
	"testing"
	"time"
)

func TestDefaultSchedulerConfig_ReturnsExpectedDefaults(t *testing.T) {
	cfg := DefaultSchedulerConfig()

	if cfg.AffinityTTL != 1*time.Hour {
		t.Fatalf("AffinityTTL = %v, want %v", cfg.AffinityTTL, 1*time.Hour)
	}
	if cfg.AffinityCleanupInterval != 5*time.Minute {
		t.Fatalf("AffinityCleanupInterval = %v, want %v", cfg.AffinityCleanupInterval, 5*time.Minute)
	}
	if cfg.JobQueueSize != 100 {
		t.Fatalf("JobQueueSize = %d, want %d", cfg.JobQueueSize, 100)
	}
	if cfg.RecoveryBatchSize != 128 {
		t.Fatalf("RecoveryBatchSize = %d, want %d", cfg.RecoveryBatchSize, 128)
	}
	if !cfg.EnableStickiness {
		t.Fatal("EnableStickiness = false, want true")
	}
	if !cfg.EnableJobRetentionCleanup {
		t.Fatal("EnableJobRetentionCleanup = false, want true")
	}
	if !cfg.EnableLeaderDurableRequeueLoop {
		t.Fatal("EnableLeaderDurableRequeueLoop = false, want true")
	}
	if cfg.EnableResumeRecoveredQueuedJobs {
		t.Fatal("EnableResumeRecoveredQueuedJobs = true, want false")
	}
}

func TestSchedulerConfigValidateAppliesDefaults(t *testing.T) {
	var cfg SchedulerConfig

	cfg.Validate()

	if cfg.AffinityTTL != 1*time.Hour {
		t.Fatalf("AffinityTTL = %v, want %v", cfg.AffinityTTL, 1*time.Hour)
	}
	if cfg.AffinityCleanupInterval != 5*time.Minute {
		t.Fatalf("AffinityCleanupInterval = %v, want %v", cfg.AffinityCleanupInterval, 5*time.Minute)
	}
	if cfg.JobQueueSize != 100 {
		t.Fatalf("JobQueueSize = %d, want %d", cfg.JobQueueSize, 100)
	}
	if cfg.DefaultWorkerMaxConcurrency != 1 {
		t.Fatalf("DefaultWorkerMaxConcurrency = %d, want %d", cfg.DefaultWorkerMaxConcurrency, 1)
	}
	if cfg.MaxWorkerConcurrencyCap != 100 {
		t.Fatalf("MaxWorkerConcurrencyCap = %d, want %d", cfg.MaxWorkerConcurrencyCap, 100)
	}
	if cfg.CapacityTTL != 5*time.Second {
		t.Fatalf("CapacityTTL = %v, want %v", cfg.CapacityTTL, 5*time.Second)
	}
	if cfg.DispatchRejectCooldown != 5*time.Second {
		t.Fatalf("DispatchRejectCooldown = %v, want %v", cfg.DispatchRejectCooldown, 5*time.Second)
	}
	if cfg.MaxDispatchAttempts != 3 {
		t.Fatalf("MaxDispatchAttempts = %d, want %d", cfg.MaxDispatchAttempts, 3)
	}
	if cfg.StageCapacityDeferralTimeout != 2*time.Second {
		t.Fatalf("StageCapacityDeferralTimeout = %v, want %v", cfg.StageCapacityDeferralTimeout, 2*time.Second)
	}
	if cfg.StageCapacityDeferralPollInterval != 100*time.Millisecond {
		t.Fatalf("StageCapacityDeferralPollInterval = %v, want %v", cfg.StageCapacityDeferralPollInterval, 100*time.Millisecond)
	}
	if cfg.RecoveryTimeout != 5*time.Minute {
		t.Fatalf("RecoveryTimeout = %v, want %v", cfg.RecoveryTimeout, 5*time.Minute)
	}
	if cfg.RecoveryBatchSize != 128 {
		t.Fatalf("RecoveryBatchSize = %d, want %d", cfg.RecoveryBatchSize, 128)
	}
	if cfg.JobRetentionTTL != 24*time.Hour {
		t.Fatalf("JobRetentionTTL = %v, want %v", cfg.JobRetentionTTL, 24*time.Hour)
	}
	if cfg.JobRetentionCleanupInterval != 5*time.Minute {
		t.Fatalf("JobRetentionCleanupInterval = %v, want %v", cfg.JobRetentionCleanupInterval, 5*time.Minute)
	}
	if cfg.EnableJobRetentionCleanup {
		t.Fatal("EnableJobRetentionCleanup = true, want false for zero-value config")
	}
	if cfg.EnableResumeRecoveredQueuedJobs {
		t.Fatal("EnableResumeRecoveredQueuedJobs = true, want false")
	}
}

func TestSchedulerConfigValidatePreservesExplicitValues(t *testing.T) {
	cfg := SchedulerConfig{
		AffinityTTL:                       2 * time.Hour,
		AffinityCleanupInterval:           10 * time.Minute,
		JobQueueSize:                      42,
		DefaultWorkerMaxConcurrency:       4,
		MaxWorkerConcurrencyCap:           12,
		CapacityTTL:                       9 * time.Second,
		DispatchRejectCooldown:            11 * time.Second,
		MaxDispatchAttempts:               7,
		EnableStageCapacityDeferral:       true,
		StageCapacityDeferralTimeout:      750 * time.Millisecond,
		StageCapacityDeferralPollInterval: 25 * time.Millisecond,
		RecoveryTimeout:                   2 * time.Minute,
		RecoveryBatchSize:                 256,
		JobRetentionTTL:                   2 * time.Hour,
		JobRetentionCleanupInterval:       10 * time.Minute,
		EnableJobRetentionCleanup:         false,
		EnableResumeRecoveredQueuedJobs:   true,
	}

	cfg.Validate()

	if cfg.AffinityTTL != 2*time.Hour {
		t.Fatalf("AffinityTTL = %v, want %v", cfg.AffinityTTL, 2*time.Hour)
	}
	if cfg.AffinityCleanupInterval != 10*time.Minute {
		t.Fatalf("AffinityCleanupInterval = %v, want %v", cfg.AffinityCleanupInterval, 10*time.Minute)
	}
	if cfg.JobQueueSize != 42 {
		t.Fatalf("JobQueueSize = %d, want %d", cfg.JobQueueSize, 42)
	}
	if cfg.DefaultWorkerMaxConcurrency != 4 {
		t.Fatalf("DefaultWorkerMaxConcurrency = %d, want %d", cfg.DefaultWorkerMaxConcurrency, 4)
	}
	if cfg.MaxWorkerConcurrencyCap != 12 {
		t.Fatalf("MaxWorkerConcurrencyCap = %d, want %d", cfg.MaxWorkerConcurrencyCap, 12)
	}
	if cfg.CapacityTTL != 9*time.Second {
		t.Fatalf("CapacityTTL = %v, want %v", cfg.CapacityTTL, 9*time.Second)
	}
	if cfg.DispatchRejectCooldown != 11*time.Second {
		t.Fatalf("DispatchRejectCooldown = %v, want %v", cfg.DispatchRejectCooldown, 11*time.Second)
	}
	if cfg.MaxDispatchAttempts != 7 {
		t.Fatalf("MaxDispatchAttempts = %d, want %d", cfg.MaxDispatchAttempts, 7)
	}
	if !cfg.EnableStageCapacityDeferral {
		t.Fatal("EnableStageCapacityDeferral = false, want true")
	}
	if cfg.StageCapacityDeferralTimeout != 750*time.Millisecond {
		t.Fatalf("StageCapacityDeferralTimeout = %v, want %v", cfg.StageCapacityDeferralTimeout, 750*time.Millisecond)
	}
	if cfg.StageCapacityDeferralPollInterval != 25*time.Millisecond {
		t.Fatalf("StageCapacityDeferralPollInterval = %v, want %v", cfg.StageCapacityDeferralPollInterval, 25*time.Millisecond)
	}
	if cfg.RecoveryTimeout != 2*time.Minute {
		t.Fatalf("RecoveryTimeout = %v, want %v", cfg.RecoveryTimeout, 2*time.Minute)
	}
	if cfg.RecoveryBatchSize != 256 {
		t.Fatalf("RecoveryBatchSize = %d, want %d", cfg.RecoveryBatchSize, 256)
	}
	if cfg.JobRetentionTTL != 2*time.Hour {
		t.Fatalf("JobRetentionTTL = %v, want %v", cfg.JobRetentionTTL, 2*time.Hour)
	}
	if cfg.JobRetentionCleanupInterval != 10*time.Minute {
		t.Fatalf("JobRetentionCleanupInterval = %v, want %v", cfg.JobRetentionCleanupInterval, 10*time.Minute)
	}
	if cfg.EnableJobRetentionCleanup {
		t.Fatal("EnableJobRetentionCleanup = true, want false")
	}
	if !cfg.EnableResumeRecoveredQueuedJobs {
		t.Fatal("EnableResumeRecoveredQueuedJobs = false, want true")
	}
}

func TestSchedulerConfigValidateAdjustsInvalidTimingRelationships(t *testing.T) {
	cfg := SchedulerConfig{
		AffinityTTL:                       1 * time.Minute,
		AffinityCleanupInterval:           2 * time.Minute, // invalid: >= TTL
		StageCapacityDeferralTimeout:      100 * time.Millisecond,
		StageCapacityDeferralPollInterval: 250 * time.Millisecond, // invalid: >= timeout
		JobRetentionTTL:                   4 * time.Minute,
		JobRetentionCleanupInterval:       10 * time.Minute, // invalid: >= TTL
	}

	cfg.Validate()

	if cfg.AffinityCleanupInterval != 15*time.Second {
		t.Fatalf("AffinityCleanupInterval = %v, want %v", cfg.AffinityCleanupInterval, 15*time.Second)
	}
	if cfg.AffinityCleanupInterval >= cfg.AffinityTTL {
		t.Fatalf("AffinityCleanupInterval = %v, want < AffinityTTL %v", cfg.AffinityCleanupInterval, cfg.AffinityTTL)
	}
	if cfg.StageCapacityDeferralPollInterval != 25*time.Millisecond {
		t.Fatalf(
			"StageCapacityDeferralPollInterval = %v, want %v",
			cfg.StageCapacityDeferralPollInterval,
			25*time.Millisecond,
		)
	}
	if cfg.StageCapacityDeferralPollInterval >= cfg.StageCapacityDeferralTimeout {
		t.Fatalf(
			"StageCapacityDeferralPollInterval = %v, want < StageCapacityDeferralTimeout %v",
			cfg.StageCapacityDeferralPollInterval,
			cfg.StageCapacityDeferralTimeout,
		)
	}
	if cfg.JobRetentionCleanupInterval >= cfg.JobRetentionTTL {
		t.Fatalf(
			"JobRetentionCleanupInterval = %v, want < JobRetentionTTL %v",
			cfg.JobRetentionCleanupInterval,
			cfg.JobRetentionTTL,
		)
	}
}

func TestSchedulerConfigValidatePreservesValidTimingRelationships(t *testing.T) {
	cfg := SchedulerConfig{
		AffinityTTL:                       1 * time.Minute,
		AffinityCleanupInterval:           10 * time.Second, // valid: < TTL
		StageCapacityDeferralTimeout:      1 * time.Second,
		StageCapacityDeferralPollInterval: 100 * time.Millisecond, // valid: < timeout
	}

	cfg.Validate()

	if cfg.AffinityCleanupInterval != 10*time.Second {
		t.Fatalf("AffinityCleanupInterval = %v, want %v", cfg.AffinityCleanupInterval, 10*time.Second)
	}
	if cfg.StageCapacityDeferralPollInterval != 100*time.Millisecond {
		t.Fatalf(
			"StageCapacityDeferralPollInterval = %v, want %v",
			cfg.StageCapacityDeferralPollInterval,
			100*time.Millisecond,
		)
	}
}

func TestSchedulerConfigValidateHandlesNegativeValues(t *testing.T) {
	cfg := SchedulerConfig{
		JobQueueSize:                -100,
		CapacityTTL:                 -1 * time.Second,
		RecoveryTimeout:             100 * time.Millisecond,
		RecoveryBatchSize:           -5,
		MaxDispatchAttempts:         -2,
		MaxWorkerConcurrencyCap:     -9,
		JobRetentionTTL:             -2 * time.Second,
		JobRetentionCleanupInterval: -3 * time.Second,
	}

	cfg.Validate()

	if cfg.JobQueueSize != 100 {
		t.Fatalf("JobQueueSize = %d, want %d", cfg.JobQueueSize, 100)
	}
	if cfg.CapacityTTL != 5*time.Second {
		t.Fatalf("CapacityTTL = %v, want %v", cfg.CapacityTTL, 5*time.Second)
	}
	if cfg.RecoveryBatchSize != 128 {
		t.Fatalf("RecoveryBatchSize = %d, want %d", cfg.RecoveryBatchSize, 128)
	}
	if cfg.RecoveryTimeout != 1*time.Second {
		t.Fatalf("RecoveryTimeout = %v, want %v", cfg.RecoveryTimeout, 1*time.Second)
	}
	if cfg.MaxDispatchAttempts != 3 {
		t.Fatalf("MaxDispatchAttempts = %d, want %d", cfg.MaxDispatchAttempts, 3)
	}
	if cfg.MaxWorkerConcurrencyCap != 100 {
		t.Fatalf("MaxWorkerConcurrencyCap = %d, want %d", cfg.MaxWorkerConcurrencyCap, 100)
	}
	if cfg.JobRetentionTTL != 24*time.Hour {
		t.Fatalf("JobRetentionTTL = %v, want %v", cfg.JobRetentionTTL, 24*time.Hour)
	}
	if cfg.JobRetentionCleanupInterval != 5*time.Minute {
		t.Fatalf("JobRetentionCleanupInterval = %v, want %v", cfg.JobRetentionCleanupInterval, 5*time.Minute)
	}
}

func TestSchedulerConfigValidateEnforcesMinRecoveryTimeout(t *testing.T) {
	cfg := SchedulerConfig{
		RecoveryTimeout: 250 * time.Millisecond,
	}

	cfg.Validate()

	if cfg.RecoveryTimeout != 1*time.Second {
		t.Fatalf("RecoveryTimeout = %v, want %v", cfg.RecoveryTimeout, 1*time.Second)
	}
}

func TestSchedulerConfigValidateCapsExtremeValues(t *testing.T) {
	cfg := SchedulerConfig{
		JobQueueSize:            1_000_000,
		RecoveryBatchSize:       100_000,
		MaxDispatchAttempts:     10_000,
		MaxWorkerConcurrencyCap: 500_000,
	}

	cfg.Validate()

	if cfg.JobQueueSize != 10000 {
		t.Fatalf("JobQueueSize = %d, want %d", cfg.JobQueueSize, 10000)
	}
	if cfg.RecoveryBatchSize != 4096 {
		t.Fatalf("RecoveryBatchSize = %d, want %d", cfg.RecoveryBatchSize, 4096)
	}
	if cfg.MaxDispatchAttempts != 50 {
		t.Fatalf("MaxDispatchAttempts = %d, want %d", cfg.MaxDispatchAttempts, 50)
	}
	if cfg.MaxWorkerConcurrencyCap != 10000 {
		t.Fatalf("MaxWorkerConcurrencyCap = %d, want %d", cfg.MaxWorkerConcurrencyCap, 10000)
	}
}

func TestSchedulerConfigValidateSmallTTLProducesMinimumIntervals(t *testing.T) {
	cfg := SchedulerConfig{
		AffinityTTL:                       1 * time.Millisecond,
		AffinityCleanupInterval:           2 * time.Millisecond,
		StageCapacityDeferralTimeout:      1 * time.Millisecond,
		StageCapacityDeferralPollInterval: 2 * time.Millisecond,
		JobRetentionTTL:                   1 * time.Millisecond,
		JobRetentionCleanupInterval:       2 * time.Millisecond,
	}

	cfg.Validate()

	if cfg.AffinityCleanupInterval < 100*time.Millisecond {
		t.Fatalf("AffinityCleanupInterval = %v, want >= %v", cfg.AffinityCleanupInterval, 100*time.Millisecond)
	}
	if cfg.StageCapacityDeferralPollInterval < 10*time.Millisecond {
		t.Fatalf("StageCapacityDeferralPollInterval = %v, want >= %v", cfg.StageCapacityDeferralPollInterval, 10*time.Millisecond)
	}
	if cfg.JobRetentionCleanupInterval < 1*time.Second {
		t.Fatalf("JobRetentionCleanupInterval = %v, want >= %v", cfg.JobRetentionCleanupInterval, 1*time.Second)
	}
}

func TestSchedulerConfigValidateDoesNotAlterBooleanFields(t *testing.T) {
	cfg := SchedulerConfig{
		EnableStickiness:                false,
		EnableResumeRecoveredQueuedJobs: false,
		EnableJobRetentionCleanup:       false,
		EnableLeaderDurableRequeueLoop:  false,
	}

	cfg.Validate()

	if cfg.EnableStickiness {
		t.Fatal("EnableStickiness = true, want false")
	}
	if cfg.EnableResumeRecoveredQueuedJobs {
		t.Fatal("EnableResumeRecoveredQueuedJobs = true, want false")
	}
	if cfg.EnableJobRetentionCleanup {
		t.Fatal("EnableJobRetentionCleanup = true, want false")
	}
	if cfg.EnableLeaderDurableRequeueLoop {
		t.Fatal("EnableLeaderDurableRequeueLoop = true, want false")
	}
}

func TestSchedulerConfigValidatedReturnsCopy(t *testing.T) {
	original := SchedulerConfig{
		JobQueueSize: -1,
	}
	validated := original.Validated()

	if original.JobQueueSize != -1 {
		t.Fatalf("original.JobQueueSize mutated = %d, want -1", original.JobQueueSize)
	}
	if validated.JobQueueSize != 100 {
		t.Fatalf("validated.JobQueueSize = %d, want %d", validated.JobQueueSize, 100)
	}
}
