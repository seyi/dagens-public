package hitl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn" // Added this import
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ===========================================================================
// Test Helpers
// ===========================================================================

var (
	testPgContainer testcontainers.Container
	pgxPool         *pgxpool.Pool
	redisClient     *redis.Client
	redisContainer  testcontainers.Container
	// testCP is a base checkpoint for tests. Make copies before modifying in tests.
	testCP          = &ExecutionCheckpoint{
		RequestID:    "test-req-base",
		GraphID:      "g1",
		GraphVersion: "v1",
		NodeID:       "n1",
		StateData:    []byte(`{"key":"value"}`),
		CreatedAt:    time.Now(),
		FailureCount: 0,
	}
)

func TestMain(m *testing.M) {
	if !dockerAvailable() {
		fmt.Println("Skipping pkg/hitl: Docker runtime unavailable for testcontainers")
		os.Exit(0)
	}

	ctx := context.Background()

	// Setup PostgreSQL Container
	pgReq := testcontainers.ContainerRequest{
		Image:        "postgres:15-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Cmd:          []string{"postgres", "-c", "fsync=off"},
		Env: map[string]string{
			"POSTGRES_DB":       "testdb",
			"POSTGRES_PASSWORD": "testpassword",
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		).WithStartupTimeout(5 * time.Minute),
	}
	var err error
	testPgContainer, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: pgReq,
		Started:          true,
	})
	if err != nil {
		fmt.Printf("Skipping pkg/hitl: PostgreSQL container unavailable: %v\n", err)
		os.Exit(0)
	}

	pgPort, _ := testPgContainer.MappedPort(ctx, "5432")
	pgHost, _ := testPgContainer.Host(ctx)
	pgConnStr := fmt.Sprintf("postgres://postgres:testpassword@%s:%s/testdb?sslmode=disable", pgHost, pgPort.Port())

	for i := 0; i < 10; i++ {
		pgxPool, err = pgxpool.New(ctx, pgConnStr)
		if err == nil {
			if pingErr := pgxPool.Ping(ctx); pingErr == nil {
				break
			}
			pgxPool.Close()
		}
		time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
	}
	if err != nil {
		fmt.Printf("Failed to create pgxpool after retries: %v\n", err)
		os.Exit(1)
	}

	_, err = NewPostgresCheckpointStore(ctx, pgxPool)
	if err != nil {
		fmt.Printf("Failed to initialize PostgresCheckpointStore: %v\n", err)
		os.Exit(1)
	}

	redisReq := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(2 * time.Minute),
	}
	redisContainer, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: redisReq,
		Started:          true,
	})
	if err != nil {
		fmt.Printf("Skipping pkg/hitl: Redis container unavailable: %v\n", err)
		_ = testPgContainer.Terminate(ctx)
		pgxPool.Close()
		os.Exit(0)
	}

	redisPort, _ := redisContainer.MappedPort(ctx, "6379")
	redisHost, _ := redisContainer.Host(ctx)
	redisClient = redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", redisHost, redisPort.Port()),
	})

	for i := 0; i < 10; i++ {
		if pingErr := redisClient.Ping(ctx).Err(); pingErr == nil {
			break
		}
		time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
	}

	code := m.Run()

	_ = testPgContainer.Terminate(ctx)
	pgxPool.Close()
	_ = redisContainer.Terminate(ctx)
	redisClient.Close()

	os.Exit(code)
}

func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}

	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		return false
	}

	return true
}

func NewTestPostgresStore(t *testing.T) *PostgresCheckpointStore {
	store, err := NewPostgresCheckpointStore(context.Background(), pgxPool)
	assert.NoError(t, err)
	return store
}

func NewTestRedisClient(t *testing.T) *redis.Client {
	return redisClient
}

func cleanupTables(t *testing.T, pool *pgxpool.Pool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, fmt.Sprintf("TRUNCATE %s, %s CASCADE;", checkpointTable, checkpointDLQTable))
	assert.NoError(t, err)
}

func assertRowCount(t *testing.T, pool *pgxpool.Pool, table string, expected int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var count int
	err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s;", table)).Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, expected, count, fmt.Sprintf("Expected %d rows in %s, got %d", expected, table, count))
}

func concurrent(t *testing.T, n int, fn func(id int)) {
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			fn(id)
		}(i)
	}
	wg.Wait()
}

