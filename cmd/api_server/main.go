package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/seyi/dagens/pkg/api"
	"github.com/seyi/dagens/pkg/auth"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote"
	"github.com/seyi/dagens/pkg/scheduler"
	"github.com/seyi/dagens/pkg/secrets"
	"github.com/seyi/dagens/pkg/telemetry"
)

func main() {
	// 1. Initialize Telemetry
	telemetry.InitGlobalTelemetry("dagens-api-server", "1.0.0")
	defer telemetry.ShutdownGlobalTelemetry(context.Background())
	// 2. Setup Infrastructure
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

	// Initialize Authenticator
	jwtSecret, err := secretMgr.GetSecret(context.Background(), secrets.Config{
		Name:     "JWT_SECRET",
		Provider: "env", // Default to env for now
	})
	if err != nil {
		log.Printf("Warning: JWT_SECRET not found, using development default")
		jwtSecret = "dagens-dev-secret-change-me-in-production"
	}
	authenticator := auth.NewJWTAuthenticator(jwtSecret, "dagens-api", "dagens-clients")

	var reg registry.Registry
	// var err error // err is already declared in global scope or not needed here if handled immediately, but `distReg, err :=` declares it locally.
	// Actually, NewDistributedAgentRegistry returns (..., error), so we need to handle it.

	etcdEndpoints := os.Getenv("ETCD_ENDPOINTS")
	if etcdEndpoints != "" {
		log.Printf("Using Distributed Registry with etcd: %s", etcdEndpoints)
		distReg, err := registry.NewDistributedAgentRegistry(registry.RegistryConfig{
			EtcdEndpoints: []string{etcdEndpoints},
			NodeID:        "api-server",
			NodeName:      "API Server",
			NodeAddress:   os.Getenv("POD_IP"), // Kubernetes pod IP
			NodePort:      8080,
			LeaseTTL:      10,
		})
		if err != nil {
			log.Fatalf("Failed to create distributed registry: %v", err)
		}

		// Start registry background processes
		if err := distReg.Start(context.Background()); err != nil {
			log.Fatalf("Failed to start distributed registry: %v", err)
		}
		// Ensure registry is stopped on shutdown
		defer distReg.Stop()

		reg = distReg
	} else {
		log.Println("Using Mock Registry (standalone mode)")
		reg = &MockRegistry{
			nodes: []registry.NodeInfo{
				{ID: "worker-1", Name: "Worker 1", Address: "worker-1", Port: 50051, Healthy: true},
				{ID: "worker-2", Name: "Worker 2", Address: "worker-2", Port: 50051, Healthy: true},
			},
		}
	}

	// Use the real RemoteExecutor to dial the workers
	realExec := remote.NewRemoteExecutor(reg, 30*time.Second)

	schedulerCfg := scheduler.DefaultSchedulerConfig()
	if timeoutRaw := strings.TrimSpace(os.Getenv("SCHEDULER_RECOVERY_TIMEOUT")); timeoutRaw != "" {
		timeout, err := time.ParseDuration(timeoutRaw)
		if err != nil {
			log.Fatalf("Invalid SCHEDULER_RECOVERY_TIMEOUT %q: %v", timeoutRaw, err)
		}
		schedulerCfg.RecoveryTimeout = timeout
	}

	sched := scheduler.NewSchedulerWithConfig(reg, realExec, schedulerCfg)
	var closeTransitionStore func()

	transitionBackend := strings.ToLower(strings.TrimSpace(os.Getenv("SCHEDULER_TRANSITION_STORE")))
	if transitionBackend == "" {
		transitionBackend = "memory"
	}

	switch transitionBackend {
	case "memory":
		log.Println("Using in-memory scheduler transition store")
	case "postgres":
		dsn := strings.TrimSpace(os.Getenv("SCHEDULER_TRANSITION_POSTGRES_DSN"))
		if dsn == "" {
			dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
		}
		if dsn == "" {
			log.Fatal("SCHEDULER_TRANSITION_STORE=postgres requires SCHEDULER_TRANSITION_POSTGRES_DSN or DATABASE_URL")
		}

		store, err := scheduler.NewPostgresTransitionStoreFromURL(context.Background(), dsn)
		if err != nil {
			log.Fatalf("Failed to initialize postgres transition store: %v", err)
		}
		if err := sched.SetTransitionStore(store); err != nil {
			store.Close()
			log.Fatalf("Failed to set scheduler transition store: %v", err)
		}
		closeTransitionStore = store.Close
		log.Println("Using PostgreSQL scheduler transition store")
	default:
		log.Fatalf("Unsupported SCHEDULER_TRANSITION_STORE value: %q", transitionBackend)
	}

	sched.Start()

	server := api.NewServer(sched)

	// 5. Start HTTP Server
	port := ":8080"
	log.Printf("Starting Dagens Control API on %s", port)

	// Apply auth middleware unless DEV_MODE is enabled
	var handler http.Handler = server.Routes()
	if os.Getenv("DEV_MODE") != "true" {
		handler = auth.HTTPMiddleware(authenticator)(server.Routes())
	} else {
		log.Println("DEV_MODE enabled - skipping authentication")
	}

	srv := &http.Server{
		Addr:    port,
		Handler: handler,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// 6. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}
	sched.Stop()
	realExec.Close()
	if closeTransitionStore != nil {
		closeTransitionStore()
	}
	log.Println("Server exiting")
}

// Cluster-aware MockRegistry for the showcase
type MockRegistry struct {
	nodes []registry.NodeInfo
}

func (m *MockRegistry) GetHealthyNodes() []registry.NodeInfo { return m.nodes }
func (m *MockRegistry) GetNode(id string) (registry.NodeInfo, bool) {
	for _, n := range m.nodes {
		if n.ID == id {
			return n, true
		}
	}
	return registry.NodeInfo{}, false
}
func (m *MockRegistry) GetNodes() []registry.NodeInfo                     { return m.nodes }
func (m *MockRegistry) GetNodeID() string                                 { return "api-server" }
func (m *MockRegistry) GetNodesByCapability(c string) []registry.NodeInfo { return m.nodes }
func (m *MockRegistry) GetNodeCount() int                                 { return len(m.nodes) }
func (m *MockRegistry) GetHealthyNodeCount() int                          { return len(m.nodes) }
func (m *MockRegistry) Start(ctx context.Context) error                   { return nil }
func (m *MockRegistry) Stop() error                                       { return nil }
