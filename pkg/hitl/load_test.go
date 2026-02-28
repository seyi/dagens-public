package hitl

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// H003 Phase 2 - Production-Scale Load Testing
// ===========================================================================

// LoadTestConfig configures the load test parameters
type LoadTestConfig struct {
	TargetOpsPerSec int           // Target operations per second
	Duration        time.Duration // How long to run the test
	NumWorkers      int           // Number of concurrent workers
	WarmupDuration  time.Duration // Warmup period before measuring
}

// DefaultLoadTestConfig returns conservative defaults for CI
func DefaultLoadTestConfig() LoadTestConfig {
	return LoadTestConfig{
		TargetOpsPerSec: 100,          // 100 ops/sec for CI
		Duration:        10 * time.Second,
		NumWorkers:      10,
		WarmupDuration:  2 * time.Second,
	}
}

// ProductionLoadTestConfig returns production-scale settings
func ProductionLoadTestConfig() LoadTestConfig {
	return LoadTestConfig{
		TargetOpsPerSec: 1000,         // 1000 ops/sec
		Duration:        60 * time.Second, // 1 minute (use 10 min for full test)
		NumWorkers:      50,
		WarmupDuration:  5 * time.Second,
	}
}

// LoadTestMetrics captures performance metrics
type LoadTestMetrics struct {
	TotalOps        int64
	SuccessfulOps   int64
	FailedOps       int64
	Latencies       []time.Duration
	StartTime       time.Time
	EndTime         time.Time
	mu              sync.Mutex
}

func NewLoadTestMetrics() *LoadTestMetrics {
	return &LoadTestMetrics{
		Latencies: make([]time.Duration, 0, 100000),
	}
}

func (m *LoadTestMetrics) RecordSuccess(latency time.Duration) {
	atomic.AddInt64(&m.TotalOps, 1)
	atomic.AddInt64(&m.SuccessfulOps, 1)
	m.mu.Lock()
	m.Latencies = append(m.Latencies, latency)
	m.mu.Unlock()
}

func (m *LoadTestMetrics) RecordFailure() {
	atomic.AddInt64(&m.TotalOps, 1)
	atomic.AddInt64(&m.FailedOps, 1)
}

func (m *LoadTestMetrics) Percentile(p float64) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.Latencies) == 0 {
		return 0
	}

	sorted := make([]time.Duration, len(m.Latencies))
	copy(sorted, m.Latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func (m *LoadTestMetrics) OpsPerSecond() float64 {
	duration := m.EndTime.Sub(m.StartTime).Seconds()
	if duration == 0 {
		return 0
	}
	return float64(m.TotalOps) / duration
}

func (m *LoadTestMetrics) ErrorRate() float64 {
	total := atomic.LoadInt64(&m.TotalOps)
	if total == 0 {
		return 0
	}
	return float64(atomic.LoadInt64(&m.FailedOps)) / float64(total)
}

func (m *LoadTestMetrics) Report(t *testing.T) {
	t.Logf("=== Load Test Results ===")
	t.Logf("Duration:     %v", m.EndTime.Sub(m.StartTime))
	t.Logf("Total Ops:    %d", m.TotalOps)
	t.Logf("Successful:   %d", m.SuccessfulOps)
	t.Logf("Failed:       %d", m.FailedOps)
	t.Logf("Ops/sec:      %.2f", m.OpsPerSecond())
	t.Logf("Error Rate:   %.4f%%", m.ErrorRate()*100)
	t.Logf("Latency p50:  %v", m.Percentile(0.50))
	t.Logf("Latency p95:  %v", m.Percentile(0.95))
	t.Logf("Latency p99:  %v", m.Percentile(0.99))
	t.Logf("Latency max:  %v", m.Percentile(1.0))
}

// TestLoadTest_CreateOperations tests sustained Create operations
func TestLoadTest_CreateOperations(t *testing.T) {
	if os.Getenv("LOAD_TEST") != "true" {
		t.Skip("Skipping load test. Set LOAD_TEST=true to enable.")
	}

	config := DefaultLoadTestConfig()
	if os.Getenv("LOAD_TEST_PRODUCTION") == "true" {
		config = ProductionLoadTestConfig()
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)
	metrics := NewLoadTestMetrics()

	// Rate limiter: tokens per worker
	opsPerWorkerPerSec := config.TargetOpsPerSec / config.NumWorkers
	tickInterval := time.Second / time.Duration(opsPerWorkerPerSec)

	ctx, cancel := context.WithTimeout(context.Background(), config.Duration+config.WarmupDuration+10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var opCounter int64
	startCh := make(chan struct{})

	// Spawn workers
	for w := 0; w < config.NumWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startCh // Wait for start signal

			ticker := time.NewTicker(tickInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					opID := atomic.AddInt64(&opCounter, 1)
					requestID := fmt.Sprintf("load-create-%d-%d", workerID, opID)

					cp := &ExecutionCheckpoint{
						RequestID:    requestID,
						GraphID:      "load-test-graph",
						GraphVersion: "v1",
						NodeID:       fmt.Sprintf("node-%d", workerID),
						StateData:    []byte(`{"load":"test"}`),
						CreatedAt:    time.Now(),
					}

					start := time.Now()
					err := store.Create(cp)
					latency := time.Since(start)

					if err != nil {
						metrics.RecordFailure()
					} else {
						metrics.RecordSuccess(latency)
					}
				}
			}
		}(w)
	}

	// Warmup
	t.Logf("Warming up for %v...", config.WarmupDuration)
	metrics.StartTime = time.Now()
	close(startCh)
	time.Sleep(config.WarmupDuration)

	// Reset metrics after warmup
	warmupMetrics := metrics
	metrics = NewLoadTestMetrics()
	metrics.StartTime = time.Now()

	t.Logf("Running load test for %v at target %d ops/sec...", config.Duration, config.TargetOpsPerSec)
	time.Sleep(config.Duration)
	metrics.EndTime = time.Now()
	cancel()

	wg.Wait()

	// Report
	t.Logf("Warmup ops: %d", warmupMetrics.TotalOps)
	metrics.Report(t)

	// Assertions
	assert.GreaterOrEqual(t, metrics.OpsPerSecond(), float64(config.TargetOpsPerSec)*0.8,
		"Should achieve at least 80%% of target throughput")
	assert.LessOrEqual(t, metrics.ErrorRate(), 0.01,
		"Error rate should be less than 1%%")
	assert.LessOrEqual(t, metrics.Percentile(0.99), 100*time.Millisecond,
		"p99 latency should be under 100ms")
}

