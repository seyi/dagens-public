package main

import (
	"context"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote"
	"github.com/seyi/dagens/pkg/secrets"
	pb "github.com/seyi/dagens/pkg/remote/proto"
	"github.com/seyi/dagens/pkg/telemetry"
	"google.golang.org/grpc"
)

func main() {
	nodeID := os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID = fmt.Sprintf("worker-%d", time.Now().Unix())
	}

	// 1. Initialize Telemetry
	telemetry.InitGlobalTelemetry("dagens-worker", "1.0.0")
	defer telemetry.ShutdownGlobalTelemetry(context.Background())

	// 2. Setup Infrastructure
	var distReg *registry.DistributedAgentRegistry
	etcdEndpoints := os.Getenv("ETCD_ENDPOINTS")
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "50051"
	}
	portInt := 50051 // Default
	fmt.Sscanf(port, "%d", &portInt)

	if etcdEndpoints != "" {
		log.Printf("Using Distributed Registry with etcd: %s", etcdEndpoints)
		var err error
		distReg, err = registry.NewDistributedAgentRegistry(registry.RegistryConfig{
			EtcdEndpoints:    []string{etcdEndpoints},
			NodeID:           nodeID,
			NodeName:         fmt.Sprintf("Worker Node %s", nodeID),
			NodeAddress:      os.Getenv("POD_IP"), // Kubernetes pod IP or Host IP
			NodePort:         portInt,
			NodeCapabilities: []string{"deepseek/deepseek-chat", "generic-agent"},
			LeaseTTL:         10,
		})
		if err != nil {
			log.Fatalf("Failed to create distributed registry: %v", err)
		}
		
		// Start registry to register this node
		if err := distReg.Start(context.Background()); err != nil {
			log.Fatalf("Failed to start distributed registry: %v", err)
		}
		defer distReg.Stop()
	} else {
		log.Println("Using nil Registry (standalone/passive mode)")
	}

	// 3. Setup Secrets Management
	secretMgr := secrets.NewManager()
	secretMgr.RegisterProvider(secrets.NewEnvProvider())
	if vaultAddr := os.Getenv("VAULT_ADDR"); vaultAddr != "" {
		vaultToken := os.Getenv("VAULT_TOKEN")
		vaultProv, err := secrets.NewVaultProvider(secrets.VaultConfig{
			Addr:  vaultAddr,
			Token: vaultToken,
		})
		if err == nil {
			secretMgr.RegisterProvider(vaultProv)
			log.Println("Registered Vault secret provider")
		}
	}

	// 4. Setup Agent Manager (Local execution logic)
	agentManager := &SimpleAgentManager{
		secretMgr: secretMgr,
	}

	// 5. Setup Remote Execution Service
	service := remote.NewRemoteExecutionService(distReg, agentManager, nodeID)
	grpcHandler := remote.NewGRPCRemoteExecutionService(service)

	heartbeatCtx, cancelHeartbeats := context.WithCancel(context.Background())
	defer cancelHeartbeats()
	startWorkerCapacityHeartbeat(heartbeatCtx, nodeID, service)

	// 6. Start gRPC Server
	// Port already parsed above
	
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterRemoteExecutionServiceServer(s, grpcHandler)

	log.Printf("Worker %s listening on %s", nodeID, port)

	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// 5. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down worker...")
	cancelHeartbeats()
	s.GracefulStop()
	log.Println("Worker exiting")
}

// SimpleAgentManager implements the remote.AgentManager interface
type SimpleAgentManager struct {
	secretMgr *secrets.Manager
}

func (m *SimpleAgentManager) GetAgent(name string) (agent.Agent, error) {
	return nil, nil
}

func (m *SimpleAgentManager) ExecuteAgent(ctx context.Context, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	log.Printf("Executing agent %s: %s", agentName, input.Instruction)
	
	// Use SecretManager to get the API Key
	apiKey := ""
	if m.secretMgr != nil {
		// Try to get from vault or env via manager
		val, err := m.secretMgr.GetSecret(ctx, secrets.Config{
			Name:     "OPENROUTER_API_KEY",
			Provider: "vault", // Prefer vault if available
		})
		if err == nil {
			apiKey = val
		} else {
			// Fallback to env via manager
			val, err = m.secretMgr.GetSecret(ctx, secrets.Config{
				Name:     "OPENROUTER_API_KEY",
				Provider: "env",
			})
			if err == nil {
				apiKey = val
			}
		}
	}

	if apiKey == "" {
		// Fallback to simulation if no key provided (safe default)
		log.Println("No OPENROUTER_API_KEY found, simulating execution.")
		time.Sleep(100 * time.Millisecond)
		return &agent.AgentOutput{Result: fmt.Sprintf("Simulated result for %s", agentName)}, nil
	}

	// Real AI Execution via OpenRouter (DeepSeek)
	resp, err := callDeepSeek(ctx, apiKey, input.Instruction)
	if err != nil {
		return nil, fmt.Errorf("AI execution failed: %w", err)
	}

	return &agent.AgentOutput{
		Result: resp,
		Metadata: map[string]interface{}{
			"worker_id": os.Getenv("NODE_ID"),
			"model":     "deepseek/deepseek-chat",
			"provider":  "openrouter",
		},
	}, nil
}

type openRouterRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

func callDeepSeek(ctx context.Context, apiKey, prompt string) (string, error) {
	reqBody := openRouterRequest{
		Model: "deepseek/deepseek-chat",
		Messages: []message{
			{Role: "user", Content: prompt},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://dagens.ai") // OpenRouter requirement
	req.Header.Set("X-Title", "Dagens Distributed Runtime")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("OpenRouter API error: %s", resp.Status)
	}

	var result openRouterResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices returned from AI")
	}

	return result.Choices[0].Message.Content, nil
}

func startWorkerCapacityHeartbeat(ctx context.Context, nodeID string, service *remote.RemoteExecutionService) {
	apiServerURL := os.Getenv("API_SERVER_URL")
	if apiServerURL == "" {
		apiServerURL = "http://api-server:8080"
	}

	heartbeatInterval := 2 * time.Second
	if raw := os.Getenv("WORKER_HEARTBEAT_INTERVAL_SECONDS"); raw != "" {
		var seconds int
		if _, err := fmt.Sscanf(raw, "%d", &seconds); err == nil && seconds > 0 {
			heartbeatInterval = time.Duration(seconds) * time.Second
		}
	}

	maxConcurrency := 1
	if raw := os.Getenv("WORKER_MAX_CONCURRENCY"); raw != "" {
		var configured int
		if _, err := fmt.Sscanf(raw, "%d", &configured); err == nil && configured > 0 {
			maxConcurrency = configured
		}
	}

	workerToken := os.Getenv("WORKER_HEARTBEAT_TOKEN")
	client := &http.Client{Timeout: 5 * time.Second}
	endpoint := apiServerURL + "/v1/internal/worker_capacity"

	send := func() {
		payload := map[string]interface{}{
			"node_id":         nodeID,
			"in_flight":       service.CurrentInFlight(),
			"max_concurrency": maxConcurrency,
			"report_timestamp": time.Now().UTC(),
		}

		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("worker heartbeat marshal failed: %v", err)
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			log.Printf("worker heartbeat request build failed: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if workerToken != "" {
			req.Header.Set("X-Dagens-Worker-Token", workerToken)
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("worker heartbeat failed: %v", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= http.StatusMultipleChoices {
			log.Printf("worker heartbeat rejected: status=%s", resp.Status)
		}
	}

	go func() {
		send()
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()
}
