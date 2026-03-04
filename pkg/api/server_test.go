package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/scheduler"
)

const simpleJobRequest = `{
  "name": "hello-graph",
  "nodes": [
    {"id": "start", "type": "function", "name": "Start"},
    {"id": "end", "type": "function", "name": "End"}
  ],
  "edges": [{"from": "start", "to": "end"}],
  "entry_node": "start",
  "finish_nodes": ["end"],
  "input": {"instruction": "Hello from Dagens"}
}`

func TestSubmitJobHandlerReturnsRetryAfterWhenQueueIsFull(t *testing.T) {
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		JobQueueSize: 1,
	})
	server := NewServer(sched)

	firstReq := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(simpleJobRequest))
	firstRec := httptest.NewRecorder()
	server.SubmitJobHandler(firstRec, firstReq)

	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("first response code = %d, want %d", firstRec.Code, http.StatusAccepted)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(simpleJobRequest))
	secondRec := httptest.NewRecorder()
	server.SubmitJobHandler(secondRec, secondReq)

	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("second response code = %d, want %d", secondRec.Code, http.StatusTooManyRequests)
	}

	if got := secondRec.Header().Get("Retry-After"); got != schedulerRetryAfterSeconds {
		t.Fatalf("Retry-After = %q, want %q", got, schedulerRetryAfterSeconds)
	}

	if !strings.Contains(secondRec.Body.String(), "job queue is full") {
		t.Fatalf("response body = %q, want queue saturation message", secondRec.Body.String())
	}
}

func TestUpdateWorkerCapacityHandlerAcceptsHeartbeat(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})
	server := NewServer(sched)

	body, _ := json.Marshal(WorkerCapacityUpdateRequest{
		NodeID:          "worker-1",
		InFlight:        2,
		MaxConcurrency:  4,
		ReportTimestamp: time.Now().UTC(),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/worker_capacity", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()

	server.UpdateWorkerCapacityHandler(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUpdateWorkerCapacityHandlerRejectsBadToken(t *testing.T) {
	t.Setenv("DEV_MODE", "false")
	t.Setenv("WORKER_HEARTBEAT_TOKEN", "expected-token")
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})
	server := NewServer(sched)

	body, _ := json.Marshal(WorkerCapacityUpdateRequest{
		NodeID:          "worker-1",
		InFlight:        1,
		MaxConcurrency:  2,
		ReportTimestamp: time.Now().UTC(),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/worker_capacity", strings.NewReader(string(body)))
	req.Header.Set("X-Dagens-Worker-Token", "wrong-token")
	rec := httptest.NewRecorder()

	server.UpdateWorkerCapacityHandler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateWorkerCapacityHandlerRejectsWhenAuthRequiredButTokenMissing(t *testing.T) {
	t.Setenv("DEV_MODE", "false")
	t.Setenv("WORKER_HEARTBEAT_TOKEN", "")
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})
	server := NewServer(sched)

	body, _ := json.Marshal(WorkerCapacityUpdateRequest{
		NodeID:          "worker-1",
		InFlight:        1,
		MaxConcurrency:  2,
		ReportTimestamp: time.Now().UTC(),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/worker_capacity", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()

	server.UpdateWorkerCapacityHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestUpdateWorkerCapacityHandlerRejectsNegativeInFlight(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})
	server := NewServer(sched)

	body, _ := json.Marshal(WorkerCapacityUpdateRequest{
		NodeID:          "worker-1",
		InFlight:        -1,
		MaxConcurrency:  2,
		ReportTimestamp: time.Now().UTC(),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/worker_capacity", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()

	server.UpdateWorkerCapacityHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdateWorkerCapacityHandlerRejectsFutureReportTimestamp(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})
	server := NewServer(sched)

	body, _ := json.Marshal(WorkerCapacityUpdateRequest{
		NodeID:          "worker-1",
		InFlight:        1,
		MaxConcurrency:  2,
		ReportTimestamp: time.Now().UTC().Add(10 * time.Second),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/worker_capacity", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()

	server.UpdateWorkerCapacityHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdateWorkerCapacityHandlerRecordsSuccessMetric(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})
	server := NewServer(sched)

	_ = observability.GetMetrics()
	before := metricCounterValue(t, "spark_agent_worker_heartbeats_succeeded_total")

	body, _ := json.Marshal(WorkerCapacityUpdateRequest{
		NodeID:          "worker-1",
		InFlight:        1,
		MaxConcurrency:  2,
		ReportTimestamp: time.Now().UTC(),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/worker_capacity", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()

	server.UpdateWorkerCapacityHandler(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusNoContent)
	}

	after := metricCounterValue(t, "spark_agent_worker_heartbeats_succeeded_total")
	if after != before+1 {
		t.Fatalf("worker heartbeat success metric = %v, want %v", after, before+1)
	}
}

func metricCounterValue(t *testing.T, metricName string) float64 {
	t.Helper()

	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	for _, family := range metricFamilies {
		if family.GetName() != metricName {
			continue
		}
		metrics := family.GetMetric()
		if len(metrics) == 0 || metrics[0].GetCounter() == nil {
			t.Fatalf("metric %q missing counter value", metricName)
		}
		return metrics[0].GetCounter().GetValue()
	}

	t.Fatalf("metric %q not found", metricName)
	return 0
}
