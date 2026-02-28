//go:build integration

package hitl

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestAtomicity_ConcurrentCreateSameRequestID validates that concurrent Create
// operations with the same request_id result in exactly one checkpoint.
// H002 Validation: Concurrent writes do not result in duplicate data.
func TestAtomicity_ConcurrentCreateSameRequestID(t *testing.T) {
	withPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresCheckpointStore(ctx, pool)
		require.NoError(t, err)

		const numGoroutines = 20
		requestID := "concurrent-create-test"

		var wg sync.WaitGroup
		var successCount atomic.Int32

		// Launch concurrent Create operations
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				cp := &ExecutionCheckpoint{
					RequestID:    requestID,
					GraphID:      fmt.Sprintf("graph-%d", idx),
					GraphVersion: "v1",
					NodeID:       "node-a",
					StateData:    []byte(fmt.Sprintf("state-%d", idx)),
					CreatedAt:    time.Now(),
					ExpiresAt:    time.Now().Add(time.Hour),
					TraceID:      fmt.Sprintf("trace-%d", idx),
				}
				if err := store.Create(cp); err == nil {
					successCount.Add(1)
				}
			}(i)
		}

		wg.Wait()

		// Verify: All creates should "succeed" (ON CONFLICT DO NOTHING doesn't error)
		require.Equal(t, int32(numGoroutines), successCount.Load(), "All creates should complete without error")

		// Verify: Only ONE checkpoint exists
		cp, err := store.GetByRequestID(requestID)
		require.NoError(t, err)
		require.NotNil(t, cp)
		require.Equal(t, requestID, cp.RequestID)

		// Verify via direct query: exactly 1 row
		var rowCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM hitl_execution_checkpoints WHERE request_id = $1", requestID).Scan(&rowCount)
		require.NoError(t, err)
		require.Equal(t, 1, rowCount, "Exactly one checkpoint should exist")

		t.Log("PASS: Concurrent creates resulted in exactly 1 checkpoint (atomicity verified)")
	})
}

// TestAtomicity_ConcurrentRecordFailure validates that concurrent RecordFailure
// operations correctly increment failure_count without lost updates.
// H002 Validation: Concurrent updates do not cause lost updates.
func TestAtomicity_ConcurrentRecordFailure(t *testing.T) {
	withPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresCheckpointStore(ctx, pool)
		require.NoError(t, err)

		requestID := "concurrent-failure-test"
		const numGoroutines = 50

		// Create initial checkpoint
		cp := &ExecutionCheckpoint{
			RequestID:    requestID,
			GraphID:      "graph-1",
			GraphVersion: "v1",
			NodeID:       "node-a",
			StateData:    []byte("initial-state"),
			CreatedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(time.Hour),
		}
		require.NoError(t, store.Create(cp))

		var wg sync.WaitGroup
		var successCount atomic.Int32

		// Launch concurrent RecordFailure operations
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				testErr := fmt.Errorf("failure-%d", idx)
				_, err := store.RecordFailure(requestID, testErr)
				if err == nil {
					successCount.Add(1)
				}
			}(i)
		}

		wg.Wait()

		// Verify: All RecordFailure calls succeeded
		require.Equal(t, int32(numGoroutines), successCount.Load(), "All RecordFailure calls should succeed")

		// Verify: failure_count equals exactly numGoroutines
		finalCp, err := store.GetByRequestID(requestID)
		require.NoError(t, err)
		require.Equal(t, numGoroutines, finalCp.FailureCount, "Failure count should equal number of concurrent increments")

		t.Logf("PASS: Concurrent RecordFailure resulted in failure_count=%d (no lost updates)", finalCp.FailureCount)
	})
}

// TestAtomicity_ConcurrentMoveToCheckpointDLQ validates that concurrent DLQ moves
// result in exactly one successful move, others get ErrCheckpointNotFound.
// H002 Validation: Transaction isolation prevents double-move corruption.
func TestAtomicity_ConcurrentMoveToCheckpointDLQ(t *testing.T) {
	withPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresCheckpointStore(ctx, pool)
		require.NoError(t, err)

		requestID := "concurrent-dlq-test"
		const numGoroutines = 10

		// Create initial checkpoint
		cp := &ExecutionCheckpoint{
			RequestID:    requestID,
			GraphID:      "graph-1",
			GraphVersion: "v1",
			NodeID:       "node-a",
			StateData:    []byte("state"),
			CreatedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(time.Hour),
		}
		require.NoError(t, store.Create(cp))

		var wg sync.WaitGroup
		var successCount atomic.Int32
		var notFoundCount atomic.Int32

		// Launch concurrent MoveToCheckpointDLQ operations
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				err := store.MoveToCheckpointDLQ(requestID, fmt.Sprintf("dlq-error-%d", idx))
				if err == nil {
					successCount.Add(1)
				} else if err == ErrCheckpointNotFound {
					notFoundCount.Add(1)
				}
			}(i)
		}

		wg.Wait()

		// Verify: Exactly ONE successful move
		require.Equal(t, int32(1), successCount.Load(), "Exactly one DLQ move should succeed")
		require.Equal(t, int32(numGoroutines-1), notFoundCount.Load(), "Others should get ErrCheckpointNotFound")

		// Verify: Checkpoint no longer in active table
		_, err = store.GetByRequestID(requestID)
		require.ErrorIs(t, err, ErrCheckpointNotFound)

		// Verify: Exactly 1 row in DLQ table
		var dlqCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM hitl_checkpoint_dlq WHERE request_id = $1", requestID).Scan(&dlqCount)
		require.NoError(t, err)
		require.Equal(t, 1, dlqCount, "Exactly one DLQ entry should exist")

		t.Log("PASS: Concurrent DLQ moves resulted in exactly 1 DLQ entry (transaction isolation verified)")
	})
}

