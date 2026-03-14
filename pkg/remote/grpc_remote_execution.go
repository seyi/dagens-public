// Package remote provides gRPC server and client implementations for remote agent execution
package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/remote/proto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// GRPCRemoteExecutionService implements the gRPC service for remote execution
type GRPCRemoteExecutionService struct {
	proto.UnsafeRemoteExecutionServiceServer
	service *RemoteExecutionService
}

// NewGRPCRemoteExecutionService creates a new gRPC remote execution service
func NewGRPCRemoteExecutionService(service *RemoteExecutionService) *GRPCRemoteExecutionService {
	return &GRPCRemoteExecutionService{
		service: service,
	}
}

// Execute handles remote execution requests via gRPC
func (s *GRPCRemoteExecutionService) Execute(ctx context.Context, req *proto.ExecuteRequest) (*proto.ExecuteResponse, error) {
	// Extract trace context from metadata
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(md))
	}

	// Deserialize the input from JSON
	var input agent.AgentInput
	if err := json.Unmarshal([]byte(req.InputJson), &input); err != nil {
		return &proto.ExecuteResponse{
			Error:     fmt.Sprintf("failed to deserialize input: %v", err),
			NodeId:    s.service.localNodeID,
			RequestId: req.RequestId,
		}, nil
	}

	// Execute the agent
	output, err := s.service.Execute(ctx, &RemoteExecutionRequest{
		AgentName: req.AgentName,
		Input:     &input,
		NodeID:    req.NodeId,
		RequestID: req.RequestId,
		Metadata:  convertStringMap(req.Metadata),
	})

	if err != nil {
		return &proto.ExecuteResponse{
			Error:     err.Error(),
			NodeId:    s.service.localNodeID,
			RequestId: req.RequestId,
		}, nil
	}

	// Check for execution error from the service response
	if output.Error != "" {
		return &proto.ExecuteResponse{
			Error:      output.Error,
			NodeId:     s.service.localNodeID,
			RequestId:  req.RequestId,
			DurationMs: int64(output.Duration.Milliseconds()),
		}, nil
	}

	// Serialize the output to JSON
	outputBytes, err := json.Marshal(output.Output)
	if err != nil {
		return &proto.ExecuteResponse{
			Error:     fmt.Sprintf("failed to serialize output: %v", err),
			NodeId:    s.service.localNodeID,
			RequestId: req.RequestId,
		}, nil
	}

	return &proto.ExecuteResponse{
		OutputJson: string(outputBytes),
		NodeId:     s.service.localNodeID,
		RequestId:  req.RequestId,
		DurationMs: int64(output.Duration.Milliseconds()),
	}, nil
}

// HealthCheck implements the health check endpoint
func (s *GRPCRemoteExecutionService) HealthCheck(ctx context.Context, req *proto.HealthCheckRequest) (*proto.HealthCheckResponse, error) {
	return &proto.HealthCheckResponse{
		Healthy:   true,
		NodeId:    s.service.localNodeID,
		Timestamp: time.Now().Unix(),
	}, nil
}

// GRPCRemoteExecutor implements the client side of remote execution
type GRPCRemoteExecutor struct {
	registry *RemoteExecutor
}

// NewGRPCRemoteExecutor creates a new gRPC-based remote executor
func NewGRPCRemoteExecutor(registry *RemoteExecutor) *GRPCRemoteExecutor {
	return &GRPCRemoteExecutor{
		registry: registry,
	}
}

// ExecuteOnNode executes an agent on a remote node using gRPC
func (re *GRPCRemoteExecutor) ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Get node address from registry
	addr, err := re.registry.GetNodeAddress(nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get node address: %w", err)
	}

	// Create gRPC connection
	conn, err := grpc.Dial(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to remote node %s: %w", addr, err)
	}
	defer conn.Close()

	// Inject trace context into metadata
	md := metadata.New(nil)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(md))
	ctx = metadata.NewOutgoingContext(ctx, md)

	// Serialize input to JSON
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize input: %w", err)
	}

	// Create gRPC client
	client := proto.NewRemoteExecutionServiceClient(conn)

	// Create request
	req := &proto.ExecuteRequest{
		AgentName: agentName,
		InputJson: string(inputBytes),
		NodeId:    nodeID,
		RequestId: fmt.Sprintf("grpc-remote-exec-%d", time.Now().UnixNano()),
		Metadata:  map[string]string{"source_node": "TODO"}, // Would get from registry
	}
	if authority, ok := dispatchAuthorityFromContext(ctx); ok {
		for key, value := range authority.metadata() {
			req.Metadata[key] = fmt.Sprintf("%v", value)
		}
	}

	// Execute the request
	resp, err := client.Execute(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("gRPC execution failed: %w", err)
	}

	if resp.Error != "" {
		switch {
		case strings.Contains(resp.Error, ErrStaleDispatchAuthority.Error()):
			return nil, ErrStaleDispatchAuthority
		case strings.Contains(resp.Error, ErrMissingDispatchAuthority.Error()):
			return nil, ErrMissingDispatchAuthority
		}
		return nil, fmt.Errorf("remote agent error: %s", resp.Error)
	}

	// Deserialize output
	var output agent.AgentOutput
	if err := json.Unmarshal([]byte(resp.OutputJson), &output); err != nil {
		return nil, fmt.Errorf("failed to deserialize output: %w", err)
	}

	return &output, nil
}

// convertStringMap converts map[string]string to map[string]interface{}
func convertStringMap(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}

// convertInterfaceMap converts map[string]interface{} to map[string]string
func convertInterfaceMap(m map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		if str, ok := v.(string); ok {
			result[k] = str
		} else {
			result[k] = fmt.Sprintf("%v", v)
		}
	}
	return result
}