// TestLoadTest_RecordFailureOperations tests sustained RecordFailure operations
func TestLoadTest_RecordFailureOperations(t *testing.T) {
	if os.Getenv("LOAD_TEST") != "true" {
		t.Skip("Skipping load test. Set LOAD_TEST=true to enable.")
	}

	config := DefaultLoadTestConfig()
	if os.Getenv("LOAD_TEST_PRODUCTION") == "true" {
		config = ProductionLoadTestConfig()
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	// Pre-create checkpoints for the test
	numCheckpoints := config.NumWorkers * 10
	t.Logf("Pre-creating %d checkpoints...", numCheckpoints)
	checkpointIDs := make([]string, numCheckpoints)
	for i := 0; i < numCheckpoints; i++ {
		requestID := fmt.Sprintf("load-failure-%d", i)
		checkpointIDs[i] = requestID
		cp := &ExecutionCheckpoint{
			RequestID:    requestID,
			GraphID:      "load-test-graph",
			GraphVersion: "v1",
			NodeID:       "node-failure",
			StateData:    []byte(`{"load":"test"}`),
			CreatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(cp))
	}

	metrics := NewLoadTestMetrics()
	opsPerWorkerPerSec := config.TargetOpsPerSec / config.NumWorkers
	tickInterval := time.Second / time.Duration(opsPerWorkerPerSec)

	ctx, cancel := context.WithTimeout(context.Background(), config.Duration+config.WarmupDuration+10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var opCounter int64
	startCh := make(chan struct{})

	for w := 0; w < config.NumWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startCh

			ticker := time.NewTicker(tickInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					opID := atomic.AddInt64(&opCounter, 1)
					// Round-robin across checkpoints
					checkpointIdx := int(opID) % numCheckpoints
					requestID := checkpointIDs[checkpointIdx]

					start := time.Now()
					_, err := store.RecordFailure(requestID, fmt.Errorf("load test error %d", opID))
					latency := time.Since(start)

					if err != nil {
						metrics.RecordFailure()
					} else {
						metrics.RecordSuccess(latency)
					}
				}
			}
		}(w)
	}

	t.Logf("Warming up for %v...", config.WarmupDuration)
	metrics.StartTime = time.Now()
	close(startCh)
	time.Sleep(config.WarmupDuration)

	// Reset after warmup
	metrics = NewLoadTestMetrics()
	metrics.StartTime = time.Now()

	t.Logf("Running load test for %v at target %d ops/sec...", config.Duration, config.TargetOpsPerSec)
	time.Sleep(config.Duration)
	metrics.EndTime = time.Now()
	cancel()

	wg.Wait()
	metrics.Report(t)

	// Assertions
	assert.GreaterOrEqual(t, metrics.OpsPerSecond(), float64(config.TargetOpsPerSec)*0.8,
		"Should achieve at least 80%% of target throughput")
	assert.LessOrEqual(t, metrics.ErrorRate(), 0.01,
		"Error rate should be less than 1%%")
	assert.LessOrEqual(t, metrics.Percentile(0.99), 100*time.Millisecond,
		"p99 latency should be under 100ms")
}

