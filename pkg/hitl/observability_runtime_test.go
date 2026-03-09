package hitl

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/telemetry"
)

type testCounter struct {
	val float64
}

func (c *testCounter) Inc()          { c.val += 1 }
func (c *testCounter) Add(v float64) { c.val += v }

type testHistogram struct {
	observations int
	last         float64
}

func (h *testHistogram) Observe(v float64) {
	h.observations++
	h.last = v
}

type testGauge struct {
	val float64
}

func (g *testGauge) Set(v float64) { g.val = v }
func (g *testGauge) Inc()          { g.val += 1 }
func (g *testGauge) Dec()          { g.val -= 1 }
func (g *testGauge) Add(v float64) { g.val += v }

type testQueue struct {
	enqueueErr error
	acked      int
	queueLen   int64
	lastJob    *ResumptionJob
}

func (q *testQueue) Enqueue(ctx context.Context, job *ResumptionJob) error {
	q.lastJob = job
	return q.enqueueErr
}
func (q *testQueue) Dequeue(ctx context.Context) (*ResumptionJob, error) {
	return nil, context.Canceled
}
func (q *testQueue) Ack(ctx context.Context, jobID string) error {
	q.acked++
	return nil
}
func (q *testQueue) QueueLength(ctx context.Context) (int64, error) {
	return q.queueLen, nil
}

type testIdempotency struct {
	exists   bool
	setNX    bool
	setErr   error
	setCalls int
	setNXFn  func(key string, ttl time.Duration) (bool, error)
}

func (s *testIdempotency) Exists(key string) (bool, error) { return s.exists, nil }
func (s *testIdempotency) Set(key string, ttl time.Duration) error {
	s.setCalls++
	return s.setErr
}
func (s *testIdempotency) SetNX(key string, ttl time.Duration) (bool, error) {
	if s.setNXFn != nil {
		return s.setNXFn(key, ttl)
	}
	return s.setNX, nil
}
func (s *testIdempotency) Delete(key string) error { return nil }

type testCheckpointStore struct {
	cp           *ExecutionCheckpoint
	moveCalls    int
	recordFail   int
	createCalls  int
	deleteCalls  int
	replaceCalls int
	created      []*ExecutionCheckpoint
	createErr    error
	deleteErr    error
	replaceErr   error
}

type testCheckpointStoreNoReplace struct {
	inner *testCheckpointStore
}

func (s *testCheckpointStore) CreateWithTransaction(tx Transaction, cp *ExecutionCheckpoint) error {
	return nil
}
func (s *testCheckpointStore) Create(cp *ExecutionCheckpoint) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.createCalls++
	s.created = append(s.created, cp)
	return nil
}
func (s *testCheckpointStore) GetByRequestID(requestID string) (*ExecutionCheckpoint, error) {
	if s.cp == nil {
		return nil, ErrCheckpointNotFound
	}
	return s.cp, nil
}
func (s *testCheckpointStore) Delete(requestID string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleteCalls++
	return nil
}

func (s *testCheckpointStore) Replace(oldRequestID string, next *ExecutionCheckpoint) error {
	if s.replaceErr != nil {
		return s.replaceErr
	}
	s.replaceCalls++
	s.created = append(s.created, next)
	return nil
}
func (s *testCheckpointStore) ListOrphaned(olderThan time.Duration) ([]*ExecutionCheckpoint, error) {
	return nil, nil
}
func (s *testCheckpointStore) RecordFailure(requestID string, err error) (*ExecutionCheckpoint, error) {
	s.recordFail++
	return s.cp, nil
}
func (s *testCheckpointStore) MoveToCheckpointDLQ(requestID string, finalError string) error {
	s.moveCalls++
	return nil
}

func (s *testCheckpointStoreNoReplace) CreateWithTransaction(tx Transaction, cp *ExecutionCheckpoint) error {
	return s.inner.CreateWithTransaction(tx, cp)
}
func (s *testCheckpointStoreNoReplace) Create(cp *ExecutionCheckpoint) error {
	return s.inner.Create(cp)
}
func (s *testCheckpointStoreNoReplace) GetByRequestID(requestID string) (*ExecutionCheckpoint, error) {
	return s.inner.GetByRequestID(requestID)
}
func (s *testCheckpointStoreNoReplace) Delete(requestID string) error {
	return s.inner.Delete(requestID)
}
func (s *testCheckpointStoreNoReplace) ListOrphaned(olderThan time.Duration) ([]*ExecutionCheckpoint, error) {
	return s.inner.ListOrphaned(olderThan)
}
func (s *testCheckpointStoreNoReplace) RecordFailure(requestID string, err error) (*ExecutionCheckpoint, error) {
	return s.inner.RecordFailure(requestID, err)
}
func (s *testCheckpointStoreNoReplace) MoveToCheckpointDLQ(requestID string, finalError string) error {
	return s.inner.MoveToCheckpointDLQ(requestID, finalError)
}

type testGraphRegistry struct{}

