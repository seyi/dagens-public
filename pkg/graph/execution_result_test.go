package graph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seyi/dagens/pkg/telemetry"
)

type testClonableNode struct {
	*BaseNode
	payload *int
}

func newTestClonableNode(id string, payload *int) *testClonableNode {
	return &testClonableNode{
		BaseNode: NewBaseNode(id, "test-clonable"),
		payload:  payload,
	}
}

func (n *testClonableNode) Execute(ctx context.Context, state State) error { return nil }

func (n *testClonableNode) Clone() Node {
	var cp *int
	if n.payload != nil {
		v := *n.payload
		cp = &v
	}
	return &testClonableNode{
		BaseNode: NewBaseNode(n.ID(), n.Type()),
		payload:  cp,
	}
}

type testClonableEdge struct {
	from    string
	to      string
	payload *int
}

func (e *testClonableEdge) From() string                       { return e.from }
func (e *testClonableEdge) To() string                         { return e.to }
func (e *testClonableEdge) Type() string                       { return "test-clonable-edge" }
func (e *testClonableEdge) ShouldTraverse(state State) bool    { return true }
func (e *testClonableEdge) Metadata() map[string]interface{}   { return map[string]interface{}{} }
func (e *testClonableEdge) Clone() Edge {
	var cp *int
	if e.payload != nil {
		v := *e.payload
		cp = &v
	}
	return &testClonableEdge{from: e.from, to: e.to, payload: cp}
}

// NOTE: This file intentionally avoids t.Parallel() in tests that mutate
// package-global graphExecutionContextFactory.

