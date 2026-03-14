package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmitOperationalAlert_DeliversWebhookPayload(t *testing.T) {
	payloadCh := make(chan schedulerAlertPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload schedulerAlertPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		payloadCh <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := DefaultSchedulerConfig()
	cfg.AlertWebhookURL = server.URL
	cfg.AlertRequestTimeout = 2 * time.Second

	s := NewSchedulerWithConfig(nil, nil, cfg)
	s.emitOperationalAlert(context.Background(), "scheduler.recovery.failed", "critical", "recovery failed", map[string]interface{}{
		"status": "failed",
	})

	select {
	case got := <-payloadCh:
		if got.Event != "scheduler.recovery.failed" {
			t.Fatalf("event=%q, want %q", got.Event, "scheduler.recovery.failed")
		}
		if got.Severity != "critical" {
			t.Fatalf("severity=%q, want %q", got.Severity, "critical")
		}
		if got.Message != "recovery failed" {
			t.Fatalf("message=%q, want %q", got.Message, "recovery failed")
		}
		if got.Timestamp.IsZero() {
			t.Fatal("expected non-zero timestamp")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook payload")
	}
}

func TestEmitOperationalAlert_RetriesUntilSuccess(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := DefaultSchedulerConfig()
	cfg.AlertWebhookURL = server.URL
	cfg.AlertRequestTimeout = 2 * time.Second
	cfg.AlertMaxAttempts = 3
	cfg.AlertRetryBaseInterval = 10 * time.Millisecond

	s := NewSchedulerWithConfig(nil, nil, cfg)
	s.emitOperationalAlert(context.Background(), "scheduler.recovery.failed", "critical", "recovery failed", nil)

	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts=%d, want 3", got)
	}
}

func TestEmitOperationalAlert_ExhaustsRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := DefaultSchedulerConfig()
	cfg.AlertWebhookURL = server.URL
	cfg.AlertRequestTimeout = 2 * time.Second
	cfg.AlertMaxAttempts = 3
	cfg.AlertRetryBaseInterval = 10 * time.Millisecond

	s := NewSchedulerWithConfig(nil, nil, cfg)
	s.emitOperationalAlert(context.Background(), "scheduler.recovery.failed", "critical", "recovery failed", nil)

	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts=%d, want 3", got)
	}
}

func TestEmitOperationalAlert_RespectsContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		<-time.After(5 * time.Second)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := DefaultSchedulerConfig()
	cfg.AlertWebhookURL = server.URL
	cfg.AlertRequestTimeout = 150 * time.Millisecond
	cfg.AlertMaxAttempts = 3
	cfg.AlertRetryBaseInterval = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	s := NewSchedulerWithConfig(nil, nil, cfg)
	s.emitOperationalAlert(ctx, "scheduler.recovery.failed", "critical", "recovery failed", nil)
	elapsed := time.Since(start)

	if elapsed > 600*time.Millisecond {
		t.Fatalf("alert send exceeded expected timeout bound: %v", elapsed)
	}
}