// isSerializationError checks if an error is a PostgreSQL serialization error (SQLSTATE 40001).
func isSerializationError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.SQLState() == "40001"
}


// ===========================================================================
// H002 - Postgres Atomicity Tests
// ===========================================================================

func TestPostgresCheckpointAtomicity_H002_ConcurrentCreates(t *testing.T) {
	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	numGoroutines := 50
	requestID := "concurrent-create-req"
	cp := *testCP
	cp.RequestID = requestID

	concurrent(t, numGoroutines, func(id int) {
		err := store.Create(&cp)
		assert.True(t, err == nil || (err != nil && errors.Is(err, pgx.ErrNoRows)), "Unexpected error type: %v", err)
	})

	assertRowCount(t, pgxPool, checkpointTable, 1)
	retrievedCP, err := store.GetByRequestID(requestID)
	assert.NoError(t, err)
	assert.NotNil(t, retrievedCP)
	assert.Equal(t, requestID, retrievedCP.RequestID)
}

func TestPostgresCheckpointAtomicity_H002_ConcurrentRecordFailure(t *testing.T) {
	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	requestID := "concurrent-failure-req"
	cp := *testCP
	cp.RequestID = requestID
	assert.NoError(t, store.Create(&cp))
	assertRowCount(t, pgxPool, checkpointTable, 1)

	initialCP, err := store.GetByRequestID(requestID)
	assert.NoError(t, err)
	assert.NotNil(t, initialCP)
	assert.Equal(t, 0, initialCP.FailureCount)

	numGoroutines := 50
	concurrent(t, numGoroutines, func(id int) {
		_, err := store.RecordFailure(requestID, fmt.Errorf("simulated error %d", id))
		assert.NoError(t, err)
	})

	finalCP, err := store.GetByRequestID(requestID)
	assert.NoError(t, err)
	assert.NotNil(t, finalCP)
	assert.Equal(t, numGoroutines, finalCP.FailureCount)
	assert.NotEqual(t, "", finalCP.LastError)
}

func TestPostgresCheckpointAtomicity_H002_ConcurrentMoveToCheckpointDLQ(t *testing.T) {
	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	requestID := "concurrent-dlq-req"
	cp := *testCP
	cp.RequestID = requestID
	assert.NoError(t, store.Create(&cp))
	assertRowCount(t, pgxPool, checkpointTable, 1)
	assertRowCount(t, pgxPool, checkpointDLQTable, 0)

	numGoroutines := 50
	var successfulMoves int32

	concurrent(t, numGoroutines, func(id int) {
		err := store.MoveToCheckpointDLQ(requestID, fmt.Sprintf("final error %d", id))
		if err == nil {
			atomic.AddInt32(&successfulMoves, 1)
		} else {
			assert.ErrorIs(t, err, ErrCheckpointNotFound)
		}
	})

	assert.Equal(t, int32(1), successfulMoves)
	assertRowCount(t, pgxPool, checkpointTable, 0)
	assertRowCount(t, pgxPool, checkpointDLQTable, 1)

	query := fmt.Sprintf("SELECT original_request_id, last_error FROM %s WHERE request_id = $1;", checkpointDLQTable)
	row := pgxPool.QueryRow(context.Background(), query, requestID)
	var originalRequestID, lastError string
	assert.NoError(t, row.Scan(&originalRequestID, &lastError))
	assert.Equal(t, requestID, originalRequestID)
	assert.Contains(t, lastError, "final error")
}

func TestPostgresCheckpointAtomicity_H002_TxCreateWithRollback(t *testing.T) {
	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	requestID := "tx-rollback-req"
	cp := *testCP
	cp.RequestID = requestID

	tx, err := store.BeginTransaction(context.Background())
	assert.NoError(t, err)

	err = store.CreateWithTransaction(tx, &cp)
	assert.NoError(t, err)

	err = tx.Rollback()
	assert.NoError(t, err)

	_, err = store.GetByRequestID(requestID)
	assert.ErrorIs(t, err, ErrCheckpointNotFound)
	assertRowCount(t, pgxPool, checkpointTable, 0)
}

func TestPostgresCheckpointAtomicity_H002_ReadAfterWriteConsistency(t *testing.T) {
	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	requestID := "raw-consistency-req"
	cp := *testCP
	cp.RequestID = requestID
	cp.StateData = []byte(`{"version": 1}`)

	assert.NoError(t, store.Create(&cp))

	numReaders := 20
	concurrent(t, numReaders, func(id int) {
		retrievedCP, err := store.GetByRequestID(requestID)
		assert.NoError(t, err)
		assert.NotNil(t, retrievedCP)
		assert.Equal(t, cp.StateData, retrievedCP.StateData)
	})
}

