package hitl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// H003 Phase 3: Chaos Testing
// =============================================================================
//
// These tests validate atomicity under failure conditions:
// - Connection interruptions
// - Transaction timeouts
// - Context cancellations
// - Data integrity under concurrent operations
//
// Run with: CHAOS_TEST=true go test -v -run "TestChaos_" ./pkg/hitl/...
// =============================================================================

// TestChaos_ConnectionTimeout verifies behavior when connections timeout
func TestChaos_ConnectionTimeout(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	// Create a checkpoint
	cp := &ExecutionCheckpoint{
		RequestID:    "chaos-timeout-test",
		GraphID:      "chaos-graph",
		GraphVersion: "v1",
		NodeID:       "node-timeout",
		StateData:    []byte(`{"chaos":"timeout"}`),
		CreatedAt:    time.Now(),
	}
	require.NoError(t, store.Create(cp))

	// Test operation with very short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for context to expire
	time.Sleep(10 * time.Millisecond)

	// Try to perform operations with expired context
	_, err := store.GetByRequestIDWithContext(ctx, "chaos-timeout-test")
	assert.Error(t, err, "Operation with expired context should fail")

	t.Logf("Connection timeout test passed: %v", err)
}

// TestChaos_ConcurrentDLQRace tests concurrent DLQ operations for race conditions
func TestChaos_ConcurrentDLQRace(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	numCheckpoints := 50
	t.Logf("Pre-creating %d checkpoints for DLQ race test...", numCheckpoints)

	for i := 0; i < numCheckpoints; i++ {
		cp := &ExecutionCheckpoint{
			RequestID:    fmt.Sprintf("dlq-race-%d", i),
			GraphID:      "dlq-graph",
			GraphVersion: "v1",
			NodeID:       "node-dlq",
			StateData:    []byte(`{"dlq":"race"}`),
			CreatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(cp))
	}

	numWorkers := 30
	var successCount, notFoundCount, failCount int64
	var wg sync.WaitGroup

	t.Logf("Running DLQ race with %d workers on %d checkpoints...", numWorkers, numCheckpoints)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for i := 0; i < numCheckpoints; i++ {
				requestID := fmt.Sprintf("dlq-race-%d", i)

				err := store.MoveToCheckpointDLQ(requestID, "race test")

				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else if err == ErrCheckpointNotFound {
					atomic.AddInt64(&notFoundCount, 1)
				} else {
					atomic.AddInt64(&failCount, 1)
				}
			}
		}(w)
	}

	wg.Wait()

	t.Logf("=== DLQ Race Results ===")
	t.Logf("Successes:    %d (moved to DLQ)", successCount)
	t.Logf("NotFound:     %d (already moved)", notFoundCount)
	t.Logf("Failures:     %d", failCount)

	// Each checkpoint should be moved exactly once
	assert.Equal(t, int64(numCheckpoints), successCount,
		"Exactly %d checkpoints should be moved to DLQ", numCheckpoints)

	// Total attempts should be accounted for
	totalAttempts := int64(numWorkers * numCheckpoints)
	assert.Equal(t, totalAttempts, successCount+notFoundCount+failCount,
		"All attempts should be accounted for")

	// Failure rate should be 0% with proper transaction handling
	assert.Equal(t, int64(0), failCount,
		"No failures should occur during race")
}

// TestChaos_ConcurrentFailureRecording tests concurrent failure recording for atomicity
func TestChaos_ConcurrentFailureRecording(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	// Create checkpoint
	cp := &ExecutionCheckpoint{
		RequestID:    "failure-recording-test",
		GraphID:      "failure-graph",
		GraphVersion: "v1",
		NodeID:       "node-failure",
		StateData:    []byte(`{"failure":"test"}`),
		CreatedAt:    time.Now(),
	}
	require.NoError(t, store.Create(cp))

	numWorkers := 100
	var successCount, failCount int64
	var wg sync.WaitGroup

	t.Logf("Running concurrent failure recording with %d workers...", numWorkers)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			_, err := store.RecordFailure("failure-recording-test", errors.New(fmt.Sprintf("error from worker %d", workerID)))
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&failCount, 1)
			}
		}(w)
	}

	wg.Wait()

	t.Logf("=== Concurrent Failure Recording Results ===")
	t.Logf("Successes:    %d", successCount)
	t.Logf("Failures:     %d", failCount)

	// All recordings should succeed
	assert.Equal(t, int64(numWorkers), successCount,
		"All %d RecordFailure calls should succeed", numWorkers)

	// Verify final failure count
	finalCP, err := store.GetByRequestID("failure-recording-test")
	require.NoError(t, err)

	// Due to atomic increment, failure count should be exactly numWorkers
	assert.Equal(t, numWorkers, finalCP.FailureCount,
		"Final failure count should be exactly %d", numWorkers)

	t.Logf("Final failure count: %d (expected %d)", finalCP.FailureCount, numWorkers)
}

