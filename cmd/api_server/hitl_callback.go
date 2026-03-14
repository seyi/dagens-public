package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/hitl"
	"github.com/seyi/dagens/pkg/scheduler"
)

const (
	defaultHITLCallbackPath = "/api/human-callback"
	maxCallbackBodyBytes    = 1 << 20 // 1 MiB
)

type hitlCallbackRuntime struct {
	path         string
	handler      http.Handler
	drillPath    string
	drillHandler http.Handler
	close        func() error
	securityMgr  *hitl.SecurityManager
	baseURL      string
	checkpoints  hitl.CheckpointStore
}

const (
	hitlDrillGraphID      = "ha-hitl-drill"
	hitlDrillGraphVersion = "v1"
	defaultHITLDrillPath  = "/api/dev/hitl-failover-drill"
)

func newHITLCallbackRuntimeFromEnv(ctx context.Context, sched *scheduler.Scheduler) (*hitlCallbackRuntime, error) {
	if !envBool("HITL_CALLBACK_ENABLED", false) {
		return nil, nil
	}

	secret := strings.TrimSpace(os.Getenv("HITL_CALLBACK_SECRET"))
	if secret == "" {
		return nil, fmt.Errorf("HITL_CALLBACK_ENABLED=true requires HITL_CALLBACK_SECRET")
	}

	redisAddr := strings.TrimSpace(os.Getenv("HITL_REDIS_ADDR"))
	if redisAddr == "" {
		return nil, fmt.Errorf("HITL_CALLBACK_ENABLED=true requires HITL_REDIS_ADDR")
	}

	redisDB, err := envInt("HITL_REDIS_DB", 0)
	if err != nil {
		return nil, fmt.Errorf("invalid HITL_REDIS_DB: %w", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:      redisAddr,
		Password:  os.Getenv("HITL_REDIS_PASSWORD"),
		DB:        redisDB,
		TLSConfig: redisTLSConfigFromEnv(),
	})

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("failed to connect to HITL Redis: %w", err)
	}

	block, err := envDuration("HITL_RESUMPTION_BLOCK", 5*time.Second)
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("invalid HITL_RESUMPTION_BLOCK: %w", err)
	}
	claimIdle, err := envDuration("HITL_RESUMPTION_CLAIM_IDLE", 30*time.Second)
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("invalid HITL_RESUMPTION_CLAIM_IDLE: %w", err)
	}
	visibilityGrace, err := envDuration("HITL_RESUMPTION_VISIBILITY_GRACE", 5*time.Minute)
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("invalid HITL_RESUMPTION_VISIBILITY_GRACE: %w", err)
	}

	queue, err := hitl.NewRedisResumptionQueue(redisClient, hitl.RedisQueueConfig{
		Stream:          strings.TrimSpace(envString("HITL_RESUMPTION_STREAM", "hitl:resumption")),
		Group:           strings.TrimSpace(envString("HITL_RESUMPTION_GROUP", "hitl_workers")),
		Consumer:        strings.TrimSpace(envString("HITL_RESUMPTION_CONSUMER", defaultConsumerID())),
		Block:           block,
		ClaimIdle:       claimIdle,
		VisibilityGrace: visibilityGrace,
	})
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("failed to initialize HITL Redis Streams queue: %w", err)
	}

	idStore := hitl.NewRedisIdempotencyStore(redisClient)
	metrics := hitl.NewMetricsCollector().GetMetrics()
	checkpointDSN := strings.TrimSpace(envString("HITL_CHECKPOINT_POSTGRES_DSN", envString("SCHEDULER_TRANSITION_POSTGRES_DSN", envString("DATABASE_URL", ""))))
	if checkpointDSN == "" {
		_ = redisClient.Close()
		return nil, fmt.Errorf("HITL callback runtime requires HITL_CHECKPOINT_POSTGRES_DSN, SCHEDULER_TRANSITION_POSTGRES_DSN, or DATABASE_URL")
	}
	pool, err := pgxpool.New(ctx, checkpointDSN)
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("failed to create HITL checkpoint pgx pool: %w", err)
	}
	checkpointStore, err := hitl.NewPostgresCheckpointStore(ctx, pool)
	if err != nil {
		pool.Close()
		_ = redisClient.Close()
		return nil, fmt.Errorf("failed to initialize HITL checkpoint store: %w", err)
	}

	registry := hitl.NewSimpleGraphRegistry()
	if envBool("HITL_DRILL_ENABLED", false) {
		registerHITLDrillGraph(registry, sched)
	}

	handler := hitl.NewCallbackHandler(queue, idStore, registry, []byte(secret), metrics)
	path := strings.TrimSpace(envString("HITL_CALLBACK_PATH", defaultHITLCallbackPath))
	if path == "" || path[0] != '/' {
		pool.Close()
		_ = redisClient.Close()
		return nil, fmt.Errorf("HITL_CALLBACK_PATH must start with '/'")
	}

	workerCount, err := envInt("HITL_RESUMPTION_WORKERS", 1)
	if err != nil {
		pool.Close()
		_ = redisClient.Close()
		return nil, err
	}
	if workerCount <= 0 {
		workerCount = 1
	}

	securityMgr := hitl.NewSecurityManager([]byte(secret))
	baseURL := strings.TrimSpace(envString("BASE_CALLBACK_URL", "http://localhost:8080"))

	runtime := &hitlCallbackRuntime{
		path:        path,
		handler:     newHITLCallbackHTTPHandler(handler),
		securityMgr: securityMgr,
		baseURL:     baseURL,
		checkpoints: checkpointStore,
	}
	if envBool("HITL_DRILL_ENABLED", false) {
		runtime.drillPath = strings.TrimSpace(envString("HITL_DRILL_PATH", defaultHITLDrillPath))
		if runtime.drillPath == "" || runtime.drillPath[0] != '/' {
			pool.Close()
			_ = redisClient.Close()
			return nil, fmt.Errorf("HITL_DRILL_PATH must start with '/'")
		}
		runtime.drillHandler = newHITLFailoverDrillHandler(sched, checkpointStore, securityMgr, baseURL)
	}

	worker := hitl.NewResumptionWorker(queue, checkpointStore, idStore, registry, nil, metrics)
	worker.Start(ctx, workerCount)
	runtime.close = func() error {
		worker.Stop()
		pool.Close()
		return redisClient.Close()
	}

	return runtime, nil
}