// ===========================================================================
// H003 - Production Atomicity Tests
// ===========================================================================

func TestAtomicityProduction_Phase1_MultiInstance(t *testing.T) {
	store := NewTestPostgresStore(t)

	t.Run("ConcurrentCreates_HighContention", func(t *testing.T) {
		cleanupTables(t, pgxPool)
		numGoroutines := 100
		requestID := "concurrent-create-prod-req"

		cpTemplate := *testCP
		cpTemplate.RequestID = requestID

		concurrent(t, numGoroutines, func(id int) {
			err := store.Create(&cpTemplate)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				assert.Contains(t, err.Error(), "no rows in result set", "Unexpected error: %v", err)
			}
		})

		assertRowCount(t, pgxPool, checkpointTable, 1)
	})

	t.Run("ConcurrentRecordFailure_ShouldBeAtomic", func(t *testing.T) {
		cleanupTables(t, pgxPool)
		numGoroutines := 100
		requestID := "concurrent-failure-prod-req"

		cpTemplate := *testCP
		cpTemplate.RequestID = requestID
		assert.NoError(t, store.Create(&cpTemplate))

		concurrent(t, numGoroutines, func(id int) {
			// This operation is atomic at the database level, so all increments should be preserved.
			_, err := store.RecordFailure(requestID, fmt.Errorf("error %d", id))
			assert.NoError(t, err)
		})

		finalCP, err := store.GetByRequestID(requestID)
		assert.NoError(t, err)
		assert.NotNil(t, finalCP)
		assert.Equal(t, numGoroutines, finalCP.FailureCount, "The final failure count should equal the number of concurrent updates.")
	})

	t.Run("ConcurrentMoveToCheckpointDLQ_HighContention", func(t *testing.T) {
		cleanupTables(t, pgxPool)
		numGoroutines := 100
		requestID := "concurrent-dlq-prod-req"

		cpTemplate := *testCP
		cpTemplate.RequestID = requestID
		assert.NoError(t, store.Create(&cpTemplate))
		assertRowCount(t, pgxPool, checkpointTable, 1)
		assertRowCount(t, pgxPool, checkpointDLQTable, 0)

		var successfulMoves int32
		concurrent(t, numGoroutines, func(id int) {
			err := store.MoveToCheckpointDLQ(requestID, fmt.Sprintf("final error %d", id))
			if err == nil {
				atomic.AddInt32(&successfulMoves, 1)
			} else {
				assert.ErrorIs(t, err, ErrCheckpointNotFound, "Unexpected error for failed moves")
			}
		})

		assert.Equal(t, int32(1), successfulMoves, "Exactly one MoveToCheckpointDLQ should succeed")
		assertRowCount(t, pgxPool, checkpointTable, 0)
		assertRowCount(t, pgxPool, checkpointDLQTable, 1)

		query := fmt.Sprintf("SELECT original_request_id, last_error FROM %s WHERE request_id = $1;", checkpointDLQTable)
		row := pgxPool.QueryRow(context.Background(), query, requestID)
		var originalRequestID, lastError string
		assert.NoError(t, row.Scan(&originalRequestID, &lastError))
		assert.Equal(t, requestID, originalRequestID)
		assert.Contains(t, lastError, "final error", "DLQ entry should contain the final error")
	})

	t.Run("MoveToCheckpointDLQ_CrashDuringCommit", func(t *testing.T) {
		cleanupTables(t, pgxPool)
		store := NewTestPostgresStore(t)

		requestID := "crash-commit-dlq-req"
		cp := *testCP
		cp.RequestID = requestID
		assert.NoError(t, store.Create(&cp))
		assertRowCount(t, pgxPool, checkpointTable, 1)
		assertRowCount(t, pgxPool, checkpointDLQTable, 0)

		tx, err := pgxPool.Begin(context.Background())
		assert.NoError(t, err)
		defer func() {
			_ = tx.Rollback(context.Background())
		}()

		insertQuery := fmt.Sprintf(`
			INSERT INTO %s (request_id, original_request_id, graph_id, graph_version, node_id, state_data, failure_count, last_error, last_attempt)
			SELECT request_id, request_id, graph_id, graph_version, node_id, state_data, failure_count, $2, last_attempt
			FROM %s
			WHERE request_id = $1
			ON CONFLICT (request_id) DO NOTHING;`, checkpointDLQTable, checkpointTable)

		_, err = tx.Exec(context.Background(), insertQuery, requestID, "simulated crash error")
		assert.NoError(t, err, "Insert into DLQ should succeed within transaction")

		assert.NoError(t, tx.Rollback(context.Background()), "Explicit rollback should succeed")

		assertRowCount(t, pgxPool, checkpointTable, 1)
		assertRowCount(t, pgxPool, checkpointDLQTable, 0)
		retrievedCP, err := store.GetByRequestID(requestID)
		assert.NoError(t, err)
		assert.NotNil(t, retrievedCP)
		assert.Equal(t, requestID, retrievedCP.RequestID)
	})
}

