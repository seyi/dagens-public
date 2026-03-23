package agents

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote"
	"github.com/seyi/dagens/pkg/remote/proto"
	"github.com/seyi/dagens/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// MockAgentManager for the remote side
type remoteAgentManager struct {
	executed bool
}

func (m *remoteAgentManager) GetAgent(name string) (agent.Agent, error) { return nil, nil }
func (m *remoteAgentManager) ExecuteAgent(ctx context.Context, name string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	m.executed = true
	return &agent.AgentOutput{
		Result: "Real remote execution result",
		Metadata: map[string]interface{}{"remote": true},
	}, nil
}

func TestClusterAwareLoadBalancerAgent_RealRemoteExecution(t *testing.T) {
	// 1. Start embedded etcd
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	// 2. Start a remote worker (gRPC server)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	remotePort := lis.Addr().(*net.TCPAddr).Port

	mgr := &remoteAgentManager{}
	srv := remote.NewRemoteExecutionService(nil, mgr, "remote-node-1")
	grpcHandler := remote.NewGRPCRemoteExecutionService(srv)
	
	grpcServer := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(grpcServer, grpcHandler)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	// 3. Setup Registry and RemoteExecutor
	ctx := context.Background()
	reg, err := registry.NewDistributedAgentRegistry(registry.RegistryConfig{
		EtcdEndpoints: endpoints,
		NodeID:        "local-node",
		NodeAddress:   "127.0.0.1",
		NodePort:      8080,
	})
	require.NoError(t, err)
	err = reg.Start(ctx)
	require.NoError(t, err)
	defer reg.Stop()

	// Actually we should just register a second registry instance to simulate the worker
	workerReg, err := registry.NewDistributedAgentRegistry(registry.RegistryConfig{
		EtcdEndpoints: endpoints,
		NodeID:        "remote-node-1",
		NodeAddress:   "127.0.0.1",
		NodePort:      remotePort,
	})
	require.NoError(t, err)
	require.NoError(t, workerReg.Start(ctx))
	defer workerReg.Stop()

	require.Eventually(t, func() bool {
		node, ok := reg.GetNode("remote-node-1")
		return ok && node.Healthy && node.Port == remotePort
	}, 5*time.Second, 100*time.Millisecond, "remote node was not discovered by registry")

	remoteExec := remote.NewRemoteExecutor(reg, 5*time.Second)
	defer remoteExec.Close()

	// 4. Create ClusterAwareLoadBalancerAgent
	lbAgent := NewClusterAwareLoadBalancerAgent(ClusterAwareLoadBalancerConfig{
		LoadBalancerAgentConfig: LoadBalancerAgentConfig{
			Name: "cluster-lb",
			// No local agents, so it MUST go remote
		},
		EnableClusterAwareness: true,
		Registry:              reg,
		RemoteExecutor:        remoteExec,
		ClusterStrategy:       ClusterGlobal,
	})

	// 5. Execute - should route to remote worker
	output, err := lbAgent.Execute(ctx, &agent.AgentInput{
		Instruction: "do something remote",
		Context: map[string]interface{}{
			"agent_name": "test-agent", // Hint for the executor
		},
	})

	// 6. Assertions
	require.NoError(t, err)
	assert.True(t, mgr.executed, "Remote agent manager should have been called")
	assert.Equal(t, "Real remote execution result", output.Result)
	assert.Equal(t, true, output.Metadata["remote"])
}

// --- Strategy Tests ---

type MockClusterCoordinator struct {
	localNodeID string
	strategy    ClusterLoadBalanceStrategy
	executedOn  []string
}

func (m *MockClusterCoordinator) ExecuteOnNode(ctx context.Context, nodeID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	m.executedOn = append(m.executedOn, nodeID)
	return &agent.AgentOutput{
		Result: "Mock execution result",
		Metadata: map[string]interface{}{
			"executed_on": nodeID,
		},
	}, nil
}

func (m *MockClusterCoordinator) GetStats() CoordinatorStats {
	return CoordinatorStats{}
}

func (m *MockClusterCoordinator) GetLocalNodeID() string {
	return m.localNodeID
}

