package scheduler

import (
	"testing"
	"time"
)

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
	if cfg.RecoveryTimeout != 5*time.Minute {
		t.Fatalf("RecoveryTimeout = %v, want %v", cfg.RecoveryTimeout, 5*time.Minute)
	}
}

func TestSchedulerConfigValidatePreservesExplicitValues(t *testing.T) {
	cfg := SchedulerConfig{
		AffinityTTL:                 2 * time.Hour,
		AffinityCleanupInterval:     10 * time.Minute,
		JobQueueSize:                42,
		DefaultWorkerMaxConcurrency: 4,
		MaxWorkerConcurrencyCap:     12,
		CapacityTTL:                 9 * time.Second,
		DispatchRejectCooldown:      11 * time.Second,
		MaxDispatchAttempts:         7,
		RecoveryTimeout:             2 * time.Minute,
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
	if cfg.RecoveryTimeout != 2*time.Minute {
		t.Fatalf("RecoveryTimeout = %v, want %v", cfg.RecoveryTimeout, 2*time.Minute)
	}
}