func TestExecuteWithResult_ReturnsPausedResult(t *testing.T) {
	g := NewGraph("hitl-contract")
	g.SetMetadata("graph_version", "v1.2.3")

	pauseNode := NewFunctionNode("human-review", func(ctx context.Context, state State) error {
		state.Set(PauseRequestIDStateKey, "req-123")
		return errors.New("human interaction pending: checkpoint required")
	})

	if err := g.AddNode(pauseNode); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("human-review"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("human-review"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	state := NewMemoryState()
	result, err := g.ExecuteWithResult(context.Background(), state)
	if err != nil {
		t.Fatalf("execute with result returned error: %v", err)
	}
	if result == nil || !result.IsPaused() {
		t.Fatalf("expected paused result, got %#v", result)
	}
	if result.Paused.RequestID != "req-123" {
		t.Fatalf("request_id = %q, want req-123", result.Paused.RequestID)
	}
	if result.Paused.NodeID != "human-review" {
		t.Fatalf("node_id = %q, want human-review", result.Paused.NodeID)
	}
	if result.Paused.GraphVersion != "v1.2.3" {
		t.Fatalf("graph_version = %q, want v1.2.3", result.Paused.GraphVersion)
	}
}

func TestExecuteWithResult_ReturnsPausedResultFromTypedSignal(t *testing.T) {
	g := NewGraph("typed-pause")
	g.SetMetadata("graph_version", "v2")

	pauseNode := NewFunctionNode("human-typed", func(ctx context.Context, state State) error {
		return NewPauseSignal(PausedResult{
			RequestID: "req-typed",
			NodeID:    "human-typed",
		}, errors.New("pause"))
	})
	if err := g.AddNode(pauseNode); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("human-typed"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("human-typed"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	result, err := g.ExecuteWithResult(context.Background(), NewMemoryState())
	if err != nil {
		t.Fatalf("execute with result returned error: %v", err)
	}
	if result == nil || !result.IsPaused() {
		t.Fatalf("expected paused result, got %#v", result)
	}
	if result.Paused.RequestID != "req-typed" {
		t.Fatalf("request_id = %q, want req-typed", result.Paused.RequestID)
	}
	if result.Paused.NodeID != "human-typed" {
		t.Fatalf("node_id = %q, want human-typed", result.Paused.NodeID)
	}
	if result.Paused.GraphVersion != "v2" {
		t.Fatalf("graph_version = %q, want v2", result.Paused.GraphVersion)
	}
}

func TestExecuteWithResult_ReturnsErrorForNonPauseFailure(t *testing.T) {
	g := NewGraph("failure")

	failNode := NewFunctionNode("n1", func(ctx context.Context, state State) error {
		return errors.New("boom")
	})
	if err := g.AddNode(failNode); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("n1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("n1"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	result, err := g.ExecuteWithResult(context.Background(), NewMemoryState())
	if err == nil {
		t.Fatalf("expected error, got result %#v", result)
	}
	var pauseSig PauseSignal
	if errors.As(err, &pauseSig) {
		t.Fatalf("expected non-pause error, got pause signal: %v", err)
	}
}

func TestExecuteWithResult_PausedResultIncludesTraceContext(t *testing.T) {
	g := NewGraph("trace-pause")
	pauseNode := NewFunctionNode("human", func(ctx context.Context, state State) error {
		state.Set(PauseRequestIDStateKey, "req-trace-pause")
		return errors.New("human interaction pending: checkpoint required")
	})
	if err := g.AddNode(pauseNode); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("human"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("human"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	ctx, span := tracer.StartSpan(context.Background(), "graph-test.pause")
	defer span.End()

	result, err := g.ExecuteWithResult(ctx, NewMemoryState())
	if err != nil {
		t.Fatalf("execute with result returned error: %v", err)
	}
	if result == nil || !result.IsPaused() {
		t.Fatalf("expected paused result, got %#v", result)
	}
	if result.Paused.TraceID == "" {
		t.Fatal("expected paused result trace_id")
	}
	if result.Paused.TraceID != span.TraceID() {
		t.Fatalf("trace_id = %q, want %q", result.Paused.TraceID, span.TraceID())
	}
	if result.Paused.TraceParent == "" {
		t.Fatal("expected paused result trace_parent")
	}
}

func TestExecuteWithResult_LogsValidationFailureStructured(t *testing.T) {
	logger, ok := telemetry.GetGlobalTelemetry().GetLogger().(*telemetry.InMemoryLogger)
	if !ok {
		t.Skip("global logger is not InMemoryLogger")
	}
	before := logger.GetLogs()

	g := NewGraph("validation-log")
	state := NewMemoryState()
	state.Set(PauseRequestIDStateKey, "req-validation")

	_, err := g.ExecuteWithResult(context.Background(), state)
	if err == nil {
		t.Fatal("expected validation error")
	}

	after := logger.GetLogs()
	found := false
	for _, entry := range after[len(before):] {
		if entry.Message != "graph validation failed" {
			continue
		}
		if got, _ := entry.Attributes["operation"].(string); got != "graph.execute.validate" {
			continue
		}
		if got, _ := entry.Attributes["graph_id"].(string); got != g.ID() {
			continue
		}
		if got, _ := entry.Attributes["request_id"].(string); got != "req-validation" {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Fatal("expected structured validation failure log entry with operation/graph_id/request_id")
	}
}

func TestExecuteWithResult_LogsExecutionFailureStructured(t *testing.T) {
	logger, ok := telemetry.GetGlobalTelemetry().GetLogger().(*telemetry.InMemoryLogger)
	if !ok {
		t.Skip("global logger is not InMemoryLogger")
	}
	before := logger.GetLogs()

	g := NewGraph("run-log")
	failNode := NewFunctionNode("n1", func(ctx context.Context, state State) error {
		return errors.New("boom-run")
	})
	if err := g.AddNode(failNode); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("n1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("n1"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	state := NewMemoryState()
	state.Set(PauseRequestIDStateKey, "req-run-fail")
	_, err := g.ExecuteWithResult(context.Background(), state)
	if err == nil {
		t.Fatal("expected execution error")
	}

	after := logger.GetLogs()
	found := false
	for _, entry := range after[len(before):] {
		if entry.Message != "graph execution failed" {
			continue
		}
		if got, _ := entry.Attributes["operation"].(string); got != "graph.execute.run" {
			continue
		}
		if got, _ := entry.Attributes["graph_id"].(string); got != g.ID() {
			continue
		}
		if got, _ := entry.Attributes["request_id"].(string); got != "req-run-fail" {
			continue
		}
		if got, _ := entry.Attributes["error"].(string); !strings.Contains(got, "boom-run") {
			continue
		}
		found = true
		break
	}
	if !found {
		t.Fatal("expected structured execution failure log entry with operation/graph_id/request_id/error")
	}
}

func TestExecuteWithResult_EmitsGraphNeutralHooks(t *testing.T) {
	var pauseEvent GraphPauseMetricsEvent
	var failureEvent GraphFailureMetricsEvent
	restore := SetGraphMetricsHooks(GraphMetricsHooks{
		OnPause: func(e GraphPauseMetricsEvent) { pauseEvent = e },
		OnFailure: func(e GraphFailureMetricsEvent) {
			failureEvent = e
		},
	})
	t.Cleanup(restore)

	gPause := NewGraph("hook-pause")
	gPause.SetMetadata("graph_version", "v-hook")
	if err := gPause.AddNode(NewFunctionNode("human", func(ctx context.Context, state State) error {
		state.Set(PauseRequestIDStateKey, "req-hook-pause")
		return errors.New("human interaction pending: checkpoint required")
	})); err != nil {
		t.Fatalf("add pause node: %v", err)
	}
	if err := gPause.SetEntry("human"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := gPause.AddFinish("human"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	result, err := gPause.ExecuteWithResult(context.Background(), NewMemoryState())
	if err != nil {
		t.Fatalf("pause execute failed: %v", err)
	}
	if result == nil || !result.IsPaused() {
		t.Fatalf("expected paused result, got %#v", result)
	}
	if pauseEvent.GraphID != gPause.ID() || pauseEvent.RequestID != "req-hook-pause" || pauseEvent.NodeID != "human" {
		t.Fatalf("unexpected pause hook event: %#v", pauseEvent)
	}

	gFail := NewGraph("hook-fail")
	if err := gFail.AddNode(NewFunctionNode("n1", func(ctx context.Context, state State) error {
		return errors.New("hook-failure")
	})); err != nil {
		t.Fatalf("add fail node: %v", err)
	}
	if err := gFail.SetEntry("n1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := gFail.AddFinish("n1"); err != nil {
		t.Fatalf("add finish: %v", err)
	}
	state := NewMemoryState()
	state.Set(PauseRequestIDStateKey, "req-hook-fail")
	if _, err := gFail.ExecuteWithResult(context.Background(), state); err == nil {
		t.Fatal("expected execution failure")
	}
	if failureEvent.GraphID != gFail.ID() {
		t.Fatalf("failure hook graph_id = %q, want %q", failureEvent.GraphID, gFail.ID())
	}
	if failureEvent.RequestID != "req-hook-fail" {
		t.Fatalf("failure hook request_id = %q, want req-hook-fail", failureEvent.RequestID)
	}
	if failureEvent.Operation != "graph.execute.run" {
		t.Fatalf("failure hook operation = %q, want graph.execute.run", failureEvent.Operation)
	}
	if !strings.Contains(failureEvent.Error, "hook-failure") {
		t.Fatalf("failure hook error = %q, want contains hook-failure", failureEvent.Error)
	}
}

func TestGraphClone_DeepCopy_WhenClonableNodeImplemented(t *testing.T) {
	g := NewGraph("clone-node")
	nodePayload := 42
	n1 := newTestClonableNode("n1", &nodePayload)
	if err := g.AddNode(n1); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("n1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("n1"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	cloned := g.Clone()
	clonedNodeRaw, err := cloned.GetNode("n1")
	if err != nil {
		t.Fatalf("get cloned node: %v", err)
	}
	clonedNode, ok := clonedNodeRaw.(*testClonableNode)
	if !ok {
		t.Fatalf("expected *testClonableNode, got %T", clonedNodeRaw)
	}
	if clonedNode == n1 {
		t.Fatal("expected cloned node instance to differ from original")
	}
	if clonedNode.payload == nil || n1.payload == nil {
		t.Fatal("expected payload pointers")
	}
	if clonedNode.payload == n1.payload {
		t.Fatal("expected cloned node payload pointer to differ from original")
	}

	*clonedNode.payload = 999
	if *n1.payload != 42 {
		t.Fatalf("original node payload mutated: got %d, want 42", *n1.payload)
	}
}

func TestGraphClone_DeepCopy_WhenClonableEdgeImplemented(t *testing.T) {
	g := NewGraph("clone-edge")
	n1 := NewFunctionNode("n1", func(ctx context.Context, state State) error { return nil })
	n2 := NewFunctionNode("n2", func(ctx context.Context, state State) error { return nil })
	if err := g.AddNode(n1); err != nil {
		t.Fatalf("add n1: %v", err)
	}
	if err := g.AddNode(n2); err != nil {
		t.Fatalf("add n2: %v", err)
	}
	edgePayload := 7
	e := &testClonableEdge{from: "n1", to: "n2", payload: &edgePayload}
	if err := g.AddEdge(e); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if err := g.SetEntry("n1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("n2"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	cloned := g.Clone()
	edges := cloned.GetEdges("n1")
	if len(edges) != 1 {
		t.Fatalf("cloned edge count = %d, want 1", len(edges))
	}
	clonedEdge, ok := edges[0].(*testClonableEdge)
	if !ok {
		t.Fatalf("expected *testClonableEdge, got %T", edges[0])
	}
	if clonedEdge == e {
		t.Fatal("expected cloned edge instance to differ from original")
	}
	if clonedEdge.payload == nil || e.payload == nil {
		t.Fatal("expected payload pointers")
	}
	if clonedEdge.payload == e.payload {
		t.Fatal("expected cloned edge payload pointer to differ from original")
	}

	*clonedEdge.payload = 21
	if *e.payload != 7 {
		t.Fatalf("original edge payload mutated: got %d, want 7", *e.payload)
	}
}

func TestSetGraphMetricsHooks_Concurrent(t *testing.T) {
	g := NewGraph("hooks-concurrent")
	if err := g.AddNode(NewFunctionNode("human", func(ctx context.Context, state State) error {
		state.Set(PauseRequestIDStateKey, "req-concurrent")
		return errors.New("human interaction pending: checkpoint required")
	})); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("human"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("human"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	var seenPause atomic.Int64
	var seenFailure atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			restore := SetGraphMetricsHooks(GraphMetricsHooks{
				OnPause: func(GraphPauseMetricsEvent) {
					seenPause.Add(1)
				},
				OnFailure: func(GraphFailureMetricsEvent) {
					seenFailure.Add(1)
				},
			})
			defer restore()

			_, _ = g.ExecuteWithResult(context.Background(), NewMemoryState())
			_, _ = g.ExecuteWithResult(context.Background(), NewMemoryState())
			_, _ = g.ExecuteWithResult(context.Background(), NewMemoryState())
			_ = i
		}()
	}
	wg.Wait()

	if seenFailure.Load() != 0 {
		t.Fatalf("expected no failure hook emissions, got %d", seenFailure.Load())
	}
	if seenPause.Load() == 0 {
		t.Fatal("expected pause hook emissions in concurrent run")
	}
}

func TestTraverseNode_ContextCanceled_AbortsPromptly(t *testing.T) {
	g := NewGraph("ctx-abort-traverse")

	var childExecuted int
	ctx, cancel := context.WithCancel(context.Background())

	entry := NewFunctionNode("entry", func(ctx context.Context, state State) error {
		cancel()
		return nil
	})
	child := NewFunctionNode("child", func(ctx context.Context, state State) error {
		childExecuted++
		return nil
	})
	if err := g.AddNode(entry); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	if err := g.AddNode(child); err != nil {
		t.Fatalf("add child: %v", err)
	}
	if err := g.AddEdge(NewDirectEdge("entry", "child")); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if err := g.SetEntry("entry"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("child"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	_, err := g.ExecuteWithResult(ctx, NewMemoryState())
	if err == nil {
		t.Fatal("expected context canceled error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if childExecuted != 0 {
		t.Fatalf("child executed = %d, want 0 due to prompt cancellation", childExecuted)
	}
}

func TestResumeFromPaused_ContinuesWithoutReexecutingPausedNode(t *testing.T) {
	g := NewGraph("resume")
	g.SetMetadata("graph_version", "v9")

	var entryExec, pauseExec, tailExec int

	entry := NewFunctionNode("entry", func(ctx context.Context, state State) error {
		entryExec++
		return nil
	})
	pause := NewFunctionNode("human", func(ctx context.Context, state State) error {
		pauseExec++
		state.Set(PauseRequestIDStateKey, "req-77")
		return errors.New("human interaction pending: checkpoint required")
	})
	tail := NewFunctionNode("tail", func(ctx context.Context, state State) error {
		tailExec++
		return nil
	})

	for _, n := range []Node{entry, pause, tail} {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("add node %s: %v", n.ID(), err)
		}
	}
	if err := g.AddEdge(NewDirectEdge("entry", "human")); err != nil {
		t.Fatalf("add edge entry->human: %v", err)
	}
	if err := g.AddEdge(NewDirectEdge("human", "tail")); err != nil {
		t.Fatalf("add edge human->tail: %v", err)
	}
	if err := g.SetEntry("entry"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("tail"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	state := NewMemoryState()
	initial, err := g.ExecuteWithResult(context.Background(), state)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if initial == nil || !initial.IsPaused() {
		t.Fatalf("expected paused initial result, got %#v", initial)
	}
	if entryExec != 1 || pauseExec != 1 || tailExec != 0 {
		t.Fatalf("unexpected counts before resume: entry=%d pause=%d tail=%d", entryExec, pauseExec, tailExec)
	}

	resumed, err := g.ResumeFromPaused(context.Background(), state, initial.Paused)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed == nil {
		t.Fatal("expected non-nil resume result")
	}
	if resumed.IsPaused() {
		t.Fatalf("did not expect second pause, got %#v", resumed.Paused)
	}

	// Resume should continue from outgoing edges only.
	if entryExec != 1 || pauseExec != 1 || tailExec != 1 {
		t.Fatalf("unexpected counts after resume: entry=%d pause=%d tail=%d", entryExec, pauseExec, tailExec)
	}
}

func TestResumeFromPaused_RejectsUnknownNode(t *testing.T) {
	g := NewGraph("resume-invalid")
	n := NewFunctionNode("n1", func(ctx context.Context, state State) error { return nil })
	if err := g.AddNode(n); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("n1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("n1"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	_, err := g.ResumeFromPaused(context.Background(), NewMemoryState(), &PausedResult{
		RequestID:    "req-x",
		NodeID:       "missing",
		GraphVersion: "",
	})
	if err == nil {
		t.Fatal("expected error for missing paused node")
	}
}

func TestExecuteWithObservability_FallbackWhenContextInitFails(t *testing.T) {
	oldFactory := graphExecutionContextFactory
	graphExecutionContextFactory = func(ctx context.Context, graph *Graph, collector *telemetry.TelemetryCollector) (*GraphExecutionContext, error) {
		return nil, errors.New("forced context init failure")
	}
	t.Cleanup(func() { graphExecutionContextFactory = oldFactory })

	g := NewGraph("obs-fallback")
	var executed int
	n := NewFunctionNode("n1", func(ctx context.Context, state State) error {
		executed++
		return nil
	})
	if err := g.AddNode(n); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("n1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("n1"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	if err := g.ExecuteWithObservability(context.Background(), NewMemoryState(), nil); err != nil {
		t.Fatalf("execute with observability fallback error: %v", err)
	}
	if executed != 1 {
		t.Fatalf("node execution count = %d, want 1", executed)
	}
}

func TestExecuteWithObservability_FallbackReturnsPauseSignal(t *testing.T) {
	oldFactory := graphExecutionContextFactory
	graphExecutionContextFactory = func(ctx context.Context, graph *Graph, collector *telemetry.TelemetryCollector) (*GraphExecutionContext, error) {
		return nil, errors.New("forced context init failure")
	}
	t.Cleanup(func() { graphExecutionContextFactory = oldFactory })

	g := NewGraph("obs-fallback-pause")
	n := NewFunctionNode("human", func(ctx context.Context, state State) error {
		return NewPauseSignal(PausedResult{
			RequestID: "req-obs",
			NodeID:    "human",
		}, errors.New("pause"))
	})
	if err := g.AddNode(n); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("human"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("human"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	err := g.ExecuteWithObservability(context.Background(), NewMemoryState(), nil)
	if err == nil {
		t.Fatal("expected pause signal error")
	}

	var pauseSig PauseSignal
	if !errors.As(err, &pauseSig) {
		t.Fatalf("expected PauseSignal, got %T: %v", err, err)
	}
	if pauseSig.PauseResult().RequestID != "req-obs" {
		t.Fatalf("pause request_id = %q, want req-obs", pauseSig.PauseResult().RequestID)
	}
}

func TestExecuteWithResult_ContextCanceled(t *testing.T) {
	g := NewGraph("ctx-canceled")
	n := NewFunctionNode("n1", func(ctx context.Context, state State) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("expected canceled context")
	})
	if err := g.AddNode(n); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.SetEntry("n1"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("n1"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := g.ExecuteWithResult(ctx, NewMemoryState())
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestValidate_MissingEntry(t *testing.T) {
	g := NewGraph("missing-entry")
	n := NewFunctionNode("n1", func(ctx context.Context, state State) error { return nil })
	if err := g.AddNode(n); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if err := g.AddFinish("n1"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	if err := g.Validate(); err == nil {
		t.Fatal("expected validation error for missing entry")
	}
}

func TestValidate_UnreachableFinish(t *testing.T) {
	g := NewGraph("unreachable-finish")
	entry := NewFunctionNode("entry", func(ctx context.Context, state State) error { return nil })
	isolated := NewFunctionNode("isolated-finish", func(ctx context.Context, state State) error { return nil })
	if err := g.AddNode(entry); err != nil {
		t.Fatalf("add entry node: %v", err)
	}
	if err := g.AddNode(isolated); err != nil {
		t.Fatalf("add finish node: %v", err)
	}
	if err := g.SetEntry("entry"); err != nil {
		t.Fatalf("set entry: %v", err)
	}
	if err := g.AddFinish("isolated-finish"); err != nil {
		t.Fatalf("add finish: %v", err)
	}

	if err := g.Validate(); err == nil {
		t.Fatal("expected validation error for unreachable finish")
	}
}

func TestAddEdge_RejectsUnknownNode(t *testing.T) {
	g := NewGraph("unknown-edge")
	n := NewFunctionNode("n1", func(ctx context.Context, state State) error { return nil })
	if err := g.AddNode(n); err != nil {
		t.Fatalf("add node: %v", err)
	}

	if err := g.AddEdge(NewDirectEdge("n1", "missing")); err == nil {
		t.Fatal("expected add edge error for unknown target node")
	}
	if err := g.AddEdge(NewDirectEdge("missing", "n1")); err == nil {
		t.Fatal("expected add edge error for unknown source node")
	}
}