func (m *MockClusterCoordinator) GetStrategy() ClusterLoadBalanceStrategy {
	return m.strategy
}

func (m *MockClusterCoordinator) SetStrategy(strategy ClusterLoadBalanceStrategy) {
	m.strategy = strategy
}

// MockStrategyRegistry implements registry.Registry for strategy tests
type MockStrategyRegistry struct {
	nodes []registry.NodeInfo
}

func (m *MockStrategyRegistry) GetHealthyNodes() []registry.NodeInfo { return m.nodes }
func (m *MockStrategyRegistry) GetNodesByCapability(cap string) []registry.NodeInfo {
	var matched []registry.NodeInfo
	for _, n := range m.nodes {
		for _, c := range n.Capabilities {
			if c == cap {
				matched = append(matched, n)
			}
		}
	}
	return matched
}
func (m *MockStrategyRegistry) GetNodeCount() int { return len(m.nodes) }
func (m *MockStrategyRegistry) GetHealthyNodeCount() int { return len(m.nodes) }
func (m *MockStrategyRegistry) GetNode(id string) (registry.NodeInfo, bool) { return registry.NodeInfo{}, false }
func (m *MockStrategyRegistry) GetNodes() []registry.NodeInfo { return m.nodes }
func (m *MockStrategyRegistry) GetNodeID() string { return "local-node" }
func (m *MockStrategyRegistry) Start(ctx context.Context) error { return nil }
func (m *MockStrategyRegistry) Stop() error { return nil }

func TestClusterAwareLoadBalancerAgent_Strategies(t *testing.T) {
	// Setup Registry with 1 local and 2 remote nodes
	reg := &MockStrategyRegistry{
		nodes: []registry.NodeInfo{
			{ID: "local-node", Capabilities: []string{"cap-a"}},
			{ID: "remote-1", Capabilities: []string{"cap-a", "cap-b"}},
			{ID: "remote-2", Capabilities: []string{"cap-b"}},
		},
	}

	// Helper to create agent with a specific strategy
	createAgent := func(strategy ClusterLoadBalanceStrategy) (*ClusterAwareLoadBalancerAgent, *MockClusterCoordinator) {
		coord := &MockClusterCoordinator{
			localNodeID: "local-node",
			strategy:    strategy,
		}
		
		agent := &ClusterAwareLoadBalancerAgent{
			LoadBalancerAgent:  NewLoadBalancerAgent(LoadBalancerAgentConfig{Name: "test-lb"}),
			registry:           reg,
			localAgentCount:    0, // Assume no local agents for simple routing test (forces remote)
			clusterCoordinator: coord,
		}
		return agent, coord
	}

	ctx := context.Background()

	t.Run("Global Strategy Round Robin", func(t *testing.T) {
		lb, coord := createAgent(ClusterGlobal)
		
		// Execute multiple times
		for i := 0; i < 4; i++ {
			_, err := lb.Execute(ctx, &agent.AgentInput{Instruction: "test"})
			require.NoError(t, err)
		}

		// Should have executed on remote nodes (round robin)
		// Since localAgentCount is 0, it should alternate between remote-1 and remote-2
		assert.Len(t, coord.executedOn, 4)
		assert.Contains(t, coord.executedOn, "remote-1")
		assert.Contains(t, coord.executedOn, "remote-2")
	})

	t.Run("Affinity Strategy", func(t *testing.T) {
		lb, coord := createAgent(ClusterAffinity)

		// 1. Require 'cap-b' (only on remote nodes)
		input := &agent.AgentInput{
			Instruction: "test",
			Context: map[string]interface{}{
				"required_capabilities": []string{"cap-b"},
			},
		}
		
		_, err := lb.Execute(ctx, input)
		require.NoError(t, err)

		// Should route to remote-1 or remote-2 (both have cap-b)
		// Local node does NOT have cap-b
		assert.Len(t, coord.executedOn, 1)
		nodeID := coord.executedOn[0]
		assert.True(t, nodeID == "remote-1" || nodeID == "remote-2", "Should route to node with capability")
	})
}
