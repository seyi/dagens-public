package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockNode implements Node for testing
type mockNode struct {
	id          string
	nodeType    string
	failCount   int
	currentCall int
	metadata    map[string]interface{}
}

func newMockNode(id string, failCount int) *mockNode {
	return &mockNode{
		id:        id,
		nodeType:  "mock",
		failCount: failCount,
		metadata:  make(map[string]interface{}),
	}
}

func (m *mockNode) ID() string   { return m.id }
func (m *mockNode) Type() string { return m.nodeType }
func (m *mockNode) Metadata() map[string]interface{} { return m.metadata }

func (m *mockNode) Execute(ctx context.Context, state State) error {
	m.currentCall++
	if m.currentCall <= m.failCount {
		return errors.New("simulated failure")
	}
	state.Set("mock_result_"+m.id, "success from "+m.id)
	return nil
}

func TestAgenticNode_Execute_Success(t *testing.T) {
	mock := newMockNode("node-1", 0)
	an := NewAgenticNode(mock)

	ctx := context.Background()
	state := NewMemoryState()

	err := an.Execute(ctx, state)

	assert.NoError(t, err)

	result, ok := state.Get("mock_result_node-1")
	assert.True(t, ok)
	assert.Equal(t, "success from node-1", result)
}

func TestAgenticNode_Execute_WithRetry(t *testing.T) {
	// Node fails twice, then succeeds
	mock := newMockNode("node-2", 2)
	an := NewAgenticNode(mock, WithNodeHealing(3))

	ctx := context.Background()
	state := NewMemoryState()

	err := an.Execute(ctx, state)

	assert.NoError(t, err)
	assert.Equal(t, 3, mock.currentCall) // 2 failures + 1 success
}

func TestAgenticNode_Execute_MaxRetriesExceeded(t *testing.T) {
	// Node always fails
	mock := newMockNode("node-3", 100)
	an := NewAgenticNode(mock, WithNodeHealing(2))

	ctx := context.Background()
	state := NewMemoryState()

	err := an.Execute(ctx, state)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retries")
}

func TestAgenticNode_Type(t *testing.T) {
	mock := newMockNode("node-4", 0)
	an := NewAgenticNode(mock)

	assert.Equal(t, "mock-agentic", an.Type())
}

func TestAgenticNode_Metadata(t *testing.T) {
	mock := newMockNode("node-5", 0)
	an := NewAgenticNode(mock)

	meta := an.Metadata()

	assert.True(t, meta["agentic"].(bool))
	assert.Equal(t, "mock", meta["original_type"])
	assert.Contains(t, meta["capabilities"].([]string), "monitoring")
	assert.Contains(t, meta["capabilities"].([]string), "healing")
}

func TestAgenticNode_RecordDomainMetric(t *testing.T) {
	mock := newMockNode("node-6", 0)
	an := NewAgenticNode(mock).(*AgenticNode)

	// Record domain metric
	an.RecordDomainMetric("domain.custom_metric", 42.0)

	// Verify
	mc := an.Monitoring()
	val, ok := mc.GetDomainMetric("domain.custom_metric")
	assert.True(t, ok)
	assert.Equal(t, 42.0, val)
}

func TestNewAgenticGraph(t *testing.T) {
	// Create a simple graph
	graph := NewGraph("test-graph")

	node1 := newMockNode("node1", 0)
	node2 := newMockNode("node2", 0)

	graph.AddNode(node1)
	graph.AddNode(node2)

	edge := NewDirectEdge("node1", "node2")
	graph.AddEdge(edge)

	graph.SetEntry("node1")
	graph.AddFinish("node2")

	// Create agentic graph
	agenticGraph := NewAgenticGraph(graph, WithNodeHealing(2))

	// Verify nodes are wrapped
	n1, err := agenticGraph.GetNode("node1")
	assert.NoError(t, err)
	assert.Equal(t, "mock-agentic", n1.Type())

	n2, err := agenticGraph.GetNode("node2")
	assert.NoError(t, err)
	assert.Equal(t, "mock-agentic", n2.Type())
}

func TestExecuteGraphWithAgenticCapabilities(t *testing.T) {
	// Create a simple graph
	graph := NewGraph("test-graph")

	node1 := newMockNode("node1", 0)
	node2 := newMockNode("node2", 0)

	graph.AddNode(node1)
	graph.AddNode(node2)

	edge := NewDirectEdge("node1", "node2")
	graph.AddEdge(edge)

	graph.SetEntry("node1")
	graph.AddFinish("node2")

	// Execute with agentic capabilities
	ctx := context.Background()
	state := NewMemoryState()

	err := ExecuteGraphWithAgenticCapabilities(ctx, graph, state, WithNodeHealing(2))
	assert.NoError(t, err)

	// Verify execution
	result1, ok := state.Get("mock_result_node1")
	assert.True(t, ok)
	assert.Equal(t, "success from node1", result1)

	result2, ok := state.Get("mock_result_node2")
	assert.True(t, ok)
	assert.Equal(t, "success from node2", result2)
}