func newHITLCallbackHTTPHandler(callbackHandler *hitl.CallbackHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		requestID := strings.TrimSpace(r.URL.Query().Get("req"))
		signature := strings.TrimSpace(r.URL.Query().Get("sig"))
		if requestID == "" || signature == "" {
			http.Error(w, "missing req or sig query parameter", http.StatusBadRequest)
			return
		}

		tsRaw := strings.TrimSpace(r.URL.Query().Get("ts"))
		timestamp, err := strconv.ParseInt(tsRaw, 10, 64)
		if err != nil || timestamp <= 0 {
			http.Error(w, "invalid ts query parameter", http.StatusBadRequest)
			return
		}

		resp, err := decodeHumanResponse(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		callbackHandler.HandleHumanCallback(w, r, requestID, timestamp, signature, resp)
	})
}

func decodeHumanResponse(body io.ReadCloser) (*hitl.HumanResponse, error) {
	defer body.Close()
	limited := io.LimitReader(body, maxCallbackBodyBytes)
	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()

	var resp hitl.HumanResponse
	if err := dec.Decode(&resp); err != nil {
		if err == io.EOF {
			return &hitl.HumanResponse{}, nil
		}
		return nil, fmt.Errorf("invalid callback body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("invalid callback body: expected a single JSON object")
	}
	return &resp, nil
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	return strings.EqualFold(raw, "true")
}

func envString(name, fallback string) string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	return raw
}

func envInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing env %s=%q: %w", name, raw, err)
	}
	return val, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	val, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing env %s=%q: %w", name, raw, err)
	}
	return val, nil
}

func defaultConsumerID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "api"
	}
	return "api-" + host
}

func redisTLSConfigFromEnv() *tls.Config {
	if !envBool("HITL_REDIS_TLS_ENABLED", false) {
		return nil
	}
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

func registerHITLDrillGraph(registry *hitl.SimpleGraphRegistry, sched *scheduler.Scheduler) {
	if registry == nil || sched == nil {
		return
	}

	g := graph.NewGraphWithConfig(graph.GraphConfig{
		ID:   hitlDrillGraphID,
		Name: "ha-hitl-drill",
		Metadata: map[string]interface{}{
			"graph_version": hitlDrillGraphVersion,
		},
	})
	_ = g.AddNode(graph.NewFunctionNode("human", func(ctx context.Context, state graph.State) error { return nil }))
	_ = g.AddNode(graph.NewFunctionNode("tail", func(ctx context.Context, state graph.State) error {
		jobIDRaw, ok := state.Get("scheduler_job_id")
		if !ok {
			return fmt.Errorf("missing scheduler_job_id in drill state")
		}
		jobID, ok := jobIDRaw.(string)
		if !ok || strings.TrimSpace(jobID) == "" {
			return fmt.Errorf("invalid scheduler_job_id type %T", jobIDRaw)
		}

		selectedOption := ""
		if raw, exists := state.Get(fmt.Sprintf(hitl.StateKeyHumanPendingFmt, "human")); exists {
			payload, ok := raw.([]byte)
			if !ok {
				return fmt.Errorf("pending response payload type=%T, want []byte", raw)
			}
			var resp hitl.HumanResponse
			if err := json.Unmarshal(payload, &resp); err != nil {
				return fmt.Errorf("decode pending response: %w", err)
			}
			selectedOption = resp.SelectedOption
		}

		return sched.ResumeAwaitingHumanJob(ctx, jobID, map[string]interface{}{
			"hitl_drill_resume_ready":       true,
			"hitl_callback_selected_option": selectedOption,
		})
	}))
	_ = g.AddEdge(graph.NewDirectEdge("human", "tail"))
	_ = g.SetEntry("human")
	_ = g.AddFinish("tail")
	registry.RegisterExecutableGraph(g, hitlDrillGraphVersion)
}