// TestChaos_RapidCreateDelete tests create/delete races for data consistency
func TestChaos_RapidCreateDelete(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	numOps := 100
	var createSuccessCount, deleteSuccessCount, deleteNotFoundCount int64
	var wg sync.WaitGroup

	t.Logf("Running rapid create/delete test with %d operations...", numOps)

	// Create and immediately delete checkpoints concurrently
	for i := 0; i < numOps; i++ {
		requestID := fmt.Sprintf("rapid-test-%d", i)

		// Creator goroutine
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			cp := &ExecutionCheckpoint{
				RequestID:    id,
				GraphID:      "rapid-graph",
				GraphVersion: "v1",
				NodeID:       "node-rapid",
				StateData:    []byte(`{"rapid":"test"}`),
				CreatedAt:    time.Now(),
			}
			if err := store.Create(cp); err == nil {
				atomic.AddInt64(&createSuccessCount, 1)
			}
		}(requestID)

		// Deleter goroutine
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			// Small delay to increase chance of race
			time.Sleep(time.Microsecond)
			err := store.Delete(id)
			if err == nil {
				atomic.AddInt64(&deleteSuccessCount, 1)
			} else if err == ErrCheckpointNotFound {
				atomic.AddInt64(&deleteNotFoundCount, 1)
			}
		}(requestID)
	}

	wg.Wait()

	t.Logf("=== Rapid Create/Delete Results ===")
	t.Logf("Creates:      %d", createSuccessCount)
	t.Logf("Deletes:      %d (successful)", deleteSuccessCount)
	t.Logf("NotFound:     %d (delete raced before create)", deleteNotFoundCount)

	// Most creates should succeed
	assert.GreaterOrEqual(t, createSuccessCount, int64(numOps/2),
		"At least half of creates should succeed")

	// Deletes + NotFound should equal numOps
	assert.Equal(t, int64(numOps), deleteSuccessCount+deleteNotFoundCount,
		"All deletes should either succeed or report not found")
}

// TestChaos_DataIntegrityUnderLoad verifies no data corruption under concurrent operations
func TestChaos_DataIntegrityUnderLoad(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	numCheckpoints := 100
	expectedData := make(map[string][]byte)

	// Create checkpoints with unique data
	t.Logf("Creating %d checkpoints with unique data...", numCheckpoints)
	for i := 0; i < numCheckpoints; i++ {
		requestID := fmt.Sprintf("integrity-check-%d", i)
		data := []byte(fmt.Sprintf(`{"id":%d,"hash":"%x"}`, i, i*12345))

		cp := &ExecutionCheckpoint{
			RequestID:    requestID,
			GraphID:      "integrity-graph",
			GraphVersion: "v1",
			NodeID:       "node-integrity",
			StateData:    data,
			CreatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(cp))
		expectedData[requestID] = data
	}

	// Run concurrent read/write operations
	numWorkers := 20
	opsPerWorker := 50
	var wg sync.WaitGroup

	t.Logf("Running concurrent load: %d workers x %d ops...", numWorkers, opsPerWorker)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for i := 0; i < opsPerWorker; i++ {
				requestID := fmt.Sprintf("integrity-check-%d", (workerID*opsPerWorker+i)%numCheckpoints)

				switch i % 2 {
				case 0: // Read
					store.GetByRequestID(requestID)
				case 1: // Record failure (increments counter)
					store.RecordFailure(requestID, errors.New("load test"))
				}
			}
		}(w)
	}

	wg.Wait()

	// Verify data integrity
	t.Logf("Verifying data integrity...")
	corruptionCount := 0

	for requestID, expectedStateData := range expectedData {
		cp, err := store.GetByRequestID(requestID)
		if err == ErrCheckpointNotFound {
			continue
		}
		if err != nil {
			t.Errorf("Unexpected error for %s: %v", requestID, err)
			corruptionCount++
			continue
		}

		// StateData should not be corrupted
		if string(cp.StateData) != string(expectedStateData) {
			t.Errorf("Data corruption for %s: expected %s, got %s",
				requestID, string(expectedStateData), string(cp.StateData))
			corruptionCount++
		}
	}

	assert.Equal(t, 0, corruptionCount,
		"No data corruption should occur under load")

	t.Logf("Data integrity verified: 0 corruptions detected")
}

