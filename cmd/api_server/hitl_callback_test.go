package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/hitl"
)

type testQueue struct {
	job *hitl.ResumptionJob
}

func (q *testQueue) Enqueue(ctx context.Context, job *hitl.ResumptionJob) error {
	q.job = job
	return nil
}
func (q *testQueue) Dequeue(ctx context.Context) (*hitl.ResumptionJob, error) {
	return nil, context.Canceled
}
func (q *testQueue) Ack(ctx context.Context, jobID string) error { return nil }
func (q *testQueue) QueueLength(ctx context.Context) (int64, error) {
	return 1, nil
}

type testIdStore struct {
	keys map[string]bool
}

func newTestIdStore() *testIdStore {
	return &testIdStore{keys: make(map[string]bool)}
}

func (s *testIdStore) Exists(key string) (bool, error) {
	return s.keys[key], nil
}
func (s *testIdStore) Set(key string, ttl time.Duration) error {
	s.keys[key] = true
	return nil
}
func (s *testIdStore) SetNX(key string, ttl time.Duration) (bool, error) {
	if s.keys[key] {
		return false, nil
	}
	s.keys[key] = true
	return true, nil
}
func (s *testIdStore) Delete(key string) error {
	delete(s.keys, key)
	return nil
}

func TestHITLCallbackHTTPHandler_Success(t *testing.T) {
	secret := "test-secret"
	requestID := "req-123"
	ts := time.Now().Unix()
	sig := sign(requestID, ts, secret)

	queue := &testQueue{}
	handler := newHITLCallbackHTTPHandler(hitl.NewCallbackHandler(
		queue,
		newTestIdStore(),
		nil,
		[]byte(secret),
		hitl.NewMetricsCollector().GetMetrics(),
	))

	req := httptest.NewRequest(http.MethodPost, "/api/human-callback?req="+requestID+"&ts="+strconv.FormatInt(ts, 10)+"&sig="+sig, strings.NewReader(`{"selected_option":"approve"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if queue.job == nil {
		t.Fatal("expected enqueue to capture job")
	}
	if queue.job.RequestID != requestID {
		t.Fatalf("request_id = %q, want %q", queue.job.RequestID, requestID)
	}
	if queue.job.Response == nil || queue.job.Response.SelectedOption != "approve" {
		t.Fatalf("unexpected response payload: %#v", queue.job.Response)
	}
}

func TestHITLCallbackHTTPHandler_BadRequest(t *testing.T) {
	handler := newHITLCallbackHTTPHandler(hitl.NewCallbackHandler(
		&testQueue{},
		newTestIdStore(),
		nil,
		[]byte("secret"),
		hitl.NewMetricsCollector().GetMetrics(),
	))

	cases := []struct {
		name   string
		method string
		url    string
		body   string
		status int
	}{
		{name: "method", method: http.MethodGet, url: "/api/human-callback", status: http.StatusMethodNotAllowed},
		{name: "missing req", method: http.MethodPost, url: "/api/human-callback?ts=1&sig=x", status: http.StatusBadRequest},
		{name: "bad ts", method: http.MethodPost, url: "/api/human-callback?req=r1&ts=bad&sig=x", status: http.StatusBadRequest},
		{name: "bad body", method: http.MethodPost, url: "/api/human-callback?req=r1&ts=1&sig=x", body: `{"selected_option":"ok","x":1}`, status: http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}

func sign(requestID string, ts int64, secret string) string {
	payload := requestID + ":" + strconv.FormatInt(ts, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
