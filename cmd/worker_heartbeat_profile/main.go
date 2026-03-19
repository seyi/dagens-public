package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/api"
	"github.com/seyi/dagens/pkg/scheduler"
)

type profileConfig struct {
	RequestCount      int
	Concurrency       int
	NodeCount         int
	MaxConcurrency    int
	ChurnStride       int
	Mode              string
	SlowResponseDelay time.Duration
	RetryAttempts     int
	RetryBackoff      time.Duration
	TargetURL         string
	EvidenceRoot      string
	Timeout           time.Duration
}

type profileResult struct {
	TimestampUTC string         `json:"timestamp_utc"`
	Config       profileConfig  `json:"config"`
	Requests     requestSummary `json:"requests"`
	Latencies    latencySummary `json:"latencies"`
	Timings      timingSummary  `json:"timings"`
	StatusCodes  map[string]int `json:"status_codes"`
}

type requestSummary struct {
	Attempted        int     `json:"attempted"`
	Succeeded        int     `json:"succeeded"`
	Failed           int     `json:"failed"`
	ThroughputPerSec float64 `json:"throughput_per_sec"`
	TotalAttempts    int     `json:"total_attempts"`
	RetriedRequests  int     `json:"retried_requests"`
}

type latencySummary struct {
	Min float64 `json:"min"`
	Avg float64 `json:"avg"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

type timingSummary struct {
	TotalDurationMS float64 `json:"total_duration_ms"`
}

func main() {
	cfg := profileConfig{}
	flag.IntVar(&cfg.RequestCount, "requests", 5000, "number of worker heartbeat requests to send")
	flag.IntVar(&cfg.Concurrency, "concurrency", 64, "number of concurrent request workers")
	flag.IntVar(&cfg.NodeCount, "nodes", 1000, "number of distinct worker node ids to rotate through")
	flag.IntVar(&cfg.MaxConcurrency, "max-concurrency", 8, "reported worker max_concurrency value")
	flag.IntVar(&cfg.ChurnStride, "churn-stride", 0, "change worker identity generation every N logical requests; 0 disables churn")
	flag.StringVar(&cfg.Mode, "mode", "clean", "profile mode: clean|auth_fail|slow_response")
	flag.DurationVar(&cfg.SlowResponseDelay, "slow-response-delay", 5*time.Millisecond, "artificial per-request delay for slow_response mode")
	flag.IntVar(&cfg.RetryAttempts, "retry-attempts", 0, "additional attempts after a non-204 or transport failure")
	flag.DurationVar(&cfg.RetryBackoff, "retry-backoff", 10*time.Millisecond, "delay between retry attempts")
	flag.StringVar(&cfg.TargetURL, "target-url", "", "optional full worker heartbeat endpoint URL; when set, profile a real server instead of a local test server")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 2*time.Minute, "overall profile timeout")
	flag.Parse()

	if cfg.RequestCount <= 0 || cfg.Concurrency <= 0 || cfg.NodeCount <= 0 || cfg.MaxConcurrency <= 0 {
		exitf("requests, concurrency, nodes, max-concurrency must all be > 0")
	}
	if cfg.Mode != "clean" && cfg.Mode != "auth_fail" && cfg.Mode != "slow_response" {
		exitf("unsupported mode %q", cfg.Mode)
	}
	if cfg.Mode == "slow_response" && cfg.SlowResponseDelay < 0 {
		exitf("slow-response-delay must be >= 0")
	}
	if cfg.ChurnStride < 0 {
		exitf("churn-stride must be >= 0")
	}
	if cfg.RetryAttempts < 0 {
		exitf("retry-attempts must be >= 0")
	}
	if cfg.RetryBackoff < 0 {
		exitf("retry-backoff must be >= 0")
	}
	if err := os.MkdirAll(cfg.EvidenceRoot, 0o755); err != nil {
		exitf("create evidence root: %v", err)
	}

	switch cfg.Mode {
	case "auth_fail":
		_ = os.Setenv("DEV_MODE", "false")
		_ = os.Setenv("WORKER_HEARTBEAT_TOKEN", "expected-heartbeat-token")
	default:
		_ = os.Setenv("DEV_MODE", "true")
		_ = os.Unsetenv("WORKER_HEARTBEAT_TOKEN")
	}

	s := scheduler.NewSchedulerWithConfig(nil, nil, scheduler.DefaultSchedulerConfig())
	srv := api.NewServer(s)
	targetURL := cfg.TargetURL
	if targetURL == "" {
		handler := srv.Routes()
		if cfg.Mode == "slow_response" && cfg.SlowResponseDelay > 0 {
			handler = delayedHandler(handler, cfg.SlowResponseDelay)
		}
		testServer := httptest.NewServer(handler)
		defer testServer.Close()
		targetURL = testServer.URL + "/v1/internal/worker_capacity"
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        cfg.Concurrency * 2,
			MaxIdleConnsPerHost: cfg.Concurrency * 2,
			MaxConnsPerHost:     cfg.Concurrency * 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	start := time.Now()
	latencies, successes, failures, totalAttempts, retriedRequests, statusCodes := runHeartbeatProfile(ctx, client, targetURL, cfg)
	total := time.Since(start)

	result := profileResult{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Config:       cfg,
		Requests: requestSummary{
			Attempted:        cfg.RequestCount,
			Succeeded:        successes,
			Failed:           failures,
			ThroughputPerSec: float64(successes) / total.Seconds(),
			TotalAttempts:    totalAttempts,
			RetriedRequests:  retriedRequests,
		},
		Latencies:   buildLatencySummary(latencies),
		Timings:     timingSummary{TotalDurationMS: durationMillis(total)},
		StatusCodes: statusCodes,
	}

	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: worker heartbeat profile completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("attempted=%d succeeded=%d failed=%d total_attempts=%d retried_requests=%d throughput_per_sec=%.2f\n",
		result.Requests.Attempted,
		result.Requests.Succeeded,
		result.Requests.Failed,
		result.Requests.TotalAttempts,
		result.Requests.RetriedRequests,
		result.Requests.ThroughputPerSec,
	)
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-worker-heartbeat-profile-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func durationMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func runHeartbeatProfile(ctx context.Context, client *http.Client, url string, cfg profileConfig) ([]time.Duration, int, int, int, int, map[string]int) {
	requestCh := make(chan int)
	latencies := make([]time.Duration, cfg.RequestCount)
	var successes int64
	var failures int64
	var totalAttempts int64
	var retriedRequests int64
	statusCounts := make(map[string]int)
	var statusMu sync.Mutex

	var wg sync.WaitGroup
	for worker := 0; worker < cfg.Concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for reqIndex := range requestCh {
				select {
				case <-ctx.Done():
					return
				default:
				}

				payload, err := json.Marshal(api.WorkerCapacityUpdateRequest{
					NodeID:          heartbeatNodeID(reqIndex, cfg.NodeCount, cfg.ChurnStride),
					InFlight:        reqIndex % cfg.MaxConcurrency,
					MaxConcurrency:  cfg.MaxConcurrency,
					ReportTimestamp: time.Now().UTC(),
				})
				if err != nil {
					atomic.AddInt64(&failures, 1)
					recordStatus(statusCounts, &statusMu, "marshal_error")
					continue
				}

				requestBegin := time.Now()
				var retried bool
				for attempt := 0; attempt <= cfg.RetryAttempts; attempt++ {
					if attempt > 0 {
						retried = true
						if cfg.RetryBackoff > 0 {
							timer := time.NewTimer(cfg.RetryBackoff)
							select {
							case <-ctx.Done():
								timer.Stop()
								return
							case <-timer.C:
							}
						}
					}
					atomic.AddInt64(&totalAttempts, 1)

					req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
					if err != nil {
						recordStatus(statusCounts, &statusMu, "request_error")
						if attempt == cfg.RetryAttempts {
							latencies[reqIndex] = time.Since(requestBegin)
							atomic.AddInt64(&failures, 1)
						}
						continue
					}
					req.Header.Set("Content-Type", "application/json")
					if cfg.Mode == "auth_fail" {
						req.Header.Set("X-Dagens-Worker-Token", "wrong-heartbeat-token")
					}

					resp, err := client.Do(req)
					if err != nil {
						recordStatus(statusCounts, &statusMu, "transport_error")
						if attempt == cfg.RetryAttempts {
							latencies[reqIndex] = time.Since(requestBegin)
							atomic.AddInt64(&failures, 1)
						}
						continue
					}
					_ = resp.Body.Close()
					code := fmt.Sprintf("%d", resp.StatusCode)
					recordStatus(statusCounts, &statusMu, code)
					if resp.StatusCode == http.StatusNoContent {
						latencies[reqIndex] = time.Since(requestBegin)
						if retried {
							atomic.AddInt64(&retriedRequests, 1)
						}
						atomic.AddInt64(&successes, 1)
						break
					}
					if attempt == cfg.RetryAttempts {
						latencies[reqIndex] = time.Since(requestBegin)
						if retried {
							atomic.AddInt64(&retriedRequests, 1)
						}
						atomic.AddInt64(&failures, 1)
					}
				}
			}
		}()
	}

	for i := 0; i < cfg.RequestCount; i++ {
		requestCh <- i
	}
	close(requestCh)
	wg.Wait()

	return latencies, int(successes), int(failures), int(totalAttempts), int(retriedRequests), statusCounts
}

func heartbeatNodeID(reqIndex, nodeCount, churnStride int) string {
	base := fmt.Sprintf("worker-%04d", reqIndex%nodeCount)
	if churnStride <= 0 {
		return base
	}
	return fmt.Sprintf("%s-r%04d", base, reqIndex/churnStride)
}

func recordStatus(statusCounts map[string]int, mu *sync.Mutex, key string) {
	mu.Lock()
	defer mu.Unlock()
	statusCounts[key]++
}

func buildLatencySummary(latencies []time.Duration) latencySummary {
	values := make([]float64, 0, len(latencies))
	for _, latency := range latencies {
		if latency <= 0 {
			continue
		}
		values = append(values, durationMillis(latency))
	}
	if len(values) == 0 {
		return latencySummary{}
	}
	sort.Float64s(values)
	total := 0.0
	for _, value := range values {
		total += value
	}
	return latencySummary{
		Min: values[0],
		Avg: total / float64(len(values)),
		P50: percentile(values, 0.50),
		P95: percentile(values, 0.95),
		P99: percentile(values, 0.99),
		Max: values[len(values)-1],
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	index := int(float64(len(sorted)-1) * p)
	return sorted[index]
}

func delayedHandler(next http.Handler, delay time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		next.ServeHTTP(w, r)
	})
}

func writeResults(evidenceRoot string, result profileResult) error {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(evidenceRoot, "result.json"), append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	summary := fmt.Sprintf(
		"status=PASS\n"+
			"evidence_root=%s\n"+
			"attempted=%d\n"+
			"succeeded=%d\n"+
			"failed=%d\n"+
			"throughput_per_sec=%.2f\n"+
			"latency_p95_ms=%.2f\n"+
			"latency_p99_ms=%.2f\n",
		evidenceRoot,
		result.Requests.Attempted,
		result.Requests.Succeeded,
		result.Requests.Failed,
		result.Requests.ThroughputPerSec,
		result.Latencies.P95,
		result.Latencies.P99,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}