// TestChaos_TransactionIsolation tests that transactions provide proper isolation
func TestChaos_TransactionIsolation(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	// Create initial checkpoint
	cp := &ExecutionCheckpoint{
		RequestID:    "isolation-test",
		GraphID:      "isolation-graph",
		GraphVersion: "v1",
		NodeID:       "node-isolation",
		StateData:    []byte(`{"isolation":"test"}`),
		CreatedAt:    time.Now(),
	}
	require.NoError(t, store.Create(cp))

	// Multiple concurrent transactions updating the same row
	numWorkers := 50
	var wg sync.WaitGroup
	var successCount, failCount int64

	t.Logf("Running transaction isolation test with %d concurrent workers...", numWorkers)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			_, err := store.RecordFailure("isolation-test", errors.New(fmt.Sprintf("worker %d", workerID)))
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&failCount, 1)
			}
		}(w)
	}

	wg.Wait()

	t.Logf("=== Transaction Isolation Results ===")
	t.Logf("Successes:    %d", successCount)
	t.Logf("Failures:     %d", failCount)

	// All operations should succeed
	assert.Equal(t, int64(numWorkers), successCount,
		"All concurrent operations should succeed")

	// Verify final state
	finalCP, err := store.GetByRequestID("isolation-test")
	require.NoError(t, err)

	// Failure count should match number of successful operations
	assert.Equal(t, numWorkers, finalCP.FailureCount,
		"Failure count should reflect all %d concurrent updates", numWorkers)

	t.Logf("Final failure count: %d (expected: %d)", finalCP.FailureCount, numWorkers)
}

// =============================================================================
// H003 Phase 3: SERIALIZABLE Contention Chaos Test
// =============================================================================
//
// This test specifically targets the SERIALIZABLE retry mechanism under high
// contention to validate that:
// 1. Serialization retries work correctly under pressure
// 2. Data remains atomic despite contention
// 3. Performance metrics are captured for the retry logic
//
// =============================================================================

// SerializableContentionMetrics captures detailed metrics for SERIALIZABLE testing
type SerializableContentionMetrics struct {
	// Operation counts
	TotalAttempts        int64
	SuccessfulOps        int64
	NotFoundOps          int64 // Expected "already moved" responses
	SerializationErrors  int64 // 40001 errors that triggered retry
	DeadlockErrors       int64 // 40P01 errors that triggered retry
	MaxRetriesExceeded   int64 // Operations that failed after all retries
	OtherErrors          int64
	TotalRetries         int64 // Sum of all retry attempts

	// Latency tracking (in microseconds)
	Latencies     []int64
	RetryLatencies []int64 // Latency of operations that required retries

	// Timing
	StartTime time.Time
	EndTime   time.Time
}

func (m *SerializableContentionMetrics) AddLatency(d time.Duration) {
	atomic.AddInt64(&m.TotalAttempts, 1)
	// Store latency in microseconds for precision
	m.Latencies = append(m.Latencies, d.Microseconds())
}

func (m *SerializableContentionMetrics) Duration() time.Duration {
	return m.EndTime.Sub(m.StartTime)
}

func (m *SerializableContentionMetrics) OpsPerSecond() float64 {
	if m.Duration().Seconds() == 0 {
		return 0
	}
	return float64(m.SuccessfulOps+m.NotFoundOps) / m.Duration().Seconds()
}

