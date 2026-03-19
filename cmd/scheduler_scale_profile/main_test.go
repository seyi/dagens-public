package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/scheduler"
)

func TestValidateConfig(t *testing.T) {
	base := profileConfig{
		StoreBackend: "memory",
		JobCount:     10,
		TasksPerJob:  2,
		Concurrency:  4,
		JobQueueSize: 20,
		EvidenceRoot: t.TempDir(),
		Timeout:      time.Minute,
	}

	tests := []struct {
		name    string
		cfg     profileConfig
		wantErr string
	}{
		{name: "memory ok", cfg: base},
		{name: "postgres requires dsn", cfg: func() profileConfig {
			cfg := base
			cfg.StoreBackend = "postgres"
			return cfg
		}(), wantErr: "database-url is required"},
		{name: "unsupported backend", cfg: func() profileConfig {
			cfg := base
			cfg.StoreBackend = "sqlite"
			return cfg
		}(), wantErr: "unsupported store-backend"},
		{name: "invalid counts", cfg: func() profileConfig {
			cfg := base
			cfg.JobCount = 0
			return cfg
		}(), wantErr: "must all be > 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("validateConfig unexpected error: %v", err)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateConfig error = %v, want substring %q", err, tt.wantErr)
				}
			}
		})
	}
}

func TestNewObservedStoreMemory(t *testing.T) {
	store, err := newObservedStore(context.Background(), profileConfig{
		StoreBackend: "memory",
		JobCount:     1,
		TasksPerJob:  1,
		Concurrency:  1,
		JobQueueSize: 1,
	})
	if err != nil {
		t.Fatalf("newObservedStore unexpected error: %v", err)
	}
	t.Cleanup(store.Close)

	if _, ok := store.base.(*scheduler.InMemoryTransitionStore); !ok {
		t.Fatalf("store.base type = %T, want *scheduler.InMemoryTransitionStore", store.base)
	}
	if store.taskLookup == nil {
		t.Fatal("expected taskLookup to be set for memory store")
	}
}

func TestNewObservedStorePostgresCreateFailure(t *testing.T) {
	_, err := newObservedStore(context.Background(), profileConfig{
		StoreBackend: "postgres",
		DatabaseURL:  "postgres://invalid:://not-a-dsn",
		JobCount:     1,
		TasksPerJob:  1,
		Concurrency:  1,
		JobQueueSize: 1,
	})
	if err == nil {
		t.Fatal("expected newObservedStore to fail for invalid postgres DSN")
	}
}