// TestAtomicity_RaceCreateAndDelete validates that concurrent Create and Delete
// operations do not cause data corruption.
// H002 Validation: Create/Delete race does not corrupt state.
func TestAtomicity_RaceCreateAndDelete(t *testing.T) {
	withPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresCheckpointStore(ctx, pool)
		require.NoError(t, err)

		requestID := "race-create-delete-test"
		const numIterations = 100

		var wg sync.WaitGroup

		// Launch interleaved Create and Delete operations
		for i := 0; i < numIterations; i++ {
			wg.Add(2)

			// Create goroutine
			go func(idx int) {
				defer wg.Done()
				cp := &ExecutionCheckpoint{
					RequestID:    requestID,
					GraphID:      fmt.Sprintf("graph-%d", idx),
					GraphVersion: "v1",
					NodeID:       "node-a",
					StateData:    []byte(fmt.Sprintf("state-%d", idx)),
					CreatedAt:    time.Now(),
					ExpiresAt:    time.Now().Add(time.Hour),
				}
				_ = store.Create(cp)
			}(i)

			// Delete goroutine
			go func() {
				defer wg.Done()
				_ = store.Delete(requestID)
			}()
		}

		wg.Wait()

		// Verify: Row count is 0 or 1 (never more, never negative)
		var rowCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM hitl_execution_checkpoints WHERE request_id = $1", requestID).Scan(&rowCount)
		require.NoError(t, err)
		require.True(t, rowCount == 0 || rowCount == 1, "Row count should be 0 or 1, got %d", rowCount)

		t.Logf("PASS: Create/Delete race resulted in row_count=%d (no corruption)", rowCount)
	})
}

// TestAtomicity_RaceRecordFailureAndDLQ validates that RecordFailure and
// MoveToCheckpointDLQ racing do not cause corruption.
// H002 Validation: RecordFailure after DLQ move correctly fails.
func TestAtomicity_RaceRecordFailureAndDLQ(t *testing.T) {
	withPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresCheckpointStore(ctx, pool)
		require.NoError(t, err)

		requestID := "race-failure-dlq-test"

		// Create initial checkpoint with some failures
		cp := &ExecutionCheckpoint{
			RequestID:    requestID,
			GraphID:      "graph-1",
			GraphVersion: "v1",
			NodeID:       "node-a",
			StateData:    []byte("state"),
			CreatedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(time.Hour),
		}
		require.NoError(t, store.Create(cp))

		const numFailureGoroutines = 20
		const numDLQGoroutines = 5

		var wg sync.WaitGroup
		var failureSuccessCount atomic.Int32
		var dlqSuccessCount atomic.Int32

		// Launch RecordFailure goroutines
		for i := 0; i < numFailureGoroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, err := store.RecordFailure(requestID, fmt.Errorf("failure-%d", idx))
				if err == nil {
					failureSuccessCount.Add(1)
				}
			}(i)
		}

		// Launch MoveToCheckpointDLQ goroutines
		for i := 0; i < numDLQGoroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				err := store.MoveToCheckpointDLQ(requestID, fmt.Sprintf("dlq-%d", idx))
				if err == nil {
					dlqSuccessCount.Add(1)
				}
			}(i)
		}

		wg.Wait()

		// Verify: Exactly 1 DLQ move succeeded
		require.Equal(t, int32(1), dlqSuccessCount.Load(), "Exactly one DLQ move should succeed")

		// Verify: Active table is empty
		var activeCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM hitl_execution_checkpoints WHERE request_id = $1", requestID).Scan(&activeCount)
		require.NoError(t, err)
		require.Equal(t, 0, activeCount, "Active table should be empty after DLQ move")

		// Verify: DLQ has exactly 1 entry
		var dlqCount int
		err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM hitl_checkpoint_dlq WHERE request_id = $1", requestID).Scan(&dlqCount)
		require.NoError(t, err)
		require.Equal(t, 1, dlqCount, "DLQ should have exactly 1 entry")

		// Verify: failure_count in DLQ reflects successful increments before DLQ move
		var dlqFailureCount int
		err = pool.QueryRow(ctx, "SELECT failure_count FROM hitl_checkpoint_dlq WHERE request_id = $1", requestID).Scan(&dlqFailureCount)
		require.NoError(t, err)
		require.GreaterOrEqual(t, dlqFailureCount, 0, "DLQ failure_count should be >= 0")
		require.LessOrEqual(t, dlqFailureCount, numFailureGoroutines, "DLQ failure_count should be <= total failures attempted")

		t.Logf("PASS: RecordFailure/DLQ race: %d successful failures before DLQ, %d DLQ moves",
			failureSuccessCount.Load(), dlqSuccessCount.Load())
	})
}