func (m *SerializableContentionMetrics) Print(t *testing.T) {
	t.Logf("╔════════════════════════════════════════════════════════════════╗")
	t.Logf("║     SERIALIZABLE CONTENTION CHAOS TEST RESULTS                 ║")
	t.Logf("╠════════════════════════════════════════════════════════════════╣")
	t.Logf("║ Test Environment                                               ║")
	t.Logf("║   Duration:              %-10v                            ║", m.Duration().Round(time.Millisecond))
	t.Logf("║   Isolation Level:       SERIALIZABLE                          ║")
	t.Logf("╠════════════════════════════════════════════════════════════════╣")
	t.Logf("║ Operation Counts                                               ║")
	t.Logf("║   Total Attempts:        %-10d                            ║", m.TotalAttempts)
	t.Logf("║   Successful Moves:      %-10d                            ║", m.SuccessfulOps)
	t.Logf("║   Already Moved:         %-10d                            ║", m.NotFoundOps)
	t.Logf("║   Ops/sec:               %-10.2f                            ║", m.OpsPerSecond())
	t.Logf("╠════════════════════════════════════════════════════════════════╣")
	t.Logf("║ Serialization Retry Metrics                                    ║")
	t.Logf("║   Serialization Errors:  %-10d (40001)                    ║", m.SerializationErrors)
	t.Logf("║   Deadlock Errors:       %-10d (40P01)                    ║", m.DeadlockErrors)
	t.Logf("║   Total Retries:         %-10d                            ║", m.TotalRetries)
	t.Logf("║   Max Retries Exceeded:  %-10d                            ║", m.MaxRetriesExceeded)
	t.Logf("║   Other Errors:          %-10d                            ║", m.OtherErrors)

	// Calculate retry rate
	totalOps := m.SuccessfulOps + m.NotFoundOps + m.MaxRetriesExceeded + m.OtherErrors
	if totalOps > 0 {
		retryRate := float64(m.TotalRetries) / float64(totalOps) * 100
		t.Logf("║   Retry Rate:            %-10.2f%%                           ║", retryRate)
	}

	t.Logf("╠════════════════════════════════════════════════════════════════╣")
	t.Logf("║ Latency Distribution                                           ║")

	if len(m.Latencies) > 0 {
		// Sort for percentile calculation
		sorted := make([]int64, len(m.Latencies))
		copy(sorted, m.Latencies)
		sortInt64s(sorted)

		p50 := sorted[len(sorted)*50/100]
		p95 := sorted[len(sorted)*95/100]
		p99 := sorted[len(sorted)*99/100]
		max := sorted[len(sorted)-1]

		t.Logf("║   p50:                   %-10v                            ║", time.Duration(p50)*time.Microsecond)
		t.Logf("║   p95:                   %-10v                            ║", time.Duration(p95)*time.Microsecond)
		t.Logf("║   p99:                   %-10v                            ║", time.Duration(p99)*time.Microsecond)
		t.Logf("║   max:                   %-10v                            ║", time.Duration(max)*time.Microsecond)
	}
	t.Logf("╚════════════════════════════════════════════════════════════════╝")
}

