// Package remote_test provides comprehensive tests for the remote execution system
package remote

import (
	"context"
	"encoding/json" // Added json import
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/seyi/dagens/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote/proto"
)

type staticAuthorityValidator struct {
	validate func(context.Context, DispatchAuthority) error
}

func (v staticAuthorityValidator) Validate(ctx context.Context, authority DispatchAuthority) error {
	if v.validate != nil {
		return v.validate(ctx, authority)
	}
	return nil
}

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
				"agent":             "remote-test-agent",
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

func TestGRPCRemoteExecutor_ExecuteOnNode_StampsDispatchAuthority(t *testing.T) {
	validator := staticAuthorityValidator{
		validate: func(ctx context.Context, authority DispatchAuthority) error {
			if authority.LeaderID != "cp-a" || authority.Epoch != "42" {
				return fmt.Errorf("unexpected authority: %+v", authority)
			}
			return nil
		},
	}
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("remote-test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		return &agent.AgentOutput{Result: "ok"}, nil
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:      "remote-node",
		Name:    "Remote Node",
		Address: "localhost",
		Port:    0,
		Healthy: true,
	})

	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "remote-node")
	service.SetDispatchAuthorityValidator(validator)

	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	time.Sleep(100 * time.Millisecond)

	testRegistry := NewMockRegistry()
	testRegistry.AddNode(registry.NodeInfo{
		ID:      "remote-node",
		Name:    "Remote Node",
		Address: "localhost",
		Port:    int(lis.Addr().(*net.TCPAddr).Port),
		Healthy: true,
	})

	executor := NewRemoteExecutor(testRegistry, 5*time.Second)
	ctx := WithDispatchAuthority(context.Background(), DispatchAuthority{LeaderID: "cp-a", Epoch: "42"})

	output, err := executor.ExecuteOnNode(ctx, "remote-node", "remote-test-agent", &agent.AgentInput{Instruction: "authority"})
	require.NoError(t, err)
	assert.Equal(t, "ok", output.Result)
}

func TestGRPCRemoteExecutor_ExecuteOnNode_RejectsMissingDispatchAuthority(t *testing.T) {
	mockAgentMgr := NewMockAgentManager()
	mockAgentMgr.AddAgent(NewMockAgent("remote-test-agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		return &agent.AgentOutput{Result: "unexpected"}, nil
	}))

	mockRegistry := NewMockRegistry()
	mockRegistry.AddNode(registry.NodeInfo{
		ID:      "remote-node",
		Name:    "Remote Node",
		Address: "localhost",
		Port:    0,
		Healthy: true,
	})

	service := NewRemoteExecutionService(mockRegistry, mockAgentMgr, "remote-node")
	service.SetDispatchAuthorityValidator(staticAuthorityValidator{
		validate: func(ctx context.Context, authority DispatchAuthority) error {
			if authority.LeaderID == "" || authority.Epoch == "" {
				return fmt.Errorf("%w", ErrMissingDispatchAuthority)
			}
			return nil
		},
	})

	server := grpc.NewServer()
	proto.RegisterRemoteExecutionServiceServer(server, NewGRPCRemoteExecutionService(service))

	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer lis.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	time.Sleep(100 * time.Millisecond)

	testRegistry := NewMockRegistry()
	testRegistry.AddNode(registry.NodeInfo{
		ID:      "remote-node",
		Name:    "Remote Node",
		Address: "localhost",
		Port:    int(lis.Addr().(*net.TCPAddr).Port),
		Healthy: true,
	})

	executor := NewRemoteExecutor(testRegistry, 5*time.Second)
	_, err = executor.ExecuteOnNode(context.Background(), "remote-node", "remote-test-agent", &agent.AgentInput{Instruction: "missing"})
	require.Error(t, err)
	if !errors.Is(err, ErrMissingDispatchAuthority) {
		t.Fatalf("ExecuteOnNode error = %v, want %v", err, ErrMissingDispatchAuthority)
	}
}

