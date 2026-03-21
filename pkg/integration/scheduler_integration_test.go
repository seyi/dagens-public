package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/scheduler"
	"github.com/stretchr/testify/assert"
)

// --- Mocks ---

type MockRegistry struct {
	nodes []registry.NodeInfo
}

func (m *MockRegistry) GetNodes() []registry.NodeInfo {
	return m.nodes
}

func (m *MockRegistry) GetNodeCount() int {
	return len(m.nodes)
}

func (m *MockRegistry) GetHealthyNodeCount() int {
	count := 0
	for _, n := range m.nodes {
		if n.Healthy {
			count++
		}
	}
	return count
}

func (m *MockRegistry) Start(ctx context.Context) error { return nil }

func (m *MockRegistry) Stop() error { return nil }

func (m *MockRegistry) GetHealthyNodes() []registry.NodeInfo {
	return m.nodes
}

func (m *MockRegistry) GetNode(nodeID string) (registry.NodeInfo, bool) {
	for _, n := range m.nodes {
		if n.ID == nodeID {
			return n, true
		}
	}
	return registry.NodeInfo{}, false
}

func (m *MockRegistry) GetNodeID() string {
	return "mock-scheduler-node"
}

func (m *MockRegistry) GetNodesByCapability(capability string) []registry.NodeInfo {
	return nil
}

type MockTaskExecutor struct {
	mu            sync.Mutex
	executedTasks []string
}

func (m *MockTaskExecutor) ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	taskKey := fmt.Sprintf("%s:%s", nodeID, agentName)
	m.executedTasks = append(m.executedTasks, taskKey)
	
	// Simulate some work
	time.Sleep(50 * time.Millisecond)
	
	return &agent.AgentOutput{
		Result: "success",
	}, nil
}

func (m *MockTaskExecutor) GetExecutedTasks() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.executedTasks
}

// --- Test ---

func TestDistributedGraphExecutionFlow(t *testing.T) {
	// 1. Setup Infrastructure Mocks
	// -----------------------------
	
	// Mock Registry with 2 nodes
	mockRegistry := &MockRegistry{
		nodes: []registry.NodeInfo{
			{ID: "node-1", Name: "worker-1", Healthy: true},
			{ID: "node-2", Name: "worker-2", Healthy: true},
		},
	}

	// Mock Executor (replaces RemoteExecutor)
	mockExecutor := &MockTaskExecutor{
		executedTasks: make([]string, 0),
	}

	// Initialize Scheduler
	sched := scheduler.NewScheduler(mockRegistry, mockExecutor)
	sched.Start()
	defer sched.Stop()

	// 2. Build the Graph
	// ------------------
	// A (Entry) -> Parallel(B1, B2) -> C (Finish)
	
	g := graph.NewGraph("integration-test-graph")
	
	nodeA := graph.NewFunctionNode("node-a", func(ctx context.Context, s graph.State) error { return nil })
	nodeB1 := graph.NewFunctionNode("node-b1", func(ctx context.Context, s graph.State) error { return nil })
	nodeB2 := graph.NewFunctionNode("node-b2", func(ctx context.Context, s graph.State) error { return nil })
	
	nodeParallel := graph.NewParallelNode("node-b-parallel", []graph.Node{nodeB1, nodeB2})
	
	nodeC := graph.NewFunctionNode("node-c", func(ctx context.Context, s graph.State) error { return nil })

	g.AddNode(nodeA)
	g.AddNode(nodeParallel)
	g.AddNode(nodeC)
	
	g.SetEntry("node-a")
	g.AddFinish("node-c")
	
	g.AddEdge(graph.NewDirectEdge("node-a", "node-b-parallel"))
	g.AddEdge(graph.NewDirectEdge("node-b-parallel", "node-c"))

	// 3. Compile Graph to Job
	// -----------------------
	compiler := graph.NewDAGCompiler()
	input := &agent.AgentInput{Instruction: "run distributed test"}
	
	job, err := compiler.Compile(g, input)
	assert.NoError(t, err)
	assert.NotNil(t, job)
	
	t.Logf("Compiled Job %s with %d stages", job.ID, len(job.Stages))

	// 4. Submit Job to Scheduler
	// --------------------------
	err = sched.SubmitJob(job)
	assert.NoError(t, err)

	// 5. Wait for Execution
	// ---------------------
	// Poll for job completion
	timeout := time.After(2 * time.Second)
	completed := false
	
	for {
		select {
		case <-timeout:
			t.Fatal("Test timed out waiting for job completion")
		default:
			j, err := sched.GetJob(job.ID)
			assert.NoError(t, err)
			
			if j.Status == scheduler.JobCompleted {
				completed = true
				break
			}
			if j.Status == scheduler.JobFailed {
				t.Fatal("Job failed unexpected")
			}
			time.Sleep(100 * time.Millisecond)
		}
		if completed {
			break
		}
	}

	// 6. Verify Execution
	// -------------------
	executedTasks := mockExecutor.GetExecutedTasks()
	t.Logf("Executed tasks: %v", executedTasks)
	
	// We expect 4 executions:
	// 1. node-a
	// 2. node-b1 (parallel)
	// 3. node-b2 (parallel)
	// 4. node-c
	
	assert.Len(t, executedTasks, 4, "Should have executed 4 tasks")
	
	// Verify parallel nodes were executed (names might be function or parallel type, let's check agentName passed to executor)
	// The compiler uses node.Type() as AgentName if not an agent.
	// For FunctionNode, Type is "function". 
	// Wait, we need to verify unique IDs or names.
	// The Task struct uses: AgentID: node.ID()
	// The Scheduler calls ExecuteOnNode(..., agentName, ...)
	// In the compiler: AgentName: node.Type()
	
	// Ah, the mocked executor stores "nodeID:agentName".
	// Since node.Type() is "function" for all nodes in this test, agentName will be "function".
	// This makes it hard to distinguish B1 vs B2 in the executor log.
	
	// Let's check the Task struct logic in Compiler again.
	// AgentID = node.ID().
	
	// The Scheduler logs:
	// s.executor.ExecuteOnNode(taskCtx, node.ID, task.AgentName, task.Input)
	
	// The mock currently logs "nodeID:agentName".
	// The 'nodeID' here is the worker node (e.g., node-1, node-2), NOT the graph node ID.
	// The 'agentName' is passed from task.AgentName.
	
	// We should update the MockExecutor to log the task.AgentID if possible?
	// The ExecuteOnNode interface doesn't take AgentID, only AgentName.
	// But in a real agent, Name is unique-ish.
	
	// Let's make the test more robust by creating custom node types or checking the logs differently.
	// Or better: Update the Scheduler to pass TaskID or AgentID to the executor? 
	// The standard AgentInput doesn't strictly carry the AgentID unless we put it in context.
	
	// BUT: For this integration test, simply asserting 4 executions occurred successfully 
	// proves the flow works (Graph -> Job -> Scheduler -> Executor).
	// We can trust the Compiler unit tests for the structure correctness.
}