func (r *testGraphRegistry) GetGraph(graphID string) (GraphDefinition, error) {
	return GraphDefinition{ID: graphID, Version: "v1"}, nil
}

type testExecutableGraphRegistry struct {
	definition GraphDefinition
	executable *graph.Graph
}

func (r *testExecutableGraphRegistry) GetGraph(graphID string) (GraphDefinition, error) {
	if r.definition.ID == "" {
		return GraphDefinition{ID: graphID, Version: "v1"}, nil
	}
	return r.definition, nil
}

func (r *testExecutableGraphRegistry) GetExecutableGraph(graphID string) (*graph.Graph, error) {
	if r.executable == nil {
		return nil, errors.New("executable graph not found")
	}
	return r.executable, nil
}

type testExecutor struct {
	resumeErr error
}

func (e *testExecutor) ExecuteCurrent(state graph.State) (graph.State, error) { return state, nil }
func (e *testExecutor) ResumeFromNode(nodeID string, state graph.State) error { return e.resumeErr }
func (e *testExecutor) CurrentNodeID() string                                 { return "node-1" }

type testOrchestratorExecutor struct {
	currentErr error
	nodeID     string
}

func (e *testOrchestratorExecutor) ExecuteCurrent(state graph.State) (graph.State, error) {
	return state, e.currentErr
}
func (e *testOrchestratorExecutor) ResumeFromNode(nodeID string, state graph.State) error { return nil }
func (e *testOrchestratorExecutor) CurrentNodeID() string {
	if e.nodeID == "" {
		return "node-orch"
	}
	return e.nodeID
}

type noMarshalState struct {
	graph.State
}

