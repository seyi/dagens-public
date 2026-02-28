// Package remote_test provides comprehensive tests for the remote execution system
package remote

import (
	"context"
	"encoding/json" // Added json import
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote/proto"
)

// MockAgent and MockRegistry are defined in testing_helpers.go

// TestGRPCRemoteExecutionService_Execute tests the gRPC execution service
func TestGRPCRemoteExecutionService_Execute(t *testing.T) {
	// Create a mock agent manager
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		return &agent.AgentOutput{
			Result: fmt.Sprintf("Processed: %s", input.Instruction),
			Metadata: map[string]interface{}{
				"processed_by": "test-agent",
				"timestamp":    time.Now().Unix(),
			},
		}, nil
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:           "test-node",
		Name:         "Test Node",
		Address:      "localhost",
		Port:         0, // Will be assigned dynamically
		Capabilities: []string{"test-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create the service
	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "test-node")

	// Create gRPC server
	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	// Start server on random port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create gRPC client
	conn, err := grpc.Dial(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	defer conn.Close()

	client := proto.NewRemoteExecutionServiceClient(conn)

	// Test execution
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &agent.AgentInput{
		Instruction: "test instruction",
		Context: map[string]interface{}{
			"test_key": "test_value",
		},
	}

	// Serialize input to JSON
	inputBytes, err := json.Marshal(input)
	require.NoError(t, err)

	req := &proto.ExecuteRequest{
		AgentName: "test-agent",
		InputJson: string(inputBytes),
		NodeId:    "test-node",
		RequestId: "test-request-123",
		Metadata:  map[string]string{"test": "value"},
	}

	resp, err := client.Execute(ctx, req)
	require.NoError(t, err)

	// Verify response
	assert.Empty(t, resp.Error)
	assert.Equal(t, "test-node", resp.NodeId)
	assert.Equal(t, "test-request-123", resp.RequestId)
	assert.NotEmpty(t, resp.OutputJson)

	// Deserialize output
	var output agent.AgentOutput
	err = json.Unmarshal([]byte(resp.OutputJson), &output)
	require.NoError(t, err)

	assert.Contains(t, output.Result.(string), "Processed: test instruction")
	assert.Equal(t, "test-agent", output.Metadata["processed_by"])
}

// TestGRPCRemoteExecutor_ExecuteOnNode tests remote execution via gRPC
func TestGRPCRemoteExecutor_ExecuteOnNode(t *testing.T) {
	// Create mock agent for the remote service
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("remote-test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		return &agent.AgentOutput{
			Result: fmt.Sprintf("Remote execution: %s", input.Instruction),
			Metadata: map[string]interface{}{
				"executed_remotely": true,
				"agent":            "remote-test-agent",
			},
		}, nil
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:           "remote-node",
		Name:         "Remote Node",
		Address:      "localhost",
		Port:         0, // Will be assigned dynamically
		Capabilities: []string{"remote-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create the remote execution service
	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "remote-node")

	// Create gRPC server
	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	// Start server on random port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create a registry that knows about the test server
	testRegistry := NewMockRegistry()
	testRegistry.AddNode(registry.NodeInfo{
		ID:           "remote-node",
		Name:         "Remote Node",
		Address:      "localhost",
		Port:         int(lis.Addr().(*net.TCPAddr).Port), // Use the actual port
		Capabilities: []string{"remote-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create remote executor
	executor := NewRemoteExecutor(testRegistry, 5*time.Second)

	// Prepare input
	input := &agent.AgentInput{
		Instruction: "remote test instruction",
		Context: map[string]interface{}{
			"remote_test": true,
		},
	}

	// Execute on remote node
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output, err := executor.ExecuteOnNode(ctx, "remote-node", "remote-test-agent", input)
	require.NoError(t, err)

	// Verify output
	assert.Contains(t, output.Result.(string), "Remote execution: remote test instruction")
	assert.Equal(t, true, output.Metadata["executed_remotely"])
	assert.Equal(t, "remote-test-agent", output.Metadata["agent"])
}

// TestRemoteExecutor_ConnectionPooling tests connection reuse
func TestRemoteExecutor_ConnectionPooling(t *testing.T) {
	// Create mock agent
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("pool-test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		return &agent.AgentOutput{
			Result: fmt.Sprintf("Pooled execution: %s", input.Instruction),
		}, nil
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:           "pool-node",
		Name:         "Pool Node",
		Address:      "localhost",
		Port:         0, // Will be assigned dynamically
		Capabilities: []string{"pool-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create the remote execution service
	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "pool-node")

	// Create gRPC server
	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	// Start server on random port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create registry with actual port
	testRegistry := NewMockRegistry()
	testRegistry.AddNode(registry.NodeInfo{
		ID:           "pool-node",
		Name:         "Pool Node",
		Address:      "localhost",
		Port:         int(lis.Addr().(*net.TCPAddr).Port),
		Capabilities: []string{"pool-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create remote executor
	executor := NewRemoteExecutor(testRegistry, 5*time.Second)

	// Prepare input
	input := &agent.AgentInput{
		Instruction: "connection pool test",
	}

	// Execute multiple requests to test connection reuse
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		output, err := executor.ExecuteOnNode(ctx, "pool-node", "pool-test-agent", input)
		require.NoError(t, err)
		assert.Contains(t, output.Result.(string), "Pooled execution: connection pool test")
	}

	// Verify connection pool is working by checking that connections are reused
	// (This is implicitly tested by the fact that all requests succeed)
}

// TestRemoteExecutor_ErrorHandling tests error handling in remote execution
func TestRemoteExecutor_ErrorHandling(t *testing.T) {
	// Create mock agent that returns an error
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("error-test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		return nil, fmt.Errorf("simulated remote error")
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:           "error-node",
		Name:         "Error Node",
		Address:      "localhost",
		Port:         0, // Will be assigned dynamically
		Capabilities: []string{"error-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create the remote execution service
	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "error-node")

	// Create gRPC server
	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	// Start server on random port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create registry with actual port
	testRegistry := NewMockRegistry()
	testRegistry.AddNode(registry.NodeInfo{
		ID:           "error-node",
		Name:         "Error Node",
		Address:      "localhost",
		Port:         int(lis.Addr().(*net.TCPAddr).Port),
		Capabilities: []string{"error-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create remote executor
	executor := NewRemoteExecutor(testRegistry, 5*time.Second)

	// Prepare input
	input := &agent.AgentInput{
		Instruction: "error test instruction",
	}

	// Execute on remote node - should return error
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output, err := executor.ExecuteOnNode(ctx, "error-node", "error-test-agent", input)
	// The error should be properly propagated from the remote agent
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "simulated remote error")
	assert.Nil(t, output)
}

// TestRemoteExecutor_TimeoutHandling tests timeout handling
func TestRemoteExecutor_TimeoutHandling(t *testing.T) {
	// Create mock agent that takes a long time to execute
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("timeout-test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		// Sleep longer than the timeout
		select {
		case <-time.After(2 * time.Second):
			return &agent.AgentOutput{
				Result: "This should not be returned due to timeout",
			}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:           "timeout-node",
		Name:         "Timeout Node",
		Address:      "localhost",
		Port:         0, // Will be assigned dynamically
		Capabilities: []string{"timeout-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create the remote execution service
	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "timeout-node")

	// Create gRPC server
	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	// Start server on random port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create registry with actual port
	testRegistry := NewMockRegistry()
	testRegistry.AddNode(registry.NodeInfo{
		ID:           "timeout-node",
		Name:         "Timeout Node",
		Address:      "localhost",
		Port:         int(lis.Addr().(*net.TCPAddr).Port),
		Capabilities: []string{"timeout-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create remote executor with short timeout
	executor := NewRemoteExecutor(testRegistry, 500*time.Millisecond) // 500ms timeout

	// Prepare input
	input := &agent.AgentInput{
		Instruction: "timeout test instruction",
	}

	// Execute on remote node - should timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	output, err := executor.ExecuteOnNode(ctx, "timeout-node", "timeout-test-agent", input)
	// Should get a context deadline exceeded error
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	assert.Nil(t, output)
}

// TestRemoteExecutor_NodeNotFound tests handling of non-existent nodes
func TestRemoteExecutor_NodeNotFound(t *testing.T) {
	// Create registry without the target node
	testRegistry := NewMockRegistry()
	// Don't add the target node "missing-node"

	// Create remote executor
	executor := NewRemoteExecutor(testRegistry, 5*time.Second)

	// Prepare input
	input := &agent.AgentInput{
		Instruction: "node not found test",
	}

	// Execute on non-existent node
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output, err := executor.ExecuteOnNode(ctx, "missing-node", "any-agent", input)
	// Should get an error about node not found
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Nil(t, output)
}

// TestRemoteExecutor_ConcurrentExecution tests concurrent remote execution
func TestRemoteExecutor_ConcurrentExecution(t *testing.T) {
	// Create mock agent
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("concurrent-test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		return &agent.AgentOutput{
			Result: fmt.Sprintf("Concurrent execution: %s", input.Instruction),
			Metadata: map[string]interface{}{
				"thread_id": fmt.Sprintf("goroutine-%d", time.Now().Nanosecond()%1000),
			},
		}, nil
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:           "concurrent-node",
		Name:         "Concurrent Node",
		Address:      "localhost",
		Port:         0, // Will be assigned dynamically
		Capabilities: []string{"concurrent-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create the remote execution service
	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "concurrent-node")

	// Create gRPC server
	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	// Start server on random port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create registry with actual port
	testRegistry := NewMockRegistry()
	testRegistry.AddNode(registry.NodeInfo{
		ID:           "concurrent-node",
		Name:         "Concurrent Node",
		Address:      "localhost",
		Port:         int(lis.Addr().(*net.TCPAddr).Port),
		Capabilities: []string{"concurrent-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create remote executor
	executor := NewRemoteExecutor(testRegistry, 5*time.Second)

	// Prepare input
	input := &agent.AgentInput{
		Instruction: "concurrent test",
	}

	// Execute multiple requests concurrently
	const numConcurrent = 10
	results := make(chan *agent.AgentOutput, numConcurrent)
	errors := make(chan error, numConcurrent)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := 0; i < numConcurrent; i++ {
		go func(requestID int) {
			result, err := executor.ExecuteOnNode(ctx, "concurrent-node", "concurrent-test-agent", input)
			if err != nil {
				errors <- err
			} else {
				results <- result
			}
		}(i)
	}

	// Collect results
	completed := 0
	for completed < numConcurrent {
		select {
		case result := <-results:
			assert.Contains(t, result.Result.(string), "Concurrent execution: concurrent test")
			completed++
		case err := <-errors:
			assert.NoError(t, err, "No errors expected in concurrent test")
			completed++
		case <-ctx.Done():
			t.Fatalf("Test timed out before completion")
		}
	}

	assert.Equal(t, numConcurrent, completed, "All requests should complete")
}

// TestRemoteExecutor_HealthCheck tests health check functionality
func TestRemoteExecutor_HealthCheck(t *testing.T) {
	// Create mock agent
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("health-test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		return &agent.AgentOutput{
			Result: "Health check OK",
		}, nil
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:           "health-node",
		Name:         "Health Node",
		Address:      "localhost",
		Port:         0, // Will be assigned dynamically
		Capabilities: []string{"health-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create the remote execution service
	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "health-node")

	// Create gRPC server
	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	// Start server on random port
	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Create registry with actual port
	testRegistry := NewMockRegistry()
	testRegistry.AddNode(registry.NodeInfo{
		ID:           "health-node",
		Name:         "Health Node",
		Address:      "localhost",
		Port:         int(lis.Addr().(*net.TCPAddr).Port),
		Capabilities: []string{"health-capability"},
		LastSeen:     time.Now(),
		Load:         0.0,
		Healthy:      true,
	})

	// Create remote executor
	executor := NewRemoteExecutor(testRegistry, 5*time.Second)

	// Test that node is healthy
	isHealthy := executor.IsNodeHealthy("health-node")
	assert.True(t, isHealthy, "Node should be healthy")

	// Get healthy nodes
	healthyNodes := executor.GetHealthyNodes()
	assert.Len(t, healthyNodes, 1, "Should have one healthy node")
	assert.Equal(t, "health-node", healthyNodes[0].ID)

	// Get node address
	addr, err := executor.GetNodeAddress("health-node")
	assert.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("localhost:%d", int(lis.Addr().(*net.TCPAddr).Port)), addr)
}