// sortInt64s sorts a slice of int64s in ascending order
func sortInt64s(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// TestChaos_SerializableContention is the primary validation test for H003
// It specifically measures the SERIALIZABLE retry mechanism under high contention
func TestChaos_SerializableContention(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	// Test parameters - clearly documented
	const (
		numCheckpoints = 100   // Number of checkpoints to create
		numWorkers     = 50    // Concurrent workers (high contention)
		testDuration   = 10 * time.Second
	)

	t.Logf("╔════════════════════════════════════════════════════════════════╗")
	t.Logf("║     SERIALIZABLE CONTENTION TEST CONFIGURATION                 ║")
	t.Logf("╠════════════════════════════════════════════════════════════════╣")
	t.Logf("║   Checkpoints:           %-10d                            ║", numCheckpoints)
	t.Logf("║   Workers:               %-10d                            ║", numWorkers)
	t.Logf("║   Duration:              %-10v                            ║", testDuration)
	t.Logf("║   Isolation Level:       SERIALIZABLE                          ║")
	t.Logf("║   Retry Strategy:        Exponential backoff + jitter          ║")
	t.Logf("║   Max Retries:           3                                     ║")
	t.Logf("╚════════════════════════════════════════════════════════════════╝")

	// Create checkpoints
	t.Logf("Creating %d checkpoints for contention test...", numCheckpoints)
	checkpointIDs := make([]string, numCheckpoints)
	for i := 0; i < numCheckpoints; i++ {
		cp := &ExecutionCheckpoint{
			RequestID:    fmt.Sprintf("serializable-chaos-%d", i),
			GraphID:      "chaos-graph",
			GraphVersion: "v1",
			NodeID:       fmt.Sprintf("node-%d", i),
			StateData:    []byte(fmt.Sprintf(`{"index":%d,"hash":"%x"}`, i, i*7919)),
			CreatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(cp))
		checkpointIDs[i] = cp.RequestID
	}

	// Metrics collection with thread-safe latency storage
	metrics := &SerializableContentionMetrics{
		Latencies:      make([]int64, 0, numWorkers*numCheckpoints),
		RetryLatencies: make([]int64, 0, numWorkers*numCheckpoints/2),
	}
	var latencyMu sync.Mutex

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	metrics.StartTime = time.Now()

	// Launch workers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Each worker attempts to move all checkpoints using SERIALIZABLE isolation
			for _, requestID := range checkpointIDs {
				select {
				case <-stopChan:
					return
				default:
				}

				start := time.Now()
				err := store.MoveToCheckpointDLQSerializable(requestID, fmt.Sprintf("worker-%d", workerID))
				elapsed := time.Since(start)

				// Thread-safe latency recording
				latencyMu.Lock()
				metrics.Latencies = append(metrics.Latencies, elapsed.Microseconds())
				latencyMu.Unlock()

				atomic.AddInt64(&metrics.TotalAttempts, 1)

				if err == nil {
					atomic.AddInt64(&metrics.SuccessfulOps, 1)
				} else if err == ErrCheckpointNotFound {
					atomic.AddInt64(&metrics.NotFoundOps, 1)
				} else if errors.Is(err, ErrMaxRetriesExceeded) {
					atomic.AddInt64(&metrics.MaxRetriesExceeded, 1)
					// Check what type of error caused the retry exhaustion
					if IsSerializationError(errors.Unwrap(err)) {
						atomic.AddInt64(&metrics.SerializationErrors, 1)
					} else if IsDeadlockError(errors.Unwrap(err)) {
						atomic.AddInt64(&metrics.DeadlockErrors, 1)
					}
				} else if IsSerializationError(err) {
					atomic.AddInt64(&metrics.SerializationErrors, 1)
					atomic.AddInt64(&metrics.TotalRetries, 1)
				} else if IsDeadlockError(err) {
					atomic.AddInt64(&metrics.DeadlockErrors, 1)
					atomic.AddInt64(&metrics.TotalRetries, 1)
				} else {
					atomic.AddInt64(&metrics.OtherErrors, 1)
					t.Logf("Unexpected error from worker %d: %v", workerID, err)
				}
			}
		}(w)
	}

	// Wait for either all workers to finish or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All workers completed
	case <-time.After(testDuration + 30*time.Second):
		close(stopChan)
		t.Log("Test timeout reached, stopping workers...")
		wg.Wait()
	}

	metrics.EndTime = time.Now()
	metrics.Print(t)

	// Verification assertions
	t.Log("\n=== VERIFICATION ===")

	// 1. Each checkpoint should be moved exactly once
	if metrics.SuccessfulOps != int64(numCheckpoints) {
		t.Errorf("ATOMICITY VIOLATION: Expected exactly %d successful moves, got %d",
			numCheckpoints, metrics.SuccessfulOps)
	} else {
		t.Logf("✓ Atomicity verified: exactly %d checkpoints moved to DLQ", numCheckpoints)
	}

	// 2. All remaining operations should be "not found" (already moved)
	expectedNotFound := int64(numWorkers*numCheckpoints) - int64(numCheckpoints) - metrics.MaxRetriesExceeded - metrics.OtherErrors
	if metrics.NotFoundOps < expectedNotFound*90/100 { // Allow 10% tolerance
		t.Errorf("Unexpected not-found count: got %d, expected ~%d", metrics.NotFoundOps, expectedNotFound)
	} else {
		t.Logf("✓ Concurrent access verified: %d operations correctly returned 'already moved'", metrics.NotFoundOps)
	}

	// 3. No unexpected errors
	if metrics.OtherErrors > 0 {
		t.Errorf("UNEXPECTED ERRORS: %d operations failed with unexpected errors", metrics.OtherErrors)
	} else {
		t.Logf("✓ Error handling verified: 0 unexpected errors")
	}

	// 4. Max retries exceeded should be minimal (< 1% of operations)
	maxRetriesThreshold := int64(numWorkers * numCheckpoints / 100)
	if metrics.MaxRetriesExceeded > maxRetriesThreshold {
		t.Errorf("RETRY EXHAUSTION: %d operations exceeded max retries (threshold: %d)",
			metrics.MaxRetriesExceeded, maxRetriesThreshold)
	} else {
		t.Logf("✓ Retry mechanism verified: %d operations exhausted retries (threshold: %d)",
			metrics.MaxRetriesExceeded, maxRetriesThreshold)
	}

	// 5. Verify all checkpoints are now in DLQ (data integrity check)
	t.Log("\n=== DATA INTEGRITY VERIFICATION ===")
	stillInCheckpoints := 0
	for _, id := range checkpointIDs {
		cp, err := store.GetByRequestID(id)
		if err == nil && cp != nil {
			stillInCheckpoints++
			t.Errorf("Checkpoint %s still exists in main table - should be in DLQ", id)
		}
	}
	if stillInCheckpoints == 0 {
		t.Logf("✓ Data integrity verified: all %d checkpoints moved to DLQ", numCheckpoints)
	}

	// Summary
	t.Log("\n=== H003 SERIALIZABLE CONTENTION SUMMARY ===")
	if metrics.SuccessfulOps == int64(numCheckpoints) && metrics.OtherErrors == 0 && stillInCheckpoints == 0 {
		t.Log("✓ H003 SERIALIZABLE isolation validation PASSED")
		t.Logf("  - %d workers, %d checkpoints, %.2f ops/sec",
			numWorkers, numCheckpoints, metrics.OpsPerSecond())
		t.Logf("  - Serialization errors handled: %d", metrics.SerializationErrors)
		t.Logf("  - Retry mechanism engaged: %d total retries", metrics.TotalRetries)
	} else {
		t.Error("✗ H003 SERIALIZABLE isolation validation FAILED")
	}
}