func TestCallbackHandlerObservability_EnqueueSuccess(t *testing.T) {
	received := &testCounter{}
	enqueued := &testCounter{}
	latency := &testHistogram{}
	metrics := &HITLMetrics{
		CallbacksReceived: received,
		CallbacksEnqueued: enqueued,
		CallbackLatency:   latency,
	}

	queue := &testQueue{}
	idStore := &testIdempotency{setNX: true}
	secret := []byte("test-secret")
	handler := NewCallbackHandler(queue, idStore, nil, secret, metrics)

	requestID := "req-obs-1"
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(requestID + ":" + strconv.FormatInt(ts, 10)))
	signature := hex.EncodeToString(mac.Sum(nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/callback", nil)
	handler.HandleHumanCallback(rec, req, requestID, ts, signature, &HumanResponse{SelectedOption: "ok"})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if received.val != 1 {
		t.Fatalf("callbacks_received = %v, want 1", received.val)
	}
	if enqueued.val != 1 {
		t.Fatalf("callbacks_enqueued = %v, want 1", enqueued.val)
	}
	if latency.observations == 0 {
		t.Fatal("expected callback latency observation")
	}
}

func TestCallbackHandler_InvalidSignature_Rejected(t *testing.T) {
	securityFailures := &testCounter{}
	metrics := &HITLMetrics{
		CallbacksReceived:       &testCounter{},
		CallbacksEnqueued:       &testCounter{},
		CallbackSecurityFailures: securityFailures,
	}

	queue := &testQueue{}
	idStore := &testIdempotency{setNX: true}
	handler := NewCallbackHandler(queue, idStore, nil, []byte("real-secret"), metrics)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/callback", nil)
	handler.HandleHumanCallback(
		rec,
		req,
		"req-invalid-signature",
		time.Now().Unix(),
		"not-a-valid-signature",
		&HumanResponse{SelectedOption: "approve"},
	)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if queue.lastJob != nil {
		t.Fatal("expected no enqueued job for invalid signature")
	}
	if securityFailures.val != 1 {
		t.Fatalf("callback_security_failure = %v, want 1", securityFailures.val)
	}
}

func TestCallbackHandler_InFlightDuplicate_SetNXFalse_NoEnqueue(t *testing.T) {
	duplicates := &testCounter{}
	metrics := &HITLMetrics{
		CallbacksReceived:  &testCounter{},
		CallbacksEnqueued:  &testCounter{},
		CallbackDuplicates: duplicates,
	}

	queue := &testQueue{}
	idStore := &testIdempotency{setNX: false}
	secret := []byte("dup-secret")
	handler := NewCallbackHandler(queue, idStore, nil, secret, metrics)

	requestID := "req-dup-inflight"
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(requestID + ":" + strconv.FormatInt(ts, 10)))
	signature := hex.EncodeToString(mac.Sum(nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/callback", nil)
	handler.HandleHumanCallback(rec, req, requestID, ts, signature, &HumanResponse{SelectedOption: "approve"})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if queue.lastJob != nil {
		t.Fatal("expected no enqueue when processing lock already exists")
	}
	if duplicates.val != 1 {
		t.Fatalf("callback_duplicates = %v, want 1", duplicates.val)
	}
}

func TestResumptionWorkerObservability_RetryableFailure(t *testing.T) {
	resumeFailures := &testCounter{}
	retries := &testCounter{}
	busy := &testGauge{}
	metrics := &HITLMetrics{
		ResumeFailures:         resumeFailures,
		ResumptionRetries:      retries,
		ResumptionWorkerBusy:   busy,
		CallbacksSuccessful:    &testCounter{},
		GraphVersionMismatches: &testCounter{},
	}

	cpStore := &testCheckpointStore{
		cp: &ExecutionCheckpoint{
			RequestID:    "req-retry",
			GraphID:      "g1",
			GraphVersion: "v1",
			NodeID:       "n1",
			StateData:    []byte(`{}`),
			FailureCount: 0,
		},
	}
	worker := NewResumptionWorker(
		&testQueue{},
		cpStore,
		&testIdempotency{setNX: true},
		&testGraphRegistry{},
		func(graphID, graphVersion string) ResumableExecutor {
			return &testExecutor{resumeErr: errors.New("timeout")}
		},
		metrics,
	)

	err := worker.processJob(context.Background(), &ResumptionJob{
		RequestID: "req-retry",
		JobID:     "job-1",
		Response:  &HumanResponse{SelectedOption: "ok"},
	})
	if err == nil {
		t.Fatal("expected retryable error from processJob")
	}
	if resumeFailures.val != 1 {
		t.Fatalf("resume_failures = %v, want 1", resumeFailures.val)
	}
	if retries.val != 1 {
		t.Fatalf("resumption_retries = %v, want 1", retries.val)
	}
	if busy.val != 0 {
		t.Fatalf("resumption_worker_busy gauge = %v, want 0 after completion", busy.val)
	}
}

func TestResumptionWorker_ContextCanceled_MapsToPermanentFailurePath(t *testing.T) {
	waiting := &testGauge{}
	moved := &testCounter{}
	metrics := &HITLMetrics{
		ActiveWaitingWorkflows: waiting,
		CheckpointsMovedToDLQ:  moved,
		ResumptionWorkerBusy:   &testGauge{},
		CallbacksSuccessful:    &testCounter{},
		ResumeFailures:         &testCounter{},
	}
	metrics.IncActiveWaitingWorkflows()

	cpStore := &testCheckpointStore{
		cp: &ExecutionCheckpoint{
			RequestID:    "req-canceled",
			GraphID:      "g1",
			GraphVersion: "v1",
			NodeID:       "n1",
			StateData:    []byte(`{}`),
		},
	}
	queue := &testQueue{}
	worker := NewResumptionWorker(
		queue,
		cpStore,
		&testIdempotency{setNX: true},
		&testGraphRegistry{},
		func(graphID, graphVersion string) ResumableExecutor { return &testExecutor{} },
		metrics,
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := worker.processJob(ctx, &ResumptionJob{
		RequestID: "req-canceled",
		JobID:     "job-canceled",
		Response:  &HumanResponse{SelectedOption: "ok"},
	})
	if err != nil {
		t.Fatalf("expected nil after permanent-failure DLQ handling, got %v", err)
	}
	if cpStore.moveCalls == 0 {
		t.Fatal("expected checkpoint moved to DLQ on canceled context path")
	}
	if queue.acked == 0 {
		t.Fatal("expected queue ack on permanent failure path")
	}
	if moved.val != 1 {
		t.Fatalf("checkpoints_moved_to_dlq = %v, want 1", moved.val)
	}
	if waiting.val != 0 {
		t.Fatalf("active waiting workflows = %v, want 0 after DLQ move", waiting.val)
	}
}

func TestResumptionWorkerObservability_MoveToDLQIncrementsMetric(t *testing.T) {
	moved := &testCounter{}
	metrics := &HITLMetrics{CheckpointsMovedToDLQ: moved}
	cpStore := &testCheckpointStore{}
	worker := NewResumptionWorker(
		&testQueue{},
		cpStore,
		&testIdempotency{setNX: true},
		&testGraphRegistry{},
		func(graphID, graphVersion string) ResumableExecutor { return &testExecutor{} },
		metrics,
	)

	worker.moveToDLQ(context.Background(), telemetry.GetGlobalTelemetry().GetTracer(), &ExecutionCheckpoint{RequestID: "req-dlq"}, "permanent failure", "")
	if moved.val != 1 {
		t.Fatalf("checkpoints_moved_to_dlq = %v, want 1", moved.val)
	}
	if cpStore.moveCalls != 1 {
		t.Fatalf("moveToCheckpointDLQ calls = %d, want 1", cpStore.moveCalls)
	}
}

func TestLoggingSanitization(t *testing.T) {
	in := map[string]interface{}{
		"request_id":    "req-1",
		"operation":     "callback.enqueue",
		"freeform_text": "secret human input",
		"payload":       map[string]interface{}{"email": "user@example.com"},
		"email":         "user@example.com",
	}

	out := safeLogFields(in)

	if _, ok := out["freeform_text"]; ok {
		t.Fatal("expected freeform_text to be redacted")
	}
	if _, ok := out["payload"]; ok {
		t.Fatal("expected payload to be redacted")
	}
	if _, ok := out["email"]; ok {
		t.Fatal("expected email to be redacted")
	}
	if got := out["request_id"]; got != "req-1" {
		t.Fatalf("expected request_id to remain, got %v", got)
	}
}

func TestQueueLengthReconciliation_SetsGaugeFromQueueProvider(t *testing.T) {
	queueLen := &testGauge{}
	metrics := &HITLMetrics{ResumptionQueueLength: queueLen}
	q := &testQueue{queueLen: 7}

	reconcileQueueLengthMetric(context.Background(), q, metrics)

	if queueLen.val != 7 {
		t.Fatalf("resumption queue length gauge = %v, want 7", queueLen.val)
	}
}

func TestTracePropagation_CallbackToWorker(t *testing.T) {
	received := &testCounter{}
	enqueued := &testCounter{}
	latency := &testHistogram{}
	metrics := &HITLMetrics{
		CallbacksReceived: received,
		CallbacksEnqueued: enqueued,
		CallbackLatency:   latency,
	}

	queue := &testQueue{queueLen: 1}
	idStore := &testIdempotency{setNX: true}
	secret := []byte("test-secret")
	handler := NewCallbackHandler(queue, idStore, nil, secret, metrics)

	requestID := "req-trace-1"
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(requestID + ":" + strconv.FormatInt(ts, 10)))
	signature := hex.EncodeToString(mac.Sum(nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/callback", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	handler.HandleHumanCallback(rec, req, requestID, ts, signature, &HumanResponse{SelectedOption: "ok"})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if queue.lastJob == nil {
		t.Fatal("expected enqueued job to be captured")
	}
	if queue.lastJob.TraceID == "" {
		t.Fatal("expected TraceID on enqueued job")
	}
	if queue.lastJob.TraceParent == "" {
		t.Fatal("expected TraceParent on enqueued job")
	}

	cpStore := &testCheckpointStore{
		cp: &ExecutionCheckpoint{
			RequestID:    requestID,
			GraphID:      "g1",
			GraphVersion: "v1",
			NodeID:       "n1",
			StateData:    []byte(`{}`),
			FailureCount: 0,
		},
	}
	worker := NewResumptionWorker(
		&testQueue{},
		cpStore,
		&testIdempotency{setNX: true},
		&testGraphRegistry{},
		func(graphID, graphVersion string) ResumableExecutor { return &testExecutor{} },
		&HITLMetrics{CallbacksSuccessful: &testCounter{}},
	)

	if err := worker.processJob(context.Background(), queue.lastJob); err != nil {
		t.Fatalf("processJob failed: %v", err)
	}
	if queue.lastJob.TraceID == "" {
		t.Fatal("expected TraceID to remain set through worker processing")
	}
}

func TestHITL_EndToEnd_CallbackQueueResumeFinish(t *testing.T) {
	var tailExecuted bool
	var selectedOption string

	g := graph.NewGraphWithConfig(graph.GraphConfig{
		ID:   "g-hitl-e2e",
		Name: "hitl-e2e",
		Metadata: map[string]interface{}{
			"graph_version": "v1",
		},
	})
	if err := g.AddNode(graph.NewFunctionNode("human", func(ctx context.Context, state graph.State) error { return nil })); err != nil {
		t.Fatalf("add human node: %v", err)
	}
	if err := g.AddNode(graph.NewFunctionNode("tail", func(ctx context.Context, state graph.State) error {
		raw, ok := state.Get(fmt.Sprintf(StateKeyHumanPendingFmt, "human"))
		if !ok {
			return fmt.Errorf("missing pending human response payload")
		}
		payload, ok := raw.([]byte)
		if !ok {
			return fmt.Errorf("pending response payload type=%T, want []byte", raw)
		}
		var resp HumanResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return fmt.Errorf("decode pending response: %w", err)
		}
		selectedOption = resp.SelectedOption
		tailExecuted = true
		state.Set("tail_done", true)
		return nil
	})); err != nil {
		t.Fatalf("add tail node: %v", err)
	}
	if err := g.AddEdge(graph.NewDirectEdge("human", "tail")); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if err := g.SetEntry("human"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("tail"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	baseState := graph.NewMemoryState()
	stateData, err := baseState.Marshal()
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	requestID := "req-hitl-e2e-1"
	cpStore := &testCheckpointStore{
		cp: &ExecutionCheckpoint{
			RequestID:    requestID,
			GraphID:      "g-hitl-e2e",
			GraphVersion: "v1",
			NodeID:       "human",
			StateData:    stateData,
		},
	}
	queue := &testQueue{queueLen: 1}
	callbackStore := &testIdempotency{setNX: true}
	workerStore := &testIdempotency{setNX: true}
	metrics := &HITLMetrics{
		CallbacksReceived:      &testCounter{},
		CallbacksEnqueued:      &testCounter{},
		CallbacksSuccessful:    &testCounter{},
		CallbackLatency:        &testHistogram{},
		ResumptionWorkerBusy:   &testGauge{},
		ResumptionQueueLength:  &testGauge{},
		ActiveWaitingWorkflows: &testGauge{},
	}
	metrics.IncActiveWaitingWorkflows()

	secret := []byte("hitl-e2e-secret")
	handler := NewCallbackHandler(queue, callbackStore, nil, secret, metrics)

	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(requestID + ":" + strconv.FormatInt(ts, 10)))
	signature := hex.EncodeToString(mac.Sum(nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/callback", nil)
	handler.HandleHumanCallback(rec, req, requestID, ts, signature, &HumanResponse{SelectedOption: "approve"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("callback status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if queue.lastJob == nil {
		t.Fatal("expected callback to enqueue a resumption job")
	}

	worker := NewResumptionWorker(
		queue,
		cpStore,
		workerStore,
		&testExecutableGraphRegistry{
			definition: GraphDefinition{ID: "g-hitl-e2e", Version: "v1"},
			executable: g,
		},
		nil,
		metrics,
	)

	if err := worker.processJob(context.Background(), queue.lastJob); err != nil {
		t.Fatalf("processJob failed: %v", err)
	}
	if !tailExecuted {
		t.Fatal("expected resumed graph tail node to execute")
	}
	if selectedOption != "approve" {
		t.Fatalf("selected_option = %q, want %q", selectedOption, "approve")
	}
	if cpStore.deleteCalls == 0 {
		t.Fatal("expected checkpoint deletion after successful resume")
	}
	if queue.acked == 0 {
		t.Fatal("expected queue ack after successful resume")
	}
	if got := metrics.ActiveWaitingWorkflows.(*testGauge).val; got != 0 {
		t.Fatalf("active waiting workflows = %v, want 0", got)
	}
	if got := metrics.CallbacksSuccessful.(*testCounter).val; got != 1 {
		t.Fatalf("callbacks_successful = %v, want 1", got)
	}
}

func TestResumptionWorker_UsesGraphNativeResume(t *testing.T) {
	g := graph.NewGraphWithConfig(graph.GraphConfig{
		ID:   "g-native-resume",
		Name: "native-resume",
		Metadata: map[string]interface{}{
			"graph_version": "v1",
		},
	})
	if err := g.AddNode(graph.NewFunctionNode("human", func(ctx context.Context, state graph.State) error { return nil })); err != nil {
		t.Fatalf("add human node: %v", err)
	}
	if err := g.AddNode(graph.NewFunctionNode("tail", func(ctx context.Context, state graph.State) error {
		state.Set("tail_done", true)
		return nil
	})); err != nil {
		t.Fatalf("add tail node: %v", err)
	}
	if err := g.AddEdge(graph.NewDirectEdge("human", "tail")); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if err := g.SetEntry("human"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("tail"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	state := graph.NewMemoryState()
	stateData, err := state.Marshal()
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	cpStore := &testCheckpointStore{
		cp: &ExecutionCheckpoint{
			RequestID:    "req-native",
			GraphID:      "g-native-resume",
			GraphVersion: "v1",
			NodeID:       "human",
			StateData:    stateData,
			FailureCount: 0,
		},
	}
	metrics := &HITLMetrics{
		ResumptionWorkerBusy:   &testGauge{},
		CallbacksSuccessful:    &testCounter{},
		ActiveWaitingWorkflows: &testGauge{},
	}
	metrics.IncActiveWaitingWorkflows()

	worker := NewResumptionWorker(
		&testQueue{},
		cpStore,
		&testIdempotency{setNX: true},
		&testExecutableGraphRegistry{
			definition: GraphDefinition{ID: "g-native-resume", Version: "v1"},
			executable: g,
		},
		nil, // graph-native path should not require fallback executor
		metrics,
	)

	job := &ResumptionJob{
		RequestID: "req-native",
		JobID:     "job-native",
		Response:  &HumanResponse{SelectedOption: "approve"},
	}
	if err := worker.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob failed: %v", err)
	}
	if cpStore.deleteCalls == 0 {
		t.Fatal("expected checkpoint delete on successful graph-native resume")
	}
}

func TestResumptionWorker_GraphNativeResumeNestedPauseCreatesCheckpoint(t *testing.T) {
	g := graph.NewGraphWithConfig(graph.GraphConfig{
		ID:   "g-native-nested",
		Name: "native-nested",
		Metadata: map[string]interface{}{
			"graph_version": "v1",
		},
	})
	if err := g.AddNode(graph.NewFunctionNode("human1", func(ctx context.Context, state graph.State) error { return nil })); err != nil {
		t.Fatalf("add human1 node: %v", err)
	}
	if err := g.AddNode(graph.NewFunctionNode("human2", func(ctx context.Context, state graph.State) error {
		state.Set(StateKeyHumanTimeout, "1m")
		return graph.NewPauseSignal(graph.PausedResult{
			RequestID:    "req-next",
			NodeID:       "human2",
			GraphVersion: "v1",
		}, nil)
	})); err != nil {
		t.Fatalf("add human2 node: %v", err)
	}
	if err := g.AddEdge(graph.NewDirectEdge("human1", "human2")); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if err := g.SetEntry("human1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("human2"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	state := graph.NewMemoryState()
	stateData, err := state.Marshal()
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	cpStore := &testCheckpointStore{
		cp: &ExecutionCheckpoint{
			RequestID:    "req-old",
			GraphID:      "g-native-nested",
			GraphVersion: "v1",
			NodeID:       "human1",
			StateData:    stateData,
			FailureCount: 0,
		},
	}
	waiting := &testGauge{}
	metrics := &HITLMetrics{
		ResumptionWorkerBusy:   &testGauge{},
		CallbacksSuccessful:    &testCounter{},
		ActiveWaitingWorkflows: waiting,
		CheckpointCreations:    &testCounter{},
		CheckpointSizeBytes:    &testHistogram{},
	}
	metrics.IncActiveWaitingWorkflows()

	queue := &testQueue{}
	worker := NewResumptionWorker(
		queue,
		cpStore,
		&testIdempotency{setNX: true},
		&testExecutableGraphRegistry{
			definition: GraphDefinition{ID: "g-native-nested", Version: "v1"},
			executable: g,
		},
		nil,
		metrics,
	)

	job := &ResumptionJob{
		RequestID: "req-old",
		JobID:     "job-nested",
		Response:  &HumanResponse{SelectedOption: "approve"},
	}
	if err := worker.processJob(context.Background(), job); err != nil {
		t.Fatalf("processJob failed: %v", err)
	}
	if cpStore.replaceCalls == 0 && cpStore.createCalls == 0 {
		t.Fatal("expected nested pause checkpoint persistence via replace or create")
	}
	if cpStore.replaceCalls == 0 && cpStore.deleteCalls == 0 {
		t.Fatal("expected original checkpoint deletion when fallback create/delete path is used")
	}
	if got := waiting.val; got != 1 {
		t.Fatalf("active waiting workflows = %v, want 1 (unchanged across nested pause)", got)
	}
	if queue.acked == 0 {
		t.Fatal("expected queue ack for nested pause path")
	}
}

func TestResumeGraphFromCheckpoint_GraphNativeNestedPauseReturnsPending(t *testing.T) {
	g := graph.NewGraphWithConfig(graph.GraphConfig{
		ID:   "g-orch-native",
		Name: "orch-native",
		Metadata: map[string]interface{}{
			"graph_version": "v1",
		},
	})
	if err := g.AddNode(graph.NewFunctionNode("human1", func(ctx context.Context, state graph.State) error { return nil })); err != nil {
		t.Fatalf("add human1 node: %v", err)
	}
	if err := g.AddNode(graph.NewFunctionNode("human2", func(ctx context.Context, state graph.State) error {
		state.Set(StateKeyHumanTimeout, "2m")
		return graph.NewPauseSignal(graph.PausedResult{
			RequestID:    "req-nested-next",
			NodeID:       "human2",
			GraphVersion: "v1",
		}, nil)
	})); err != nil {
		t.Fatalf("add human2 node: %v", err)
	}
	if err := g.AddEdge(graph.NewDirectEdge("human1", "human2")); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if err := g.SetEntry("human1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("human2"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	state := graph.NewMemoryState()
	stateData, err := state.Marshal()
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	cpStore := &testCheckpointStore{
		cp: &ExecutionCheckpoint{
			RequestID:    "req-orch-old",
			GraphID:      "g-orch-native",
			GraphVersion: "v1",
			NodeID:       "human1",
			StateData:    stateData,
		},
	}
	idStore := &testIdempotency{}
	graphRegistry := &testExecutableGraphRegistry{
		definition: GraphDefinition{ID: "g-orch-native", Version: "v1"},
		executable: g,
	}

	err = ResumeGraphFromCheckpoint(
		"req-orch-old",
		&HumanResponse{SelectedOption: "approve"},
		cpStore,
		idStore,
		graphRegistry,
		nil,
	)
	if !errors.Is(err, ErrWorkflowPending) {
		t.Fatalf("expected ErrWorkflowPending, got %v", err)
	}
	if cpStore.replaceCalls == 0 && cpStore.createCalls == 0 {
		t.Fatal("expected nested pause checkpoint persistence via replace or create")
	}
	if cpStore.replaceCalls == 0 && cpStore.deleteCalls == 0 {
		t.Fatal("expected original checkpoint deletion when fallback create/delete path is used")
	}
	if idStore.setCalls == 0 {
		t.Fatal("expected idempotency marker set for processed callback")
	}
}

func TestResumeFromCheckpoint_FallbackExecutorReturnsNonNilResult(t *testing.T) {
	cp := &ExecutionCheckpoint{
		RequestID:    "req-fallback",
		GraphID:      "g-fallback",
		GraphVersion: "v1",
		NodeID:       "n1",
	}
	state := graph.NewMemoryState()
	result, err := resumeFromCheckpoint(context.Background(), cp, state, &testGraphRegistry{}, func(graphID, graphVersion string) ResumableExecutor {
		return &testExecutor{}
	})
	if err != nil {
		t.Fatalf("resumeFromCheckpoint error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil execution result for fallback executor path")
	}
	if result.IsPaused() {
		t.Fatalf("expected fallback executor path to return non-paused result, got %#v", result.Paused)
	}
}

func TestCheckpointExpiryForState_ClampsBounds(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()

	low := graph.NewMemoryState()
	low.Set(StateKeyHumanTimeout, "-100h")
	lowExpiry := checkpointExpiryForState(now, low)
	if want := now.Add(minCheckpointLifetime); !lowExpiry.Equal(want) {
		t.Fatalf("low timeout expiry = %s, want %s", lowExpiry, want)
	}

	high := graph.NewMemoryState()
	high.Set(StateKeyHumanTimeout, "9999h")
	highExpiry := checkpointExpiryForState(now, high)
	if want := now.Add(maxCheckpointLifetime); !highExpiry.Equal(want) {
		t.Fatalf("high timeout expiry = %s, want %s", highExpiry, want)
	}
}

func TestPersistNestedPauseCheckpoint_UsesAtomicReplaceWhenAvailable(t *testing.T) {
	baseState := graph.NewMemoryState()
	baseState.Set(StateKeyHumanTimeout, "5m")
	baseState.Set("k", "v")

	cp := &ExecutionCheckpoint{
		RequestID:    "req-old",
		GraphID:      "g1",
		GraphVersion: "v1",
		NodeID:       "human1",
	}
	paused := &graph.PausedResult{
		RequestID:    "req-new",
		NodeID:       "human2",
		GraphVersion: "v1",
	}
	store := &testCheckpointStore{}
	next, err := persistNestedPauseCheckpoint(context.Background(), store, cp, baseState, paused)
	if err != nil {
		t.Fatalf("persistNestedPauseCheckpoint error: %v", err)
	}
	if next == nil {
		t.Fatal("expected non-nil replacement checkpoint")
	}
	if store.replaceCalls != 1 {
		t.Fatalf("replace calls = %d, want 1", store.replaceCalls)
	}
	if store.createCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("expected atomic replace path only, got create=%d delete=%d", store.createCalls, store.deleteCalls)
	}
}

func TestPersistNestedPauseCheckpoint_FallbackDeleteFailureIsNonFatal(t *testing.T) {
	baseState := graph.NewMemoryState()
	baseState.Set(StateKeyHumanTimeout, "5m")

	cp := &ExecutionCheckpoint{
		RequestID:    "req-old-fallback",
		GraphID:      "g1",
		GraphVersion: "v1",
		NodeID:       "human1",
	}
	paused := &graph.PausedResult{
		RequestID:    "req-new-fallback",
		NodeID:       "human2",
		GraphVersion: "v1",
	}
	inner := &testCheckpointStore{deleteErr: errors.New("simulated delete failure")}
	store := &testCheckpointStoreNoReplace{inner: inner}

	next, err := persistNestedPauseCheckpoint(context.Background(), store, cp, baseState, paused)
	if err != nil {
		t.Fatalf("expected non-fatal delete failure in fallback path, got error: %v", err)
	}
	if next == nil {
		t.Fatal("expected non-nil replacement checkpoint")
	}
	if inner.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", inner.createCalls)
	}
	if inner.deleteCalls != 0 {
		t.Fatalf("delete calls counter = %d, want 0 when delete returns error", inner.deleteCalls)
	}
}

func TestMarshalGraphState_RequiresMarshalableState(t *testing.T) {
	state := noMarshalState{State: graph.NewMemoryState()}
	if _, err := marshalGraphState(state); err == nil {
		t.Fatal("expected marshalGraphState to fail for non-marshalable state")
	}
}

func TestExecuteGraphWithMetrics_CheckpointEmission(t *testing.T) {
	creates := &testCounter{}
	lat := &testHistogram{}
	size := &testHistogram{}
	waiting := &testGauge{}

	metrics := &HITLMetrics{
		CheckpointCreations:       creates,
		CheckpointCreationLatency: lat,
		CheckpointSizeBytes:       size,
		ActiveWaitingWorkflows:    waiting,
	}

	state := graph.NewMemoryState()
	state.Set(StateKeyHumanRequestID, "req-orch-1")
	state.Set(StateKeyHumanTimeout, "5m")
	state.Set("sample", "data")

	exec := &testOrchestratorExecutor{
		currentErr: ErrHumanInteractionPending,
		nodeID:     "human-node-1",
	}
	store := &testCheckpointStore{}
	_, err := ExecuteGraphWithMetrics("g1", "v1", state, exec, store, nil, metrics)
	if !errors.Is(err, ErrWorkflowPending) {
		t.Fatalf("expected ErrWorkflowPending, got %v", err)
	}

	if creates.val != 1 {
		t.Fatalf("checkpoint creations = %v, want 1", creates.val)
	}
	if lat.observations == 0 {
		t.Fatal("expected checkpoint creation latency observation")
	}
	if size.observations == 0 {
		t.Fatal("expected checkpoint size observation")
	}
	if waiting.val != 1 {
		t.Fatalf("active waiting workflows = %v, want 1", waiting.val)
	}
}

func TestResumptionWorkerActiveWaiting_RetryableErrorDoesNotDecrement(t *testing.T) {
	waiting := &testGauge{}
	metrics := &HITLMetrics{
		ActiveWaitingWorkflows: waiting,
		ResumeFailures:         &testCounter{},
		ResumptionRetries:      &testCounter{},
		ResumptionWorkerBusy:   &testGauge{},
		CallbacksSuccessful:    &testCounter{},
		GraphVersionMismatches: &testCounter{},
	}
	metrics.IncActiveWaitingWorkflows() // simulate pending workflow

	cpStore := &testCheckpointStore{
		cp: &ExecutionCheckpoint{
			RequestID:    "req-retry-waiting",
			GraphID:      "g1",
			GraphVersion: "v1",
			NodeID:       "n1",
			StateData:    []byte(`{}`),
			FailureCount: 0,
		},
	}
	worker := NewResumptionWorker(
		&testQueue{},
		cpStore,
		&testIdempotency{setNX: true},
		&testGraphRegistry{},
		func(graphID, graphVersion string) ResumableExecutor {
			return &testExecutor{resumeErr: errors.New("timeout")}
		},
		metrics,
	)

	err := worker.processJob(context.Background(), &ResumptionJob{
		RequestID: "req-retry-waiting",
		JobID:     "job-retry-waiting",
		Response:  &HumanResponse{SelectedOption: "ok"},
	})
	if err == nil {
		t.Fatal("expected retryable error")
	}
	if waiting.val != 1 {
		t.Fatalf("active waiting workflows = %v, want 1 (unchanged on retryable error)", waiting.val)
	}
}

func TestResumptionWorkerActiveWaiting_CheckpointNotFoundDoesNotDecrement(t *testing.T) {
	waiting := &testGauge{}
	metrics := &HITLMetrics{
		ActiveWaitingWorkflows: waiting,
		ResumptionWorkerBusy:   &testGauge{},
		CallbacksSuccessful:    &testCounter{},
	}
	metrics.IncActiveWaitingWorkflows() // simulate pending workflow

	worker := NewResumptionWorker(
		&testQueue{},
		&testCheckpointStore{cp: nil}, // forces ErrCheckpointNotFound
		&testIdempotency{setNX: true},
		&testGraphRegistry{},
		func(graphID, graphVersion string) ResumableExecutor { return &testExecutor{} },
		metrics,
	)

	err := worker.processJob(context.Background(), &ResumptionJob{
		RequestID: "req-not-found-waiting",
		JobID:     "job-not-found-waiting",
		Response:  &HumanResponse{SelectedOption: "ok"},
	})
	if err != nil {
		t.Fatalf("expected nil for checkpoint-not-found ack path, got %v", err)
	}
	if waiting.val != 1 {
		t.Fatalf("active waiting workflows = %v, want 1 (unchanged on checkpoint-not-found)", waiting.val)
	}
}

func TestDLQSizeGauge_AtomicClamp(t *testing.T) {
	dlq := &testGauge{}
	metrics := &HITLMetrics{DLQSize: dlq}

	metrics.IncDLQSize()
	metrics.IncDLQSize()
	if dlq.val != 2 {
		t.Fatalf("dlq size = %v, want 2", dlq.val)
	}

	metrics.DecDLQSize()
	if dlq.val != 1 {
		t.Fatalf("dlq size after dec = %v, want 1", dlq.val)
	}

	metrics.DecDLQSize()
	metrics.DecDLQSize() // over-decrement should clamp to zero
	if dlq.val != 0 {
		t.Fatalf("dlq size after over-dec = %v, want 0", dlq.val)
	}
}

func TestActiveWaitingGauge_AtomicClamp(t *testing.T) {
	waiting := &testGauge{}
	metrics := &HITLMetrics{ActiveWaitingWorkflows: waiting}

	metrics.IncActiveWaitingWorkflows()
	metrics.IncActiveWaitingWorkflows()
	if waiting.val != 2 {
		t.Fatalf("active waiting = %v, want 2", waiting.val)
	}

	metrics.DecActiveWaitingWorkflows()
	if waiting.val != 1 {
		t.Fatalf("active waiting after dec = %v, want 1", waiting.val)
	}

	metrics.DecActiveWaitingWorkflows()
	metrics.DecActiveWaitingWorkflows() // over-decrement should clamp to zero
	if waiting.val != 0 {
		t.Fatalf("active waiting after over-dec = %v, want 0", waiting.val)
	}
}