func TestAtomicityProduction_MixedOperationContention(t *testing.T) {
	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	requestID := "mixed-op-contention-req"
	cp := *testCP
	cp.RequestID = requestID
	assert.NoError(t, store.Create(&cp))
	assertRowCount(t, pgxPool, checkpointTable, 1)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine A: RecordFailure
	go func() {
		defer wg.Done()
		_, err := store.RecordFailure(requestID, fmt.Errorf("error from A"))
		assert.NoError(t, err)
	}()

	// Goroutine B: MoveToCheckpointDLQ
	go func() {
		defer wg.Done()
		err := store.MoveToCheckpointDLQ(requestID, "dlq from B")
		// One should succeed, the other will get ErrCheckpointNotFound
		assert.True(t, err == nil || errors.Is(err, ErrCheckpointNotFound), "Unexpected error type: %v", err)
	}()
	wg.Wait()

	// Verify final state: exactly one operation should have succeeded for the checkpoint
	assertRowCount(t, pgxPool, checkpointTable, 0)
	assertRowCount(t, pgxPool, checkpointDLQTable, 1)

	// If RecordFailure happened first and then DLQ, failure_count should be 1
	// If DLQ happened first, RecordFailure should have failed with ErrCheckpointNotFound
	// The key is that the final state is consistent and not corrupted.
	_, err := store.GetByRequestID(requestID)
	assert.ErrorIs(t, err, ErrCheckpointNotFound)

	// Check DLQ directly
	query := fmt.Sprintf("SELECT failure_count FROM %s WHERE request_id = $1;", checkpointDLQTable)
	row := pgxPool.QueryRow(context.Background(), query, requestID)
	var finalFailureCount int
	assert.NoError(t, row.Scan(&finalFailureCount))

	// The failure count in DLQ should be 1 if RecordFailure ran before Move, 0 if after.
	// We are asserting consistency, not a specific order.
	assert.True(t, finalFailureCount == 0 || finalFailureCount == 1, "Expected final failure count 0 or 1, got %d", finalFailureCount)
}

