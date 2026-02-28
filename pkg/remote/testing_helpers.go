// Package remote provides helper functions for testing remote execution
package remote

import (
	"context"
	"fmt"
	"sync"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/registry"
)

// MockAgent implements the agent.Agent interface for testing
type MockAgent struct {
	name        string
	behavior    func(context.Context, *agent.AgentInput) (*agent.AgentOutput, error)
	executionCount int
	mu          sync.Mutex
}

// NewMockAgent creates a new mock agent with specified behavior
func NewMockAgent(name string, behavior func(context.Context, *agent.AgentInput) (*agent.AgentOutput, error)) *MockAgent {
	return &MockAgent{
		name:     name,
		behavior: behavior,
	}
}

func (m *MockAgent) ID() string { return m.name }
func (m *MockAgent) Name() string { return m.name }
func (m *MockAgent) Description() string { return fmt.Sprintf("Mock agent %s", m.name) }
func (m *MockAgent) Capabilities() []string { return []string{"test-capability"} }
func (m *MockAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	m.mu.Lock()
	m.executionCount++
	count := m.executionCount
	m.mu.Unlock()

	if m.behavior != nil {
		return m.behavior(ctx, input)
	}

	return &agent.AgentOutput{
		Result: fmt.Sprintf("Mock agent %s executed request %d", m.name, count),
	}, nil
}
func (m *MockAgent) Dependencies() []agent.Agent { return nil }
func (m *MockAgent) Partition() string { return "" }


// MockAgentManager implements the AgentManager interface for testing
type MockAgentManager struct {
	agents map[string]agent.Agent
	mu     sync.RWMutex
}

// NewMockAgentManager creates a new mock agent manager
func NewMockAgentManager() *MockAgentManager {
	return &MockAgentManager{
		agents: make(map[string]agent.Agent),
	}
}

// AddAgent adds an agent to the mock manager
func (m *MockAgentManager) AddAgent(agent agent.Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[agent.Name()] = agent
}

// GetAgent returns an agent by name
func (m *MockAgentManager) GetAgent(name string) (agent.Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	a, exists := m.agents[name]
	if !exists {
		return nil, agent.ErrAgentNotFound
	}
	return a, nil
}

// ExecuteAgent executes an agent by name
func (m *MockAgentManager) ExecuteAgent(ctx context.Context, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	agent, err := m.GetAgent(agentName)
	if err != nil {
		return nil, err
	}
	
	return agent.Execute(ctx, input)
}

// MockRegistry implements the registry interface for testing
type MockRegistry struct {
	nodes map[string]registry.NodeInfo
	mu    sync.RWMutex
}

// NewMockRegistry creates a new mock registry
func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		nodes: make(map[string]registry.NodeInfo),
	}
}

// GetNode returns a node by ID
func (m *MockRegistry) GetNode(nodeID string) (registry.NodeInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	node, exists := m.nodes[nodeID]
	return node, exists
}

// GetNodes returns all nodes
func (m *MockRegistry) GetNodes() []registry.NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	nodes := make([]registry.NodeInfo, 0, len(m.nodes))
	for _, node := range m.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// GetHealthyNodes returns only healthy nodes
func (m *MockRegistry) GetHealthyNodes() []registry.NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	nodes := make([]registry.NodeInfo, 0)
	for _, node := range m.nodes {
		if node.Healthy {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// GetNodesByCapability returns nodes with a specific capability
func (m *MockRegistry) GetNodesByCapability(capability string) []registry.NodeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	nodes := make([]registry.NodeInfo, 0)
	for _, node := range m.nodes {
		for _, cap := range node.Capabilities {
			if cap == capability {
				nodes = append(nodes, node)
				break
			}
		}
	}
	return nodes
}

// GetNodeID returns the local node ID
func (m *MockRegistry) GetNodeID() string {
	return "mock-registry-node"
}

// AddNode adds a node to the registry
func (m *MockRegistry) AddNode(node registry.NodeInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes[node.ID] = node
}

// RemoveNode removes a node from the registry
func (m *MockRegistry) RemoveNode(nodeID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.nodes, nodeID)
}