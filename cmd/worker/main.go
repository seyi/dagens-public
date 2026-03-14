package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote"
	pb "github.com/seyi/dagens/pkg/remote/proto"
	"github.com/seyi/dagens/pkg/secrets"
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
	var authorityValidator *remote.EtcdDispatchAuthorityValidator

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
			KeyPrefix:        os.Getenv("REGISTRY_KEY_PREFIX"),
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
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SCHEDULER_LEADERSHIP_BACKEND")), "etcd") {
		endpoints := splitNonEmptyCSV(etcdEndpoints)
		if len(endpoints) == 0 {
			log.Fatal("SCHEDULER_LEADERSHIP_BACKEND=etcd on worker requires ETCD_ENDPOINTS")
		}
		leaderKey := strings.TrimSpace(os.Getenv("SCHEDULER_LEADERSHIP_KEY"))
		if leaderKey == "" {
			leaderKey = "/dagens/control-plane/scheduler"
		}
		dialTimeout := 5 * time.Second
		if raw := strings.TrimSpace(os.Getenv("SCHEDULER_LEADERSHIP_DIAL_TIMEOUT")); raw != "" {
			timeout, err := time.ParseDuration(raw)
			if err != nil {
				log.Fatalf("Invalid SCHEDULER_LEADERSHIP_DIAL_TIMEOUT %q: %v", raw, err)
			}
			dialTimeout = timeout
		}
		queryTimeout := 2 * time.Second
		if raw := strings.TrimSpace(os.Getenv("SCHEDULER_LEADERSHIP_QUERY_TIMEOUT")); raw != "" {
			timeout, err := time.ParseDuration(raw)
			if err != nil {
				log.Fatalf("Invalid SCHEDULER_LEADERSHIP_QUERY_TIMEOUT %q: %v", raw, err)
			}
			queryTimeout = timeout
		}
		var err error
		authorityValidator, err = remote.NewEtcdDispatchAuthorityValidator(remote.EtcdDispatchAuthorityValidatorConfig{
			Endpoints:    endpoints,
			ElectionKey:  leaderKey,
			DialTimeout:  dialTimeout,
			QueryTimeout: queryTimeout,
		})
		if err != nil {
			log.Fatalf("Failed to initialize worker dispatch authority validator: %v", err)
		}
		defer authorityValidator.Close()
		service.SetDispatchAuthorityValidator(authorityValidator)
		log.Printf("Worker dispatch authority validation enabled (key=%s endpoints=%s)", leaderKey, strings.Join(endpoints, ","))
	}
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

	if delay := simulatedAgentDelay(input); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	if agentName == "hitl-drill-human" {
		resumeReady := false
		selectedOption := ""
		if input != nil && input.Context != nil {
			if raw, ok := input.Context["hitl_drill_resume_ready"].(bool); ok {
				resumeReady = raw
			}
			if raw, ok := input.Context["hitl_callback_selected_option"].(string); ok {
				selectedOption = raw
			}
		}
		if !resumeReady {
			return nil, fmt.Errorf("human interaction pending: checkpoint required")
		}
		return &agent.AgentOutput{
			Result: fmt.Sprintf("HITL drill resumed with selection=%s", selectedOption),
			Metadata: map[string]interface{}{
				"worker_id":        os.Getenv("NODE_ID"),
				"hitl_drill_agent": true,
				"selected_option":  selectedOption,
			},
		}, nil
	}

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
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	endpoints := workerHeartbeatEndpoints(apiServerURL)

	send := func() {
		payload := map[string]interface{}{
			"node_id":          nodeID,
			"in_flight":        service.CurrentInFlight(),
			"max_concurrency":  maxConcurrency,
			"report_timestamp": time.Now().UTC(),
		}

		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("worker heartbeat marshal failed: %v", err)
			return
		}

		var lastErr error
		for _, endpoint := range endpoints {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
			if err != nil {
				lastErr = err
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			if workerToken != "" {
				req.Header.Set("X-Dagens-Worker-Token", workerToken)
			}

			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			if resp.StatusCode >= http.StatusMultipleChoices {
				lastErr = fmt.Errorf("status=%s", resp.Status)
				resp.Body.Close()
				continue
			}
			resp.Body.Close()
			return
		}
		if lastErr != nil {
			log.Printf("worker heartbeat failed across all endpoints: %v", lastErr)
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

func workerHeartbeatEndpoints(primary string) []string {
	endpointSet := make(map[string]struct{})
	ordered := make([]string, 0, 4)

	add := func(base string) {
		base = strings.TrimSpace(base)
		if base == "" {
			return
		}
		base = strings.TrimSuffix(base, "/")
		full := base + "/v1/internal/worker_capacity"
		if _, ok := endpointSet[full]; ok {
			return
		}
		endpointSet[full] = struct{}{}
		ordered = append(ordered, full)
	}

	add(primary)
	if raw := os.Getenv("API_SERVER_FALLBACK_URLS"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			add(part)
		}
	}

	u, err := url.Parse(primary)
	if err == nil && strings.EqualFold(u.Hostname(), "api-lb") {
		add("http://api-server:8080")
		add("http://api-server-b:8080")
	}

	return ordered
}

func simulatedAgentDelay(input *agent.AgentInput) time.Duration {
	if input == nil || input.Context == nil {
		return 0
	}

	raw, ok := input.Context["dagens_simulated_sleep_ms"]
	if !ok || raw == nil {
		return 0
	}

	switch value := raw.(type) {
	case float64:
		if value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	case int:
		if value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	case int64:
		if value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	case json.Number:
		if parsed, err := value.Int64(); err == nil && parsed > 0 {
			return time.Duration(parsed) * time.Millisecond
		}
	}

	return 0
}

func splitNonEmptyCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