func TestAtomicityProduction_WriteSkewAnomaly_Demonstration_ReadCommitted(t *testing.T) {
	// This test is designed to demonstrate a write skew anomaly under PostgreSQL's
	// default READ COMMITTED isolation level. It should FAIL (by detecting inconsistency)
	// when running against READ COMMITTED.
	// This test will be SKIPPED by default, as it's a diagnostic test.
	if os.Getenv("DEMONSTRATE_WRITE_SKEW") != "true" {
		t.Skip("Skipping write skew demonstration (READ COMMITTED) test. Set DEMONSTRATE_WRITE_SKEW=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	graphID := "skew-graph-demo"
	requestID1 := "skew-req-demo-1"
	requestID2 := "skew-req-demo-2"

	// Initial state: One active checkpoint for the graph, failure_count = 0
	cp1 := *testCP
	cp1.RequestID = requestID1
	cp1.GraphID = graphID
	cp1.FailureCount = 0
	assert.NoError(t, store.Create(&cp1))
	assertRowCount(t, pgxPool, checkpointTable, 1)

	// Invariant: Max one active checkpoint per graph_id.
	// Transaction A wants to create a new checkpoint if currently only 1 exists.
	// Transaction B wants to create a new checkpoint if currently only 1 exists.
	// Under Read Committed: Both see 1, both create, resulting in 2. Invariant violated.

	var wg sync.WaitGroup
	wg.Add(2)
	
	// Channels to synchronize the reads before writes
	readDoneA := make(chan struct{})
	readDoneB := make(chan struct{})

	// Goroutine A: Attempts to create checkpoint 2 if only 1 exists
	go func() {
		defer wg.Done()
		tx, err := pgxPool.Begin(context.Background())
		assert.NoError(t, err)
		defer func() { _ = tx.Rollback(context.Background()) }()

		// Read active count (predicate)
		var activeCount int
		queryErr := tx.QueryRow(context.Background(),
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE graph_id = $1;", checkpointTable), graphID).Scan(&activeCount)
		assert.NoError(t, queryErr)
		assert.Equal(t, 1, activeCount, "Txn A: Expected initial active count of 1")

		// Signal that read is complete
		close(readDoneA)
		// Wait for other goroutine to read as well
		<-readDoneB
		
		// Decision based on read: if activeCount is 1, create a new one
		if activeCount == 1 {
			cp2 := *testCP
			cp2.RequestID = requestID2
			cp2.GraphID = graphID
			createErr := store.CreateWithTransaction(&pgxTransaction{tx: tx}, &cp2)
			assert.NoError(t, createErr)
		}

		commitErr := tx.Commit(context.Background())
		assert.NoError(t, commitErr, "Txn A commit should succeed under READ COMMITTED, leading to an anomaly")
	}()

	// Goroutine B: Also attempts to create checkpoint 2 if only 1 exists
	go func() {
		defer wg.Done()
		tx, err := pgxPool.Begin(context.Background())
		assert.NoError(t, err)
		defer func() { _ = tx.Rollback(context.Background()) }()

		// Wait for Goroutine A to read
		<-readDoneA
		
		// Read active count (predicate)
		var activeCount int
		queryErr := tx.QueryRow(context.Background(),
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE graph_id = $1;", checkpointTable), graphID).Scan(&activeCount)
		assert.NoError(t, queryErr)
		assert.Equal(t, 1, activeCount, "Txn B: Expected initial active count of 1")

		// Signal read completion
		close(readDoneB)

		// Decision based on read: if activeCount is 1, create a new one
		if activeCount == 1 {
			cp3 := *testCP
			cp3.RequestID = "skew-req-demo-3"
			cp3.GraphID = graphID
			createErr := store.CreateWithTransaction(&pgxTransaction{tx: tx}, &cp3)
			assert.NoError(t, createErr)
		}

		commitErr := tx.Commit(context.Background())
		assert.NoError(t, commitErr, "Txn B commit should succeed under READ COMMITTED, leading to an anomaly")
	}()
	wg.Wait()

	// Assert the anomaly: After both transactions commit, if READ COMMITTED is in effect,
	// both will have seen 1 active checkpoint initially, and both will have created a new one.
	// This results in 3 active checkpoints, violating the "max one active" invariant.

	finalActiveCount := 0
	finalQueryErr := pgxPool.QueryRow(context.Background(),
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE graph_id = $1;", checkpointTable), graphID).Scan(&finalActiveCount)
	assert.NoError(t, finalQueryErr)
	
	assert.Equal(t, 3, finalActiveCount, "Expected 3 active checkpoints (anomaly) under READ COMMITTED")

	t.Logf("Write Skew Anomaly Demonstrated: Final active checkpoints for %s: %d (Expected 1)", graphID, finalActiveCount)
	t.FailNow() // Explicitly fail this test to highlight the anomaly for READ COMMITTED
}

func TestAtomicityProduction_WriteSkewAnomaly_Serializable(t *testing.T) {
	// This test verifies that SERIALIZABLE isolation prevents the write skew anomaly.
	// It should PASS, with one transaction correctly failing due to a serialization error.
	if os.Getenv("DEMONSTRATE_WRITE_SKEW") != "true" {
		t.Skip("Skipping write skew SERIALIZABLE test. Set DEMONSTRATE_WRITE_SKEW=true to enable.")
	}

	cleanupTables(t, pgxPool)
	store := NewTestPostgresStore(t)

	graphID := "skew-graph-serializable"
	requestID1 := "skew-req-serializable-1"
	requestID2 := "skew-req-serializable-2"

	// Initial state: One active checkpoint for the graph, failure_count = 0
	cp1 := *testCP
	cp1.RequestID = requestID1
	cp1.GraphID = graphID
	cp1.FailureCount = 0
	assert.NoError(t, store.Create(&cp1))
	assertRowCount(t, pgxPool, checkpointTable, 1)

	var wg sync.WaitGroup
	wg.Add(2)

	// Channels to synchronize the reads before writes
	readDoneA := make(chan struct{})
	readDoneB := make(chan struct{})

	var txAError, txBError error
	var committedATransactions, committedBTransactions atomic.Int32

	// Goroutine A: Attempts to create checkpoint 2 if only 1 exists
	go func() {
		defer wg.Done()
		tx, err := store.BeginSerializableTransaction(context.Background()) // Use serializable transaction
		assert.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		// Read active count (predicate)
		var activeCount int
		        queryErr := tx.QueryRow(context.Background(), // Read from transaction
		            fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE graph_id = $1;", checkpointTable), graphID).Scan(&activeCount)
		        assert.NoError(t, queryErr)
		        assert.Equal(t, 1, activeCount, "Txn A: Expected initial active count of 1")
		// Signal that read is complete
		close(readDoneA)
		// Wait for other goroutine to read as well
		<-readDoneB
		
		// Decision based on read: if activeCount is 1, create a new one
		if activeCount == 1 {
			cp2 := *testCP
			cp2.RequestID = requestID2
			cp2.GraphID = graphID
			// CreateWithTransaction expects our custom Transaction interface, which we have.
			createErr := store.CreateWithTransaction(tx, &cp2)
			if createErr != nil {
				txAError = createErr
				return
			}
		}

		txAError = tx.Commit()
		if txAError == nil {
			committedATransactions.Add(1)
		}
	}()

	// Goroutine B: Also attempts to create checkpoint 2 if only 1 exists
	go func() {
		defer wg.Done()
		tx, err := store.BeginSerializableTransaction(context.Background()) // Use serializable transaction
		assert.NoError(t, err)
		defer func() { _ = tx.Rollback() }()

		// Wait for Goroutine A to read
		<-readDoneA
		
		// Read active count (predicate)
		var activeCount int
		queryErr := tx.QueryRow(context.Background(), // Read from transaction
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE graph_id = $1;", checkpointTable), graphID).Scan(&activeCount)
		assert.NoError(t, queryErr)
		assert.Equal(t, 1, activeCount, "Txn B: Expected initial active count of 1")

		// Signal read completion
		close(readDoneB)

		// Decision based on read: if activeCount is 1, create a new one
		if activeCount == 1 {
			cp3 := *testCP
			cp3.RequestID = "skew-req-serializable-3"
			cp3.GraphID = graphID
			createErr := store.CreateWithTransaction(tx, &cp3)
			if createErr != nil {
				txBError = createErr
				return
			}
		}
		
		txBError = tx.Commit()
		if txBError == nil {
			committedBTransactions.Add(1)
		}
	}()
	wg.Wait()

	// Assert that exactly one transaction committed successfully, and the other failed with a serialization error.
	totalCommitted := committedATransactions.Load() + committedBTransactions.Load()
	assert.Equal(t, int32(1), totalCommitted, "Expected exactly one transaction to commit successfully under SERIALIZABLE isolation")

	// Check that one transaction committed without error and the other failed with a serialization error
	if committedATransactions.Load() == 1 {
		assert.True(t, txBError != nil && isSerializationError(txBError), "Expected Txn B to fail with serialization error, got: %v", txBError)
		assert.NoError(t, txAError, "Expected Txn A to commit successfully, got: %v", txAError)
	} else {
		assert.True(t, txAError != nil && isSerializationError(txAError), "Expected Txn A to fail with serialization error, got: %v", txAError)
		assert.NoError(t, txBError, "Expected Txn B to commit successfully, got: %v", txBError)
	}

	// Verify the final state: only 2 checkpoints should exist (original + 1 new from the successful transaction)
	finalActiveCount := 0
	finalQueryErr := pgxPool.QueryRow(context.Background(),
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE graph_id = $1;", checkpointTable), graphID).Scan(&finalActiveCount)
	assert.NoError(t, finalQueryErr)
	
	assert.Equal(t, 2, finalActiveCount, "Expected 2 active checkpoints (original + 1 new) under SERIALIZABLE isolation")

	t.Logf("Write Skew Anomaly Prevented: Final active checkpoints for %s: %d (Expected 2)", graphID, finalActiveCount)
}
