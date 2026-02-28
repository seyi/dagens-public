// Package remote provides remote agent execution capabilities for distributed coordination
package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/registry"
	proto "github.com/seyi/dagens/pkg/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity" // Added
	"google.golang.org/grpc/credentials/insecure"
)

// RemoteExecutionService handles remote agent execution requests
type RemoteExecutionService struct {
	registry     registry.Registry
	localNodeID  string
	agentManager AgentManager
}

// AgentManager provides access to local agents for execution
type AgentManager interface {
	GetAgent(name string) (agent.Agent, error)
	ExecuteAgent(ctx context.Context, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error)
}

// RemoteExecutionRequest represents a request to execute an agent remotely
type RemoteExecutionRequest struct {
	AgentName string                 `json:"agent_name"`
	Input     *agent.AgentInput     `json:"input"`
	NodeID    string                `json:"node_id"`
	RequestID string                `json:"request_id"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// RemoteExecutionResponse represents the response from remote execution
type RemoteExecutionResponse struct {
	Output   *agent.AgentOutput     `json:"output"`
	Error    string                 `json:"error,omitempty"`
	NodeID   string                 `json:"node_id"`
	RequestID string                `json:"request_id"`
	Duration time.Duration          `json:"duration"`
}

// NewRemoteExecutionService creates a new remote execution service
func NewRemoteExecutionService(registry registry.Registry, agentManager AgentManager, localNodeID string) *RemoteExecutionService {
	return &RemoteExecutionService{
		registry:     registry,
		localNodeID:  localNodeID,
		agentManager: agentManager,
	}
}

// Execute executes an agent on this node as requested by a remote node
func (s *RemoteExecutionService) Execute(ctx context.Context, request *RemoteExecutionRequest) (*RemoteExecutionResponse, error) {
	startTime := time.Now()

	// Validate the request
	if request.AgentName == "" {
		return &RemoteExecutionResponse{
			Error:    "agent name is required",
			NodeID:   s.localNodeID,
			RequestID: request.RequestID,
			Duration: time.Since(startTime),
		}, nil
	}

	if request.Input == nil {
		return &RemoteExecutionResponse{
			Error:    "input is required",
			NodeID:   s.localNodeID,
			RequestID: request.RequestID,
			Duration: time.Since(startTime),
		}, nil
	}

	// Execute the agent locally
	output, err := s.agentManager.ExecuteAgent(ctx, request.AgentName, request.Input)

	duration := time.Since(startTime)

	if err != nil {
		return &RemoteExecutionResponse{
			Error:    err.Error(),
			NodeID:   s.localNodeID,
			RequestID: request.RequestID,
			Duration: duration,
		}, nil
	}

	return &RemoteExecutionResponse{
		Output:   output,
		NodeID:   s.localNodeID,
		RequestID: request.RequestID,
		Duration: duration,
	}, nil
}

// RemoteExecutor handles sending execution requests to remote nodes
type RemoteExecutor struct {
	registry registry.Registry
	connPool *ConnectionPool
	timeout  time.Duration
}

// ConnectionPool manages gRPC connections to remote nodes
type ConnectionPool struct {
	connections map[string]*grpc.ClientConn
	mu          sync.RWMutex
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool() *ConnectionPool {
	return &ConnectionPool{
		connections: make(map[string]*grpc.ClientConn),
	}
}

// GetConnection gets or creates a connection to a remote node
func (cp *ConnectionPool) GetConnection(nodeAddr string) (*grpc.ClientConn, error) {
	cp.mu.RLock()
	conn, exists := cp.connections[nodeAddr]
	cp.mu.RUnlock()

	if exists && conn.GetState() != connectivity.Shutdown {
		return conn, nil
	}

	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Double-check after acquiring write lock
	conn, exists = cp.connections[nodeAddr]
	if exists && conn.GetState() != connectivity.Shutdown {
		return conn, nil
	}

	// Create new connection
	conn, err := grpc.Dial(
		nodeAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", nodeAddr, err)
	}

	cp.connections[nodeAddr] = conn
	return conn, nil
}

// Close closes all connections in the pool
func (cp *ConnectionPool) Close() {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	for addr, conn := range cp.connections {
		conn.Close()
		delete(cp.connections, addr)
	}
}

// NewRemoteExecutor creates a new remote executor
func NewRemoteExecutor(registry registry.Registry, timeout time.Duration) *RemoteExecutor {
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &RemoteExecutor{
		registry: registry,
		connPool: NewConnectionPool(),
		timeout:  timeout,
	}
}

// ExecuteOnNode executes an agent on a remote node
func (re *RemoteExecutor) ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Get node information from registry
	node, exists := re.registry.GetNode(nodeID)
	if !exists {
		return nil, fmt.Errorf("node %s not found in registry", nodeID)
	}

	// Create remote execution request
	request := &RemoteExecutionRequest{
		AgentName: agentName,
		Input:     input,
		NodeID:    nodeID,
		RequestID: fmt.Sprintf("remote-exec-%d", time.Now().UnixNano()),
		Metadata: map[string]interface{}{
			"source_node": re.registry.GetNodeID(), // Assuming registry has this method
			"timestamp":   time.Now().Unix(),
		},
	}

	// Create connection to remote node
	addr := fmt.Sprintf("%s:%d", node.Address, node.Port)
	conn, err := re.connPool.GetConnection(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to remote node %s: %w", addr, err)
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, re.timeout)
	defer cancel()

	// Execute the request using gRPC
	output, err := re.ExecuteRemote(execCtx, request, conn)
	if err != nil {
		return nil, fmt.Errorf("remote execution failed: %w", err)
	}

	return output, nil
}

// ExecuteRemote executes an agent on a remote node using gRPC
func (re *RemoteExecutor) ExecuteRemote(ctx context.Context, request *RemoteExecutionRequest, conn *grpc.ClientConn) (*agent.AgentOutput, error) {
	// Serialize input to JSON
	inputBytes, err := json.Marshal(request.Input)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize input: %w", err)
	}

	// Create gRPC client
	client := proto.NewRemoteExecutionServiceClient(conn)

	// Create execution request
	execReq := &proto.ExecuteRequest{
		AgentName: request.AgentName,
		InputJson: string(inputBytes),
		NodeId:    request.NodeID,
		RequestId: request.RequestID,
		Metadata:  convertInterfaceMap(request.Metadata),
	}

	// Execute the request with context timeout
	resp, err := client.Execute(ctx, execReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC execution failed: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("remote agent execution failed: %s", resp.Error)
	}

	// Deserialize output
	var output agent.AgentOutput
	if err := json.Unmarshal([]byte(resp.OutputJson), &output); err != nil {
		return nil, fmt.Errorf("failed to deserialize output: %w", err)
	}

	return &output, nil
}

// Close closes the remote executor and its connection pool
func (re *RemoteExecutor) Close() error {
	re.connPool.Close()
	return nil
}

// GetNodeAddress gets the address for a node from the registry
func (re *RemoteExecutor) GetNodeAddress(nodeID string) (string, error) {
	node, exists := re.registry.GetNode(nodeID)
	if !exists {
		return "", fmt.Errorf("node %s not found", nodeID)
	}

	return fmt.Sprintf("%s:%d", node.Address, node.Port), nil
}

// IsNodeHealthy checks if a node is healthy according to the registry
func (re *RemoteExecutor) IsNodeHealthy(nodeID string) bool {
	node, exists := re.registry.GetNode(nodeID)
	if !exists {
		return false
	}
	return node.Healthy
}

// GetHealthyNodes returns all healthy nodes from the registry
func (re *RemoteExecutor) GetHealthyNodes() []registry.NodeInfo {
	return re.registry.GetHealthyNodes()
}

// GetNodesByCapability returns nodes that have a specific capability
func (re *RemoteExecutor) GetNodesByCapability(capability string) []registry.NodeInfo {
	return re.registry.GetNodesByCapability(capability)
}