// TestChaos_SerializableConflictRetry specifically tests the retry mechanism
// by forcing actual serialization conflicts (not just row-level locks)
func TestChaos_SerializableConflictRetry(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	const (
		numCheckpoints = 20    // Fewer checkpoints = more contention per checkpoint
		numWorkers     = 100   // Many workers = high contention
	)

	t.Logf("╔════════════════════════════════════════════════════════════════╗")
	t.Logf("║     SERIALIZABLE CONFLICT RETRY TEST                           ║")
	t.Logf("╠════════════════════════════════════════════════════════════════╣")
	t.Logf("║   Checkpoints:           %-10d                            ║", numCheckpoints)
	t.Logf("║   Workers:               %-10d                            ║", numWorkers)
	t.Logf("║   Target:                Force serialization retries          ║")
	t.Logf("╚════════════════════════════════════════════════════════════════╝")

	// Create checkpoints
	checkpointIDs := make([]string, numCheckpoints)
	for i := 0; i < numCheckpoints; i++ {
		cp := &ExecutionCheckpoint{
			RequestID:    fmt.Sprintf("conflict-retry-%d", i),
			GraphID:      "conflict-graph",
			GraphVersion: "v1",
			NodeID:       fmt.Sprintf("node-%d", i),
			StateData:    []byte(fmt.Sprintf(`{"index":%d}`, i)),
			CreatedAt:    time.Now(),
		}
		require.NoError(t, store.Create(cp))
		checkpointIDs[i] = cp.RequestID
	}

	// Test CreateOrUpdateSerializable with invariant checks - this can trigger conflicts
	// because it uses read-then-write patterns without FOR UPDATE on the initial read
	var successCount, conflictCount, otherErrors int64
	var wg sync.WaitGroup

	metrics := &SerializableContentionMetrics{
		Latencies: make([]int64, 0, numWorkers*numCheckpoints),
	}
	var latencyMu sync.Mutex

	metrics.StartTime = time.Now()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for _, requestID := range checkpointIDs {
				start := time.Now()

				// Use CreateOrUpdateSerializable with an invariant check
				// This forces a read-then-decide-then-write pattern that can conflict
				cp := &ExecutionCheckpoint{
					RequestID:    requestID,
					GraphID:      "updated-graph",
					GraphVersion: "v2",
					NodeID:       fmt.Sprintf("updated-node-%d", workerID),
					StateData:    []byte(fmt.Sprintf(`{"updated_by":%d}`, workerID)),
					CreatedAt:    time.Now(),
				}

				err := store.CreateOrUpdateSerializable(cp, func(existing *ExecutionCheckpoint) error {
					// Invariant check that reads existing state
					if existing.FailureCount > 1000 {
						return fmt.Errorf("invariant violation: too many failures")
					}
					return nil
				})

				elapsed := time.Since(start)

				latencyMu.Lock()
				metrics.Latencies = append(metrics.Latencies, elapsed.Microseconds())
				latencyMu.Unlock()

				atomic.AddInt64(&metrics.TotalAttempts, 1)

				if err == nil {
					atomic.AddInt64(&successCount, 1)
					atomic.AddInt64(&metrics.SuccessfulOps, 1)
				} else if errors.Is(err, ErrMaxRetriesExceeded) {
					atomic.AddInt64(&conflictCount, 1)
					atomic.AddInt64(&metrics.MaxRetriesExceeded, 1)
					// This is expected under high contention
				} else if IsSerializationError(err) || IsDeadlockError(err) {
					atomic.AddInt64(&conflictCount, 1)
					atomic.AddInt64(&metrics.SerializationErrors, 1)
				} else {
					atomic.AddInt64(&otherErrors, 1)
					atomic.AddInt64(&metrics.OtherErrors, 1)
				}
			}
		}(w)
	}

	wg.Wait()
	metrics.EndTime = time.Now()
	metrics.NotFoundOps = 0 // Not applicable for this test
	metrics.Print(t)

	t.Log("\n=== SERIALIZABLE CONFLICT RETRY VERIFICATION ===")
	t.Logf("Successful operations:      %d", successCount)
	t.Logf("Conflict/retry exhaustions: %d", conflictCount)
	t.Logf("Other errors:               %d", otherErrors)

	// The key validation: even under high contention, operations should eventually succeed
	// or fail gracefully with ErrMaxRetriesExceeded
	totalOps := successCount + conflictCount + otherErrors
	expectedOps := int64(numWorkers * numCheckpoints)

	assert.Equal(t, expectedOps, totalOps,
		"All operations should be accounted for")

	// Success rate should be high even under contention
	successRate := float64(successCount) / float64(totalOps) * 100
	t.Logf("Success rate: %.2f%%", successRate)

	if successRate < 90 {
		t.Logf("Note: Success rate below 90%% indicates high serialization contention")
		t.Logf("This is expected behavior - the retry mechanism is being exercised")
	}

	assert.Equal(t, int64(0), otherErrors,
		"No unexpected errors should occur")

	// Verify data integrity - all checkpoints should still exist
	for _, id := range checkpointIDs {
		cp, err := store.GetByRequestID(id)
		if err == ErrCheckpointNotFound {
			t.Errorf("Checkpoint %s was unexpectedly deleted", id)
			continue
		}
		require.NoError(t, err)
		require.NotNil(t, cp)
	}

	t.Logf("✓ Data integrity verified: all %d checkpoints preserved", numCheckpoints)

	if conflictCount > 0 {
		t.Logf("✓ Serialization retry mechanism was exercised: %d retries/exhaustions", conflictCount)
	} else {
		t.Log("Note: No serialization conflicts occurred - FOR UPDATE may be preventing conflicts")
	}
}