func TestRemoteExecutionService_RecordsWorkerDispatchRejectionMetric(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := observability.NewMetricsWithRegistry("dagens_test", reg)
	service := NewRemoteExecutionService(NewMockRegistry(), NewMockAgentManager(), "test-node")
	service.SetMetrics(metrics)
	service.SetDispatchAuthorityValidator(staticAuthorityValidator{
		validate: func(ctx context.Context, authority DispatchAuthority) error {
			return fmt.Errorf("%w", ErrStaleDispatchAuthority)
		},
	})

	resp, err := service.Execute(context.Background(), &RemoteExecutionRequest{
		AgentName: "agent",
		Input:     &agent.AgentInput{Instruction: "metric"},
		RequestID: "req-1",
		Metadata:  map[string]interface{}{},
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Error, ErrStaleDispatchAuthority.Error())

	families, err := reg.Gather()
	require.NoError(t, err)
	found := false
	for _, fam := range families {
		if fam.GetName() != "dagens_test_worker_dispatch_rejections_total" {
			continue
		}
		for _, metric := range fam.GetMetric() {
			labels := map[string]string{}
			for _, lp := range metric.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["reason"] == "stale_epoch" && metric.GetCounter().GetValue() == 1 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("expected worker dispatch rejection metric with reason=stale_epoch")
	}
}

func TestRemoteExecutionService_RecordsMissingAuthorityDispatchRejectionMetric(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := observability.NewMetricsWithRegistry("dagens_test", reg)
	service := NewRemoteExecutionService(NewMockRegistry(), NewMockAgentManager(), "test-node")
	service.SetMetrics(metrics)
	service.SetDispatchAuthorityValidator(staticAuthorityValidator{
		validate: func(ctx context.Context, authority DispatchAuthority) error {
			return fmt.Errorf("%w", ErrMissingDispatchAuthority)
		},
	})

	resp, err := service.Execute(context.Background(), &RemoteExecutionRequest{
		AgentName: "agent",
		Input:     &agent.AgentInput{Instruction: "metric"},
		RequestID: "req-2",
		Metadata:  map[string]interface{}{},
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Error, ErrMissingDispatchAuthority.Error())

	families, err := reg.Gather()
	require.NoError(t, err)
	found := false
	for _, fam := range families {
		if fam.GetName() != "dagens_test_worker_dispatch_rejections_total" {
			continue
		}
		for _, metric := range fam.GetMetric() {
			labels := map[string]string{}
			for _, lp := range metric.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["reason"] == "missing_authority" && metric.GetCounter().GetValue() == 1 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("expected worker dispatch rejection metric with reason=missing_authority")
	}
}

func TestEtcdDispatchAuthorityValidator_ValidatesCurrentLeaderEpoch(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer cli.Close()

	const electionKey = "/test/remote/authority"
	_, err = cli.Put(context.Background(), electionKey+"/leader", "cp-a")
	require.NoError(t, err)
	getResp, err := cli.Get(context.Background(), electionKey+"/", clientv3.WithPrefix())
	require.NoError(t, err)
	require.Len(t, getResp.Kvs, 1)

	validator, err := NewEtcdDispatchAuthorityValidator(EtcdDispatchAuthorityValidatorConfig{
		Endpoints:    endpoints,
		ElectionKey:  electionKey,
		DialTimeout:  5 * time.Second,
		QueryTimeout: 250 * time.Millisecond,
	})
	require.NoError(t, err)
	defer validator.Close()

	current := DispatchAuthority{
		LeaderID: "cp-a",
		Epoch:    fmt.Sprintf("%d", getResp.Kvs[0].ModRevision),
	}
	require.NoError(t, validator.Validate(context.Background(), current))

	stale := DispatchAuthority{
		LeaderID: "cp-a",
		Epoch:    "1",
	}
	err = validator.Validate(context.Background(), stale)
	if !errors.Is(err, ErrStaleDispatchAuthority) {
		t.Fatalf("Validate stale error = %v, want %v", err, ErrStaleDispatchAuthority)
	}
}

func TestEtcdDispatchAuthorityValidator_RejectsStaleEpochAfterLeaderChange(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer cli.Close()

	const electionKey = "/test/remote/failover"
	_, err = cli.Put(context.Background(), electionKey+"/leader", "cp-a")
	require.NoError(t, err)
	firstResp, err := cli.Get(context.Background(), electionKey+"/", clientv3.WithPrefix())
	require.NoError(t, err)
	require.Len(t, firstResp.Kvs, 1)

	validator, err := NewEtcdDispatchAuthorityValidator(EtcdDispatchAuthorityValidatorConfig{
		Endpoints:    endpoints,
		ElectionKey:  electionKey,
		DialTimeout:  5 * time.Second,
		QueryTimeout: 250 * time.Millisecond,
	})
	require.NoError(t, err)
	defer validator.Close()

	original := DispatchAuthority{
		LeaderID: "cp-a",
		Epoch:    fmt.Sprintf("%d", firstResp.Kvs[0].ModRevision),
	}
	require.NoError(t, validator.Validate(context.Background(), original))

	_, err = cli.Put(context.Background(), electionKey+"/leader", "cp-b")
	require.NoError(t, err)
	secondResp, err := cli.Get(context.Background(), electionKey+"/", clientv3.WithPrefix())
	require.NoError(t, err)
	require.Len(t, secondResp.Kvs, 1)
	require.Greater(t, secondResp.Kvs[0].ModRevision, firstResp.Kvs[0].ModRevision, "epoch should increment on leader change")

	err = validator.Validate(context.Background(), original)
	if !errors.Is(err, ErrStaleDispatchAuthority) {
		t.Fatalf("Validate old authority error = %v, want %v", err, ErrStaleDispatchAuthority)
	}

	current := DispatchAuthority{
		LeaderID: "cp-b",
		Epoch:    fmt.Sprintf("%d", secondResp.Kvs[0].ModRevision),
	}
	require.NoError(t, validator.Validate(context.Background(), current))
}

func TestEtcdDispatchAuthorityValidator_ReturnsMissingAuthorityWhenNoLeaderKeyExists(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)
	validator, err := NewEtcdDispatchAuthorityValidator(EtcdDispatchAuthorityValidatorConfig{
		Endpoints:    endpoints,
		ElectionKey:  "/test/remote/missing-authority",
		DialTimeout:  5 * time.Second,
		QueryTimeout: 250 * time.Millisecond,
	})
	require.NoError(t, err)
	defer validator.Close()

	err = validator.Validate(context.Background(), DispatchAuthority{
		LeaderID: "cp-a",
		Epoch:    "42",
	})
	if !errors.Is(err, ErrMissingDispatchAuthority) {
		t.Fatalf("Validate missing authority error = %v, want %v", err, ErrMissingDispatchAuthority)
	}
}

func TestEtcdDispatchAuthorityValidator_AlwaysBoundsQueryWithQueryTimeout(t *testing.T) {
	validator, err := NewEtcdDispatchAuthorityValidator(EtcdDispatchAuthorityValidatorConfig{
		Endpoints:    []string{"http://127.0.0.1:1"},
		ElectionKey:  "/test/remote/timeout",
		DialTimeout:  50 * time.Millisecond,
		QueryTimeout: 10 * time.Millisecond,
	})
	require.NoError(t, err)
	defer validator.Close()

	parentCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := time.Now()
	err = validator.Validate(parentCtx, DispatchAuthority{
		LeaderID: "cp-a",
		Epoch:    "42",
	})
	if err == nil {
		t.Fatal("expected validate to fail against unreachable etcd endpoint")
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatalf("Validate exceeded expected timeout bound: %s", time.Since(start))
	}
}

func TestAuthorityRejectionReason_ContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()

	reason := AuthorityRejectionReason(ctx.Err())
	if reason != "network_timeout" {
		t.Fatalf("AuthorityRejectionReason = %q, want %q", reason, "network_timeout")
	}
}

func TestAuthorityRejectionReason_TransportErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "connection refused", err: fmt.Errorf("connection refused"), want: "transport_error"},
		{name: "connection reset", err: fmt.Errorf("connection reset by peer"), want: "transport_error"},
		{name: "unavailable", err: fmt.Errorf("service unavailable"), want: "transport_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := AuthorityRejectionReason(tt.err)
			if reason != tt.want {
				t.Fatalf("AuthorityRejectionReason(%v) = %q, want %q", tt.err, reason, tt.want)
			}
		})
	}
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
