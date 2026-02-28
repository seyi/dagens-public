package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/seyi/dagens/examples/research_swarm/agents"
	"github.com/seyi/dagens/pkg/coordination"
	"github.com/seyi/dagens/pkg/observability"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

type runConfig struct {
	query         string
	barrierKey    string
	timeout       time.Duration
	metricsAddr   string
	etcdEndpoints []string
}

func main() {
	cfg := parseConfig()

	if cfg.metricsAddr != "" {
		startMetricsServer(cfg.metricsAddr)
	}

	modelProvider, closeModel, usesRealLLM, err := agents.BuildDemoModelProviderFromEnv()
	if err != nil {
		log.Fatalf("failed to initialize model provider: %v", err)
	}
	defer func() {
		if closeErr := closeModel(); closeErr != nil {
			log.Printf("model provider close error: %v", closeErr)
		}
	}()

	if usesRealLLM {
		fmt.Printf("Model provider: %s (real LLM)\n", modelProvider.Name())
	} else {
		fmt.Printf("Model provider: %s (mock fallback; set OPENAI_API_KEY or OPENROUTER_API_KEY for real LLM)\n", modelProvider.Name())
	}

	client, err := connectEtcdWithRetry(cfg.etcdEndpoints, 30*time.Second)
	if err != nil {
		log.Fatalf("failed to connect to etcd: %v", err)
	}
	defer client.Close()

	session, err := concurrency.NewSession(client, concurrency.WithTTL(10))
	if err != nil {
		log.Fatalf("failed to create etcd session: %v", err)
	}
	defer session.Close()

	agentList := []agents.ResearchAgent{
		agents.NewAcademicAgent(modelProvider),
		agents.NewGitHubAgent(modelProvider),
		agents.NewBlogAgent(modelProvider),
	}

	barrier := coordination.NewDistributedBarrier(client, session, cfg.barrierKey, len(agentList))
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	fmt.Println("Starting research swarm demo")
	fmt.Printf("Query: %s\n", cfg.query)
	fmt.Printf("Barrier key: %s\n", cfg.barrierKey)
	fmt.Printf("Participants required: %d\n", len(agentList))
	fmt.Println("Phase 1: parallel research")

	store := &agents.ResultStore{}
	var wg sync.WaitGroup
	errCh := make(chan error, len(agentList))

	for _, agent := range agentList {
		agent := agent
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()

			err := coordination.WaitForSwarm(ctx, barrier, func(runCtx context.Context) error {
				finding, runErr := agent.Run(runCtx, cfg.query)
				if runErr != nil {
					return runErr
				}
				store.Add(finding)
				fmt.Printf("[%s] waiting at barrier\n", agent.Name())
				return nil
			})
			if err != nil {
				observability.GetMetrics().RecordAgentExecution(agent.Name(), "research", "error", time.Since(start))
				errCh <- fmt.Errorf("%s failed: %w", agent.Name(), err)
				return
			}

			observability.GetMetrics().RecordAgentExecution(agent.Name(), "research", "success", time.Since(start))
			fmt.Printf("[%s] barrier released; entering synthesis\n", agent.Name())

			top := agents.SynthesizeTopTrends(store.Snapshot(), 3)
			fmt.Printf("[%s] top trends: %s\n", agent.Name(), strings.Join(top, ", "))
		}()
	}

	wg.Wait()
	close(errCh)

	var hadErr bool
	for err := range errCh {
		hadErr = true
		log.Printf("error: %v", err)
	}

	if hadErr {
		log.Fatal("demo finished with failures")
	}

	fmt.Println("Phase 3 complete: collective synthesis finished")
}

func parseConfig() runConfig {
	defaultQuery := envOrDefault("DEMO_QUERY", "What are the top 3 trends in AI agent architecture for 2026?")
	defaultBarrier := envOrDefault("BARRIER_KEY", "/barriers/research-demo")
	defaultMetricsAddr := envOrDefault("METRICS_ADDR", ":2112")
	defaultEndpoints := envOrDefault("ETCD_ENDPOINTS", "localhost:2379")

	query := flag.String("query", defaultQuery, "research query")
	barrierKey := flag.String("barrier-key", defaultBarrier, "distributed barrier key")
	timeout := flag.Duration("timeout", 60*time.Second, "overall swarm timeout")
	metricsAddr := flag.String("metrics-addr", defaultMetricsAddr, "address for metrics endpoint; empty disables")
	flag.Parse()

	return runConfig{
		query:         *query,
		barrierKey:    *barrierKey,
		timeout:       *timeout,
		metricsAddr:   strings.TrimSpace(*metricsAddr),
		etcdEndpoints: parseCSVEndpoints(defaultEndpoints),
	}
}

func connectEtcdWithRetry(endpoints []string, timeout time.Duration) (*clientv3.Client, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		cli, err := clientv3.New(clientv3.Config{
			Endpoints:   endpoints,
			DialTimeout: 3 * time.Second,
		})
		if err != nil {
			lastErr = err
			time.Sleep(1 * time.Second)
			continue
		}

		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err = cli.Get(pingCtx, "health-check")
		cancel()
		if err == nil {
			return cli, nil
		}

		lastErr = err
		_ = cli.Close()
		time.Sleep(1 * time.Second)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("unknown connection failure")
	}
	return nil, lastErr
}

func startMetricsServer(addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", observability.Handler())

	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("metrics server stopped: %v", err)
		}
	}()
	fmt.Printf("Metrics endpoint: http://localhost%s/metrics\n", addr)
}

func parseCSVEndpoints(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return []string{"localhost:2379"}
	}
	return out
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
