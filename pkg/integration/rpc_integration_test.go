package integration

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote"
	pb "github.com/seyi/dagens/pkg/remote/proto"
	"github.com/seyi/dagens/pkg/scheduler"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

// --- Mocks ---

// MockLocalAgentManager handles execution on the "Server" side
type MockLocalAgentManager struct{}

func (m *MockLocalAgentManager) GetAgent(name string) (agent.Agent, error) {
	return nil, nil // Not needed for this test
}

func (m *MockLocalAgentManager) ExecuteAgent(ctx context.Context, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	return &agent.AgentOutput{
		Result: fmt.Sprintf("RemoteExec Success: %s", agentName),
		Metadata: map[string]interface{}{
			"executor": "real-grpc",
		},
	}, nil
}

// MockRegistryForScheduler implements registry.Registry interface
type MockRegistryForScheduler struct {
	nodeID string
	addr   string
	port   int
}

func (m *MockRegistryForScheduler) GetHealthyNodes() []registry.NodeInfo {
	return []registry.NodeInfo{
		{
			ID:      m.nodeID,
			Address: m.addr,
			Port:    m.port,
			Healthy: true,
		},
	}
}

func (m *MockRegistryForScheduler) GetNode(nodeID string) (registry.NodeInfo, bool) {
	if nodeID == m.nodeID {
		return registry.NodeInfo{
			ID:      m.nodeID,
			Address: m.addr,
			Port:    m.port,
			Healthy: true,
		}, true
	}
	return registry.NodeInfo{}, false
}

// Helper methods to satisfy interface
func (m *MockRegistryForScheduler) GetNodeID() string { return "scheduler" }
func (m *MockRegistryForScheduler) Start(ctx context.Context) error { return nil }
func (m *MockRegistryForScheduler) Stop() error { return nil }
func (m *MockRegistryForScheduler) RegisterService(name string, port int) error { return nil }
func (m *MockRegistryForScheduler) UnregisterService(name string) error { return nil }
func (m *MockRegistryForScheduler) GetService(name string) (registry.ServiceInfo, bool) { return registry.ServiceInfo{}, false }
func (m *MockRegistryForScheduler) GetServices(name string) []registry.ServiceInfo { return nil }
func (m *MockRegistryForScheduler) UpdateNodeLoad(load float64) error { return nil }
func (m *MockRegistryForScheduler) AddObserver(o registry.RegistryObserver) {}
func (m *MockRegistryForScheduler) RemoveObserver(o registry.RegistryObserver) {}
func (m *MockRegistryForScheduler) GetNodeCount() int { return 1 }
func (m *MockRegistryForScheduler) GetHealthyNodeCount() int { return 1 }
func (m *MockRegistryForScheduler) GetNodes() []registry.NodeInfo { return m.GetHealthyNodes() }
func (m *MockRegistryForScheduler) GetNodesByCapability(c string) []registry.NodeInfo { return nil }


// SetupGRPCServer starts a real gRPC server on a random port
func SetupGRPCServer(t *testing.T, nodeID string) (string, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	
	// Create the service implementation
	// We pass nil for registry because Execute() doesn't use it
	mockAgentManager := &MockLocalAgentManager{}
	service := remote.NewRemoteExecutionService(nil, mockAgentManager, nodeID)
	
	// Create gRPC handler
	grpcHandler := remote.NewGRPCRemoteExecutionService(service)
	
	pb.RegisterRemoteExecutionServiceServer(s, grpcHandler)

	go func() {
		if err := s.Serve(lis); err != nil {
			// server stopped
		}
	}()

	return lis.Addr().String(), func() {
		s.Stop()
	}
}

// --- Integration Test ---

func TestRealRPCIntegration(t *testing.T) {
	// 1. Infrastructure Setup
	// -----------------------
	nodeID := "remote-node-1"
	
	// Start Real gRPC Server
	fullAddr, stopServer := SetupGRPCServer(t, nodeID)
	defer stopServer()
	
	host, portStr, _ := net.SplitHostPort(fullAddr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	
	t.Logf("Real gRPC server listening at %s", fullAddr)

	// Setup Registry pointing to this server
	mockReg := &MockRegistryForScheduler{
		nodeID: nodeID,
		addr:   host,
		port:   port,
	}

	// Setup Real Remote Executor (Client)
	// This will actually dial the gRPC server
	realExecutor := remote.NewRemoteExecutor(mockReg, 5*time.Second)

	// Setup Scheduler using Real Executor
	sched := scheduler.NewScheduler(mockReg, realExecutor)
	sched.Start()
	defer sched.Stop()

	// 2. Job Submission
	// -----------------
	g := graph.NewGraph("rpc-test-graph")
	nodeA := graph.NewFunctionNode("node-a", func(ctx context.Context, s graph.State) error { return nil })
	g.AddNode(nodeA)
	g.SetEntry("node-a")
	g.AddFinish("node-a")

	compiler := graph.NewDAGCompiler()
	job, err := compiler.Compile(g, &agent.AgentInput{Instruction: "rpc test"})
	assert.NoError(t, err)

	err = sched.SubmitJob(job)
	assert.NoError(t, err)

	// 3. Wait for Result
	// ------------------
	timeout := time.After(5 * time.Second)
	completed := false
	
	for {
		select {
		case <-timeout:
			t.Fatal("Timed out waiting for RPC execution")
		default:
			j, err := sched.GetJob(job.ID)
			assert.NoError(t, err)
			
			if j.Status == scheduler.JobCompleted {
				completed = true
				break
			}
			if j.Status == scheduler.JobFailed {
				t.Fatalf("Job failed execution")
			}
			time.Sleep(100 * time.Millisecond)
		}
		if completed {
			break
		}
	}
	
	assert.True(t, completed)
	t.Log("Successfully executed job via real gRPC transport!")
}