// TestLoadTest_MixedOperations tests a realistic mix of operations
func TestLoadTest_MixedOperations(t *testing.T) {
	if os.Getenv("LOAD_TEST") != "true" {
		t.Skip("Skipping load test. Set LOAD_TEST=true to enable.")
	}

	config := DefaultLoadTestConfig()
	if os.Getenv("LOAD_TEST_PRODUCTION") == "true" {
		config = ProductionLoadTestConfig()
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	// Pre-create some checkpoints
	numInitialCheckpoints := 100
	t.Logf("Pre-creating %d checkpoints...", numInitialCheckpoints)
	for i := 0; i < numInitialCheckpoints; i++ {
		cp := &ExecutionCheckpoint{
			RequestID:    fmt.Sprintf("mixed-init-%d", i),
			GraphID:      "load-test-graph",
			GraphVersion: "v1",
			NodeID:       "node-mixed",
			StateData:    []byte(`{"load":"test"}`),
			CreatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(cp))
	}

	createMetrics := NewLoadTestMetrics()
	readMetrics := NewLoadTestMetrics()
	updateMetrics := NewLoadTestMetrics()

	opsPerWorkerPerSec := config.TargetOpsPerSec / config.NumWorkers
	tickInterval := time.Second / time.Duration(opsPerWorkerPerSec)

	ctx, cancel := context.WithTimeout(context.Background(), config.Duration+config.WarmupDuration+10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var createCounter, readCounter, updateCounter int64
	startCh := make(chan struct{})

	for w := 0; w < config.NumWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startCh

			ticker := time.NewTicker(tickInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// Operation mix: 40% create, 40% read, 20% update
					opType := workerID % 10
					var start time.Time
					var err error

					switch {
					case opType < 4: // 40% creates
						opID := atomic.AddInt64(&createCounter, 1)
						requestID := fmt.Sprintf("mixed-create-%d-%d", workerID, opID)
						cp := &ExecutionCheckpoint{
							RequestID:    requestID,
							GraphID:      "load-test-graph",
							GraphVersion: "v1",
							NodeID:       fmt.Sprintf("node-%d", workerID),
							StateData:    []byte(`{"mixed":"create"}`),
							CreatedAt:    time.Now(),
						}
						start = time.Now()
						err = store.Create(cp)
						if err == nil {
							createMetrics.RecordSuccess(time.Since(start))
						} else {
							createMetrics.RecordFailure()
						}

					case opType < 8: // 40% reads
						opID := atomic.AddInt64(&readCounter, 1)
						checkpointIdx := int(opID) % numInitialCheckpoints
						requestID := fmt.Sprintf("mixed-init-%d", checkpointIdx)
						start = time.Now()
						_, err = store.GetByRequestID(requestID)
						if err == nil {
							readMetrics.RecordSuccess(time.Since(start))
						} else {
							readMetrics.RecordFailure()
						}

					default: // 20% updates (RecordFailure)
						opID := atomic.AddInt64(&updateCounter, 1)
						checkpointIdx := int(opID) % numInitialCheckpoints
						requestID := fmt.Sprintf("mixed-init-%d", checkpointIdx)
						start = time.Now()
						_, err = store.RecordFailure(requestID, fmt.Errorf("mixed error %d", opID))
						if err == nil {
							updateMetrics.RecordSuccess(time.Since(start))
						} else {
							updateMetrics.RecordFailure()
						}
					}
				}
			}
		}(w)
	}

	t.Logf("Warming up for %v...", config.WarmupDuration)
	startTime := time.Now()
	close(startCh)
	time.Sleep(config.WarmupDuration)

	// Reset after warmup
	createMetrics = NewLoadTestMetrics()
	readMetrics = NewLoadTestMetrics()
	updateMetrics = NewLoadTestMetrics()
	createMetrics.StartTime = time.Now()
	readMetrics.StartTime = time.Now()
	updateMetrics.StartTime = time.Now()

	t.Logf("Running mixed load test for %v at target %d ops/sec...", config.Duration, config.TargetOpsPerSec)
	time.Sleep(config.Duration)
	endTime := time.Now()
	createMetrics.EndTime = endTime
	readMetrics.EndTime = endTime
	updateMetrics.EndTime = endTime
	cancel()

	wg.Wait()

	// Combined report
	totalOps := createMetrics.TotalOps + readMetrics.TotalOps + updateMetrics.TotalOps
	totalSuccess := createMetrics.SuccessfulOps + readMetrics.SuccessfulOps + updateMetrics.SuccessfulOps
	totalFailed := createMetrics.FailedOps + readMetrics.FailedOps + updateMetrics.FailedOps
	duration := endTime.Sub(startTime).Seconds()

	t.Logf("=== Mixed Load Test Results ===")
	t.Logf("Duration:     %v", endTime.Sub(startTime))
	t.Logf("Total Ops:    %d (Create: %d, Read: %d, Update: %d)",
		totalOps, createMetrics.TotalOps, readMetrics.TotalOps, updateMetrics.TotalOps)
	t.Logf("Successful:   %d", totalSuccess)
	t.Logf("Failed:       %d", totalFailed)
	t.Logf("Ops/sec:      %.2f", float64(totalOps)/duration)
	t.Logf("Error Rate:   %.4f%%", float64(totalFailed)/float64(totalOps)*100)

	t.Logf("\n--- Create Operations ---")
	createMetrics.Report(t)
	t.Logf("\n--- Read Operations ---")
	readMetrics.Report(t)
	t.Logf("\n--- Update Operations ---")
	updateMetrics.Report(t)

	// Assertions
	combinedOpsPerSec := float64(totalOps) / duration
	assert.GreaterOrEqual(t, combinedOpsPerSec, float64(config.TargetOpsPerSec)*0.8,
		"Should achieve at least 80%% of target throughput")

	errorRate := float64(totalFailed) / float64(totalOps)
	assert.LessOrEqual(t, errorRate, 0.01,
		"Error rate should be less than 1%%")
}