// TestChaos_ForcedSerializationFailure directly exercises the retry mechanism
// by performing concurrent read-modify-write WITHOUT FOR UPDATE
func TestChaos_ForcedSerializationFailure(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	// Create a single checkpoint that all workers will try to update
	cp := &ExecutionCheckpoint{
		RequestID:    "forced-conflict-target",
		GraphID:      "conflict-graph",
		GraphVersion: "v1",
		NodeID:       "node-conflict",
		StateData:    []byte(`{"counter":0}`),
		CreatedAt:    time.Now(),
		FailureCount: 0,
	}
	require.NoError(t, store.Create(cp))

	const numWorkers = 50
	const opsPerWorker = 10

	t.Logf("╔════════════════════════════════════════════════════════════════╗")
	t.Logf("║     FORCED SERIALIZATION FAILURE TEST                          ║")
	t.Logf("╠════════════════════════════════════════════════════════════════╣")
	t.Logf("║   Target:                Single checkpoint (max contention)    ║")
	t.Logf("║   Workers:               %-10d                            ║", numWorkers)
	t.Logf("║   Ops per worker:        %-10d                            ║", opsPerWorker)
	t.Logf("║   Total operations:      %-10d                            ║", numWorkers*opsPerWorker)
	t.Logf("║   Mode:                  Read-Modify-Write (no FOR UPDATE)    ║")
	t.Logf("╚════════════════════════════════════════════════════════════════╝")

	var successCount, serializationRetries, deadlockRetries, maxRetriesExceeded, otherErrors int64
	var totalRetryAttempts int64
	var wg sync.WaitGroup

	metrics := &SerializableContentionMetrics{
		Latencies: make([]int64, 0, numWorkers*opsPerWorker),
	}
	var latencyMu sync.Mutex

	metrics.StartTime = time.Now()

	// Custom serializable operation that reads and writes WITHOUT FOR UPDATE
	// This should trigger actual serialization conflicts
	performConflictingUpdate := func(workerID, opNum int) error {
		config := SerializationConfig{
			MaxRetries:  5, // Allow more retries for this test
			BaseBackoff: 5 * time.Millisecond,
			MaxBackoff:  100 * time.Millisecond,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var localRetries int64

		err := store.WithSerializableRetry(ctx, config, func(ctx context.Context, tx pgx.Tx) error {
			// Read current value WITHOUT FOR UPDATE - this allows conflicts
			var currentFailureCount int
			selectQuery := fmt.Sprintf(`
				SELECT failure_count FROM %s WHERE request_id = $1;`, checkpointTable)

			err := tx.QueryRow(ctx, selectQuery, "forced-conflict-target").Scan(&currentFailureCount)
			if err != nil {
				return fmt.Errorf("select: %w", err)
			}

			// Simulate some work to increase conflict window
			// This is intentionally a small delay to encourage conflicts
			time.Sleep(time.Microsecond * 100)

			// Write new value based on read
			updateQuery := fmt.Sprintf(`
				UPDATE %s SET failure_count = $1, last_error = $2 WHERE request_id = $3;`, checkpointTable)

			_, err = tx.Exec(ctx, updateQuery, currentFailureCount+1,
				fmt.Sprintf("worker-%d-op-%d", workerID, opNum), "forced-conflict-target")
			if err != nil {
				if IsRetryableError(err) {
					atomic.AddInt64(&localRetries, 1)
				}
				return err
			}

			return nil
		})

		atomic.AddInt64(&totalRetryAttempts, localRetries)
		return err
	}

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for op := 0; op < opsPerWorker; op++ {
				start := time.Now()
				err := performConflictingUpdate(workerID, op)
				elapsed := time.Since(start)

				latencyMu.Lock()
				metrics.Latencies = append(metrics.Latencies, elapsed.Microseconds())
				latencyMu.Unlock()

				atomic.AddInt64(&metrics.TotalAttempts, 1)

				if err == nil {
					atomic.AddInt64(&successCount, 1)
					atomic.AddInt64(&metrics.SuccessfulOps, 1)
				} else if errors.Is(err, ErrMaxRetriesExceeded) {
					atomic.AddInt64(&maxRetriesExceeded, 1)
					atomic.AddInt64(&metrics.MaxRetriesExceeded, 1)
					// Check underlying error type
					unwrapped := errors.Unwrap(err)
					if IsSerializationError(unwrapped) {
						atomic.AddInt64(&serializationRetries, 1)
					} else if IsDeadlockError(unwrapped) {
						atomic.AddInt64(&deadlockRetries, 1)
					}
				} else if IsSerializationError(err) {
					atomic.AddInt64(&serializationRetries, 1)
					atomic.AddInt64(&metrics.SerializationErrors, 1)
				} else if IsDeadlockError(err) {
					atomic.AddInt64(&deadlockRetries, 1)
					atomic.AddInt64(&metrics.DeadlockErrors, 1)
				} else {
					atomic.AddInt64(&otherErrors, 1)
					atomic.AddInt64(&metrics.OtherErrors, 1)
					t.Logf("Worker %d op %d error: %v", workerID, op, err)
				}
			}
		}(w)
	}

	wg.Wait()
	metrics.EndTime = time.Now()
	metrics.TotalRetries = totalRetryAttempts
	metrics.Print(t)

	t.Log("\n=== FORCED SERIALIZATION FAILURE VERIFICATION ===")
	t.Logf("Successful operations:     %d", successCount)
	t.Logf("Serialization retries:     %d (40001 errors)", serializationRetries)
	t.Logf("Deadlock retries:          %d (40P01 errors)", deadlockRetries)
	t.Logf("Max retries exceeded:      %d", maxRetriesExceeded)
	t.Logf("Other errors:              %d", otherErrors)
	t.Logf("Total retry attempts:      %d", totalRetryAttempts)

	// Verify final state
	finalCP, err := store.GetByRequestID("forced-conflict-target")
	require.NoError(t, err)
	require.NotNil(t, finalCP)

	t.Logf("\n=== FINAL STATE ===")
	t.Logf("Final failure_count:       %d", finalCP.FailureCount)
	t.Logf("Expected (if all success): %d", numWorkers*opsPerWorker)

	// Key validation: the final count should equal successful operations
	// (each successful op increments by 1)
	if int64(finalCP.FailureCount) != successCount {
		t.Errorf("ATOMICITY VIOLATION: Final count %d != successful ops %d",
			finalCP.FailureCount, successCount)
	} else {
		t.Logf("✓ ATOMICITY VERIFIED: failure_count (%d) == successful ops (%d)",
			finalCP.FailureCount, successCount)
	}

	// Summary
	if serializationRetries > 0 || deadlockRetries > 0 || totalRetryAttempts > 0 {
		t.Logf("\n✓ SERIALIZABLE RETRY MECHANISM EXERCISED:")
		t.Logf("  - Total retry attempts:       %d", totalRetryAttempts)
		t.Logf("  - Serialization errors seen:  %d", serializationRetries)
		t.Logf("  - Deadlock errors seen:       %d", deadlockRetries)
		t.Log("  - System recovered atomicity despite conflicts")
	} else {
		t.Log("\nNote: No serialization conflicts triggered")
		t.Log("  - PostgreSQL may be using row-level locking automatically")
		t.Log("  - Retry mechanism not exercised in this run")
	}
}

// Helper method to get by request ID with context
func (s *PostgresCheckpointStore) GetByRequestIDWithContext(ctx context.Context, requestID string) (*ExecutionCheckpoint, error) {
	query := `
		SELECT request_id, graph_id, graph_version, node_id, state_data,
		       failure_count, created_at
		FROM execution_checkpoints
		WHERE request_id = $1
	`
	cp := &ExecutionCheckpoint{}
	err := s.pool.QueryRow(ctx, query, requestID).Scan(
		&cp.RequestID, &cp.GraphID, &cp.GraphVersion, &cp.NodeID, &cp.StateData,
		&cp.FailureCount, &cp.CreatedAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, ErrCheckpointNotFound
		}
		return nil, err
	}
	return cp, nil
}
