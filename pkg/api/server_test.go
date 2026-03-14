package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

type testLeadershipProvider struct {
	authority scheduler.LeadershipAuthority
	err       error
}

func (p testLeadershipProvider) Start(context.Context) error { return nil }
func (p testLeadershipProvider) Stop()                       {}
func (p testLeadershipProvider) DispatchAuthority(context.Context) (scheduler.LeadershipAuthority, error) {
	if p.err != nil {
		return scheduler.LeadershipAuthority{}, p.err
	}
	return p.authority, nil
}

type retryLeadershipProvider struct {
	attempts  int
	failures  int
	authority scheduler.LeadershipAuthority
}

func (p *retryLeadershipProvider) Start(context.Context) error { return nil }
func (p *retryLeadershipProvider) Stop()                       {}
func (p *retryLeadershipProvider) DispatchAuthority(context.Context) (scheduler.LeadershipAuthority, error) {
	p.attempts++
	if p.attempts <= p.failures {
		return scheduler.LeadershipAuthority{}, errors.New("transient leadership lookup failure")
	}
	return p.authority, nil
}

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

func TestSubmitJobHandlerRejectsFollowerScheduler(t *testing.T) {
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		JobQueueSize: 1,
	})
	if err := sched.SetLeadershipProvider(testLeadershipProvider{
		authority: scheduler.LeadershipAuthority{IsLeader: false, LeaderID: "api-b", Epoch: "99"},
	}); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}
	server := NewServer(sched)

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(simpleJobRequest))
	rec := httptest.NewRecorder()
	server.SubmitJobHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Header().Get("Retry-After"); got != schedulerLeaderRetryAfterSeconds {
		t.Fatalf("Retry-After = %q, want %q", got, schedulerLeaderRetryAfterSeconds)
	}
	if got := rec.Header().Get("X-Dagens-Leader-ID"); got != "api-b" {
		t.Fatalf("X-Dagens-Leader-ID = %q, want %q", got, "api-b")
	}
	if !strings.Contains(rec.Body.String(), "follower mode") {
		t.Fatalf("response body = %q, want follower mode message", rec.Body.String())
	}
}

func TestSubmitJobHandlerForwardsFollowerSubmissionToLeader(t *testing.T) {
	t.Setenv("CONTROL_PLANE_ID", "api-a")
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Dagens-Forwarded-By"); got != "api-a" {
			t.Fatalf("X-Dagens-Forwarded-By = %q, want %q", got, "api-a")
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/jobs" {
			t.Fatalf("path = %s, want /v1/jobs", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Fatalf("Authorization = %q, want bearer token forwarded", got)
		}
		if got := r.Header.Get("X-Dagens-Worker-Token"); got != "" {
			t.Fatalf("X-Dagens-Worker-Token = %q, want stripped", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Fatalf("Cookie = %q, want stripped", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"job_id":"forwarded-job","status":"PENDING","submitted_at":"2026-03-13T18:00:00Z","message":"Job submitted successfully"}`))
	}))
	defer leader.Close()
	t.Setenv("SCHEDULER_LEADER_FORWARD_URLS", "api-b="+leader.URL)

	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		JobQueueSize: 1,
	})
	if err := sched.SetLeadershipProvider(testLeadershipProvider{
		authority: scheduler.LeadershipAuthority{IsLeader: false, LeaderID: "api-b", Epoch: "99"},
	}); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}
	server := NewServer(sched)

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(simpleJobRequest))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("X-Dagens-Worker-Token", "internal-token")
	req.Header.Set("Cookie", "session=abc")
	rec := httptest.NewRecorder()
	server.SubmitJobHandler(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"job_id":"forwarded-job"`) {
		t.Fatalf("response body = %q, want forwarded job response", got)
	}
}

func TestSubmitJobHandlerRejectsWhenLeadershipUnavailable(t *testing.T) {
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		JobQueueSize: 1,
	})
	if err := sched.SetLeadershipProvider(testLeadershipProvider{
		err: errors.New("etcd unavailable"),
	}); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}
	server := NewServer(sched)

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(simpleJobRequest))
	rec := httptest.NewRecorder()
	server.SubmitJobHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Header().Get("Retry-After"); got != schedulerLeaderRetryAfterSeconds {
		t.Fatalf("Retry-After = %q, want %q", got, schedulerLeaderRetryAfterSeconds)
	}
	if !strings.Contains(rec.Body.String(), "leadership status unavailable") {
		t.Fatalf("response body = %q, want leadership unavailable message", rec.Body.String())
	}
}

func TestSubmitJobHandlerRetriesAuthorityLookupBeforeFailing(t *testing.T) {
	provider := &retryLeadershipProvider{
		failures:  2,
		authority: scheduler.LeadershipAuthority{IsLeader: false, LeaderID: "api-b", Epoch: "99"},
	}
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		JobQueueSize: 1,
	})
	if err := sched.SetLeadershipProvider(provider); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}
	server := NewServer(sched)

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(simpleJobRequest))
	rec := httptest.NewRecorder()
	server.SubmitJobHandler(rec, req)

	if provider.attempts != 3 {
		t.Fatalf("authority attempts = %d, want 3", provider.attempts)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSubmitJobHandlerReturns503AfterAuthorityRetryExhaustion(t *testing.T) {
	provider := &retryLeadershipProvider{failures: authorityLookupAttempts}
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		JobQueueSize: 1,
	})
	if err := sched.SetLeadershipProvider(provider); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}
	server := NewServer(sched)

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(simpleJobRequest))
	rec := httptest.NewRecorder()
	server.SubmitJobHandler(rec, req)

	if provider.attempts != authorityLookupAttempts {
		t.Fatalf("authority attempts = %d, want %d", provider.attempts, authorityLookupAttempts)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), "leadership status unavailable") {
		t.Fatalf("response body = %q, want leadership unavailable message", rec.Body.String())
	}
}

func TestSubmitJobHandlerRejectsOversizedRequestBody(t *testing.T) {
	t.Setenv("API_MAX_JOB_REQUEST_BYTES", "32")
	sched := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.SchedulerConfig{
		JobQueueSize: 1,
	})
	server := NewServer(sched)

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(simpleJobRequest))
	rec := httptest.NewRecorder()
	server.SubmitJobHandler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("response code = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(rec.Body.String(), "Request body too large") {
		t.Fatalf("response body = %q, want body too large message", rec.Body.String())
	}
}

func TestMaxBytesReaderReturnsMaxBytesError(t *testing.T) {
	reader := http.MaxBytesReader(httptest.NewRecorder(), io.NopCloser(strings.NewReader("hello world")), 5)
	_, err := io.ReadAll(reader)
	if err == nil {
		t.Fatal("expected max bytes error")
	}

	var maxBytesErr *http.MaxBytesError
	if !errors.As(err, &maxBytesErr) {
		t.Fatalf("error = %T %v, want *http.MaxBytesError", err, err)
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
	before := metricCounterValue(t, "dagens_worker_heartbeats_succeeded_total")

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

	after := metricCounterValue(t, "dagens_worker_heartbeats_succeeded_total")
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
