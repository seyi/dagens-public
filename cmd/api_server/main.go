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
	"github.com/seyi/dagens/pkg/observability"
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
		} else {
			log.Printf("Warning: failed to initialize Vault provider: %v (continuing with env provider)", err)
		}
	}

	// Initialize Authenticator
	devMode := strings.EqualFold(strings.TrimSpace(os.Getenv("DEV_MODE")), "true")
	environment := strings.ToLower(strings.TrimSpace(os.Getenv("ENVIRONMENT")))

	jwtSecret, err := secretMgr.GetSecret(context.Background(), secrets.Config{
		Name:     "JWT_SECRET",
		Provider: "env", // Default to env for now
	})
	if err != nil {
		if environment == "production" && !devMode {
			log.Fatal("JWT_SECRET is required when ENVIRONMENT=production")
		}
		log.Printf("Warning: JWT_SECRET not found; using development fallback (not for production)")
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
			NodeCapabilities: []string{"control-plane"},
			NodeMetadata: map[string]string{
				"role": "control-plane",
			},
			LeaseTTL:      10,
		})
		if err != nil {
			log.Fatalf("Failed to create distributed registry: %v", err)
		}

		// Start registry background processes with a bounded startup context.
		startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := distReg.Start(startCtx); err != nil {
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
	if resumeRaw := strings.TrimSpace(os.Getenv("SCHEDULER_RESUME_RECOVERED_QUEUED_JOBS")); resumeRaw != "" {
		schedulerCfg.EnableResumeRecoveredQueuedJobs = strings.EqualFold(resumeRaw, "true")
	}

	sched := scheduler.NewSchedulerWithConfig(reg, realExec, schedulerCfg)
	var closeTransitionStore func()

	if strings.EqualFold(strings.TrimSpace(os.Getenv("SCHEDULER_LEADERSHIP_BACKEND")), "etcd") {
		identity := strings.TrimSpace(os.Getenv("CONTROL_PLANE_ID"))
		if identity == "" {
			identity = strings.TrimSpace(os.Getenv("HOSTNAME"))
		}
		if identity == "" {
			identity = "api-server"
		}

		leaderKey := strings.TrimSpace(os.Getenv("SCHEDULER_LEADERSHIP_KEY"))
		if leaderKey == "" {
			leaderKey = "/dagens/control-plane/scheduler"
		}

		raw := strings.TrimSpace(os.Getenv("ETCD_ENDPOINTS"))
		if raw == "" {
			log.Fatal("SCHEDULER_LEADERSHIP_BACKEND=etcd requires ETCD_ENDPOINTS")
		}
		endpoints := make([]string, 0, 3)
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				endpoints = append(endpoints, part)
			}
		}
		if len(endpoints) == 0 {
			log.Fatal("SCHEDULER_LEADERSHIP_BACKEND=etcd requires at least one non-empty ETCD_ENDPOINTS value")
		}

		leadershipTTL := 10
		if ttlRaw := strings.TrimSpace(os.Getenv("SCHEDULER_LEADERSHIP_TTL_SECONDS")); ttlRaw != "" {
			parsed, err := time.ParseDuration(ttlRaw + "s")
			if err != nil {
				log.Fatalf("Invalid SCHEDULER_LEADERSHIP_TTL_SECONDS %q: %v", ttlRaw, err)
			}
			leadershipTTL = int(parsed / time.Second)
			if leadershipTTL <= 0 {
				log.Fatal("SCHEDULER_LEADERSHIP_TTL_SECONDS must be >= 1")
			}
		}

		dialTimeout := 5 * time.Second
		if timeoutRaw := strings.TrimSpace(os.Getenv("SCHEDULER_LEADERSHIP_DIAL_TIMEOUT")); timeoutRaw != "" {
			parsed, err := time.ParseDuration(timeoutRaw)
			if err != nil {
				log.Fatalf("Invalid SCHEDULER_LEADERSHIP_DIAL_TIMEOUT %q: %v", timeoutRaw, err)
			}
			dialTimeout = parsed
		}

		leadershipProvider, err := scheduler.NewEtcdLeadershipProvider(scheduler.EtcdLeadershipProviderConfig{
			Endpoints:   endpoints,
			ElectionKey: leaderKey,
			Identity:    identity,
			SessionTTL:  leadershipTTL,
			DialTimeout: dialTimeout,
		})
		if err != nil {
			log.Fatalf("Failed to initialize etcd leadership provider: %v", err)
		}
		if err := sched.SetLeadershipProvider(leadershipProvider); err != nil {
			log.Fatalf("Failed to set scheduler leadership provider: %v", err)
		}
		log.Printf("Using etcd scheduler leadership provider (identity=%s key=%s endpoints=%s)", identity, leaderKey, strings.Join(endpoints, ","))
	}

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

	hitlRuntime, err := newHITLCallbackRuntimeFromEnv(context.Background())
	if err != nil {
		log.Fatalf("Failed to initialize HITL callback runtime: %v", err)
	}
	if hitlRuntime != nil {
		defer hitlRuntime.close()
		log.Printf("HITL callback endpoint enabled at %s", hitlRuntime.path)
	}

	// Compose protected route tree first.
	protectedMux := http.NewServeMux()
	protectedMux.Handle("/", server.Routes())
	protectedMux.Handle("/metrics", observability.Handler())

	// Apply auth middleware unless DEV_MODE is enabled
	var protectedHandler http.Handler = protectedMux
	if !devMode {
		protectedHandler = auth.HTTPMiddleware(authenticator)(protectedMux)
	} else {
		log.Println("WARNING: DEV_MODE enabled - authentication is disabled")
	}

	// Root mux allows specific public endpoints (HITL callback) while keeping
	// existing API routes under auth middleware.
	mux := http.NewServeMux()
	mux.Handle("/", protectedHandler)
	if hitlRuntime != nil {
		mux.Handle(hitlRuntime.path, hitlRuntime.handler)
	}

	srv := &http.Server{
		Addr:    port,
		Handler: mux,
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
	// Shutdown order:
	// 1) scheduler stop (may flush final transitions)
	// 2) remote executor close (drain transport resources)
	// 3) transition store close
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
func (m *MockRegistry) GetNodes() []registry.NodeInfo { return m.nodes }
func (m *MockRegistry) GetNodeID() string             { return "api-server" }
func (m *MockRegistry) GetNodesByCapability(c string) []registry.NodeInfo {
	// Mock registry does not model per-capability filtering; return all nodes.
	return m.nodes
}
func (m *MockRegistry) GetNodeCount() int               { return len(m.nodes) }
func (m *MockRegistry) GetHealthyNodeCount() int        { return len(m.nodes) }
func (m *MockRegistry) Start(ctx context.Context) error { return nil }
func (m *MockRegistry) Stop() error                     { return nil }