// TestLoadTest_SerializableRetryUnderContention tests serializable retry under high contention
func TestLoadTest_SerializableRetryUnderContention(t *testing.T) {
	if os.Getenv("LOAD_TEST") != "true" {
		t.Skip("Skipping load test. Set LOAD_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	// Pre-create checkpoints for all workers to move
	numCheckpoints := 100
	t.Logf("Pre-creating %d checkpoints for contention test...", numCheckpoints)
	for i := 0; i < numCheckpoints; i++ {
		cp := &ExecutionCheckpoint{
			RequestID:    fmt.Sprintf("serializable-contention-%d", i),
			GraphID:      "contention-graph",
			GraphVersion: "v1",
			NodeID:       "node-contention",
			StateData:    []byte(`{"contention":"test"}`),
			CreatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(cp))
	}

	numWorkers := 20
	metrics := NewLoadTestMetrics()

	var wg sync.WaitGroup
	var successCount, notFoundCount, failCount int64

	t.Logf("Running serializable contention test with %d workers racing on %d checkpoints...", numWorkers, numCheckpoints)
	metrics.StartTime = time.Now()

	// All workers race to move the same set of checkpoints
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for i := 0; i < numCheckpoints; i++ {
				start := time.Now()
				err := store.MoveToCheckpointDLQSerializable(
					fmt.Sprintf("serializable-contention-%d", i),
					"contention test error",
				)
				latency := time.Since(start)

				if err == nil {
					atomic.AddInt64(&successCount, 1)
					metrics.RecordSuccess(latency)
				} else if err == ErrCheckpointNotFound {
					// Expected - another worker already moved it
					atomic.AddInt64(&notFoundCount, 1)
					metrics.RecordSuccess(latency)
				} else {
					atomic.AddInt64(&failCount, 1)
					metrics.RecordFailure()
					t.Logf("Worker %d checkpoint %d failed: %v", workerID, i, err)
				}
			}
		}(w)
	}

	wg.Wait()
	metrics.EndTime = time.Now()

	t.Logf("=== Serializable Contention Results ===")
	t.Logf("Duration:     %v", metrics.EndTime.Sub(metrics.StartTime))
	t.Logf("Successes:    %d (moved to DLQ)", successCount)
	t.Logf("NotFound:     %d (already moved by another worker)", notFoundCount)
	t.Logf("Failures:     %d", failCount)

	// Each checkpoint should be moved exactly once
	assert.Equal(t, int64(numCheckpoints), successCount,
		"Exactly %d checkpoints should be moved to DLQ", numCheckpoints)

	// Total attempts = numWorkers * numCheckpoints
	// Successes + NotFound should equal total attempts
	totalAttempts := int64(numWorkers * numCheckpoints)
	assert.Equal(t, totalAttempts, successCount+notFoundCount+failCount,
		"All attempts should be accounted for")

	// Failure rate should be 0% with proper serializable isolation
	assert.Equal(t, int64(0), failCount,
		"No failures should occur with serializable isolation")
}
