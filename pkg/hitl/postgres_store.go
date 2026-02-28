package hitl

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Serialization retry configuration
const (
	// DefaultMaxSerializationRetries is the default number of retries for serialization failures
	DefaultMaxSerializationRetries = 3
	// DefaultBaseBackoff is the base delay for exponential backoff
	DefaultBaseBackoff = 10 * time.Millisecond
	// DefaultMaxBackoff caps the maximum backoff delay
	DefaultMaxBackoff = 500 * time.Millisecond
)

// SerializationConfig controls retry behavior for serializable transactions
type SerializationConfig struct {
	MaxRetries  int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// DefaultSerializationConfig returns sensible defaults for serialization retry
func DefaultSerializationConfig() SerializationConfig {
	return SerializationConfig{
		MaxRetries:  DefaultMaxSerializationRetries,
		BaseBackoff: DefaultBaseBackoff,
		MaxBackoff:  DefaultMaxBackoff,
	}
}

// IsSerializationError checks if an error is a PostgreSQL serialization failure (SQLSTATE 40001)
func IsSerializationError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.SQLState() == "40001"
}

// IsDeadlockError checks if an error is a PostgreSQL deadlock (SQLSTATE 40P01)
func IsDeadlockError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.SQLState() == "40P01"
}

// IsRetryableError returns true if the error is a serialization or deadlock error
func IsRetryableError(err error) bool {
	return IsSerializationError(err) || IsDeadlockError(err)
}

// calculateBackoff returns the backoff duration for a given attempt with jitter
func calculateBackoff(attempt int, config SerializationConfig) time.Duration {
	// Exponential backoff: base * 2^attempt
	backoff := config.BaseBackoff * time.Duration(1<<uint(attempt))
	if backoff > config.MaxBackoff {
		backoff = config.MaxBackoff
	}
	// Add jitter (±25%)
	jitter := time.Duration(float64(backoff) * (0.75 + rand.Float64()*0.5))
	return jitter
}

// PostgresCheckpointStore implements CheckpointStore using PostgreSQL for durability.
// Tables are created automatically if they do not exist.
type PostgresCheckpointStore struct {
	pool *pgxpool.Pool
}

const (
	checkpointTable    = "hitl_execution_checkpoints"
	checkpointDLQTable = "hitl_checkpoint_dlq"
)

// NewPostgresCheckpointStore creates a store backed by an existing pgx pool and
// ensures the necessary tables exist.
func NewPostgresCheckpointStore(ctx context.Context, pool *pgxpool.Pool) (*PostgresCheckpointStore, error) {
	store := &PostgresCheckpointStore{pool: pool}
	if err := store.ensureSchema(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// NewPostgresCheckpointStoreFromURL is a convenience constructor that creates
// a new connection pool from the provided Postgres connection string.
func NewPostgresCheckpointStoreFromURL(ctx context.Context, connString string) (*PostgresCheckpointStore, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	return NewPostgresCheckpointStore(ctx, pool)
}

func (s *PostgresCheckpointStore) ensureSchema(ctx context.Context) error {
	ddl := []string{
		fmt.Sprintf(`
            CREATE TABLE IF NOT EXISTS %s (
                request_id TEXT PRIMARY KEY,
                graph_id TEXT NOT NULL,
                graph_version TEXT NOT NULL,
                node_id TEXT NOT NULL,
                state_data BYTEA NOT NULL,
                created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                expires_at TIMESTAMPTZ,
                trace_id TEXT,
                failure_count INTEGER NOT NULL DEFAULT 0,
                last_error TEXT,
                last_attempt TIMESTAMPTZ
            );`, checkpointTable),
		fmt.Sprintf(`
            CREATE INDEX IF NOT EXISTS idx_%s_created_at ON %s (created_at);`, checkpointTable, checkpointTable),
		fmt.Sprintf(`
            CREATE TABLE IF NOT EXISTS %s (
                request_id TEXT PRIMARY KEY,
                original_request_id TEXT NOT NULL,
                graph_id TEXT NOT NULL,
                graph_version TEXT NOT NULL,
                node_id TEXT NOT NULL,
                state_data BYTEA NOT NULL,
                failure_count INTEGER NOT NULL,
                last_error TEXT NOT NULL,
                last_attempt TIMESTAMPTZ,
                moved_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
            );`, checkpointDLQTable),
	}

	for _, stmt := range ddl {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("ensure schema: %w", err)
		}
	}
	return nil
}

// pgxTransaction adapts pgx.Tx to the Transaction interface.
type pgxTransaction struct {
	tx pgx.Tx
}

func (t *pgxTransaction) Commit() error   { return t.tx.Commit(context.Background()) }
func (t *pgxTransaction) Rollback() error { return t.tx.Rollback(context.Background()) }
func (t *pgxTransaction) QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row {
	return t.tx.QueryRow(ctx, query, args...)
}

// BeginTransaction exposes a typed transaction helper for callers that need
// to coordinate multiple operations atomically.
func (s *PostgresCheckpointStore) BeginTransaction(ctx context.Context) (Transaction, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return &pgxTransaction{tx: tx}, nil
}

// BeginSerializableTransaction exposes a typed transaction helper for callers that need
// to coordinate multiple operations atomically with SERIALIZABLE isolation.
func (s *PostgresCheckpointStore) BeginSerializableTransaction(ctx context.Context) (Transaction, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin serializable transaction: %w", err)
	}
	return &pgxTransaction{tx: tx}, nil
}

// SerializableOperation represents an operation to be executed within a serializable transaction.
// The function receives a transaction and should return an error if the operation fails.
// The operation will be retried on serialization failures.
type SerializableOperation func(ctx context.Context, tx pgx.Tx) error

// WithSerializableRetry executes an operation in a SERIALIZABLE transaction with automatic
// retry on serialization failures. This is the recommended pattern for operations that
// enforce logical invariants based on reads.
//
// IMPORTANT: The provided SerializableOperation function MUST be idempotent, as it may be
// executed multiple times if serialization failures occur.
func (s *PostgresCheckpointStore) WithSerializableRetry(ctx context.Context, config SerializationConfig, op SerializableOperation) error {
	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		// Before retrying, check if the context has been canceled.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 0 {
			// Backoff before retry
			backoff := calculateBackoff(attempt-1, config)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return fmt.Errorf("begin serializable transaction: %w", err)
		}

		err = op(ctx, tx)
		if err != nil {
			_ = tx.Rollback(ctx)
			if IsRetryableError(err) {
				lastErr = err
				continue // Retry
			}
			return err // Non-retryable error
		}

		err = tx.Commit(ctx)
		if err != nil {
			if IsRetryableError(err) {
				lastErr = err
				continue // Retry
			}
			return fmt.Errorf("commit serializable transaction: %w", err)
		}

		return nil // Success
	}

	return fmt.Errorf("%w: %w", ErrMaxRetriesExceeded, lastErr)
}

// ErrMaxRetriesExceeded is returned when serialization retries are exhausted
var ErrMaxRetriesExceeded = errors.New("max serialization retries exceeded")

// CreateWithTransaction inserts a checkpoint using the provided transaction.
func (s *PostgresCheckpointStore) CreateWithTransaction(tx Transaction, cp *ExecutionCheckpoint) error {
	pgxTx, ok := tx.(*pgxTransaction)
	if !ok {
		return fmt.Errorf("unsupported transaction type %T", tx)
	}
	return s.insertCheckpoint(context.Background(), pgxTx.tx, cp)
}

// Create inserts a checkpoint using the store's connection pool.
func (s *PostgresCheckpointStore) Create(cp *ExecutionCheckpoint) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.insertCheckpoint(ctx, s.pool, cp)
}

func (s *PostgresCheckpointStore) insertCheckpoint(ctx context.Context, q interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
}, cp *ExecutionCheckpoint) error {
	query := fmt.Sprintf(`
        INSERT INTO %s (request_id, graph_id, graph_version, node_id, state_data, created_at, expires_at, trace_id, failure_count, last_error, last_attempt)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
        ON CONFLICT (request_id) DO NOTHING;`, checkpointTable)

	if _, err := q.Exec(ctx, query,
		cp.RequestID,
		cp.GraphID,
		cp.GraphVersion,
		cp.NodeID,
		cp.StateData,
		cp.CreatedAt,
		cp.ExpiresAt,
		cp.TraceID,
		cp.FailureCount,
		cp.LastError,
		nullableTime(cp.LastAttempt),
	); err != nil {
		return fmt.Errorf("insert checkpoint: %w", err)
	}
	return nil
}

// GetByRequestID retrieves a checkpoint by request ID.
func (s *PostgresCheckpointStore) GetByRequestID(requestID string) (*ExecutionCheckpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(`
        SELECT graph_id, graph_version, node_id, state_data, created_at, expires_at, trace_id, failure_count, last_error, last_attempt
        FROM %s WHERE request_id = $1;`, checkpointTable)

	row := s.pool.QueryRow(ctx, query, requestID)

	var cp ExecutionCheckpoint
	cp.RequestID = requestID
	var lastAttempt *time.Time

	if err := row.Scan(
		&cp.GraphID,
		&cp.GraphVersion,
		&cp.NodeID,
		&cp.StateData,
		&cp.CreatedAt,
		&cp.ExpiresAt,
		&cp.TraceID,
		&cp.FailureCount,
		&cp.LastError,
		&lastAttempt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrCheckpointNotFound
		}
		return nil, fmt.Errorf("get checkpoint: %w", err)
	}

	if lastAttempt != nil {
		cp.LastAttempt = *lastAttempt
	}

	return &cp, nil
}

// Delete removes a checkpoint.
func (s *PostgresCheckpointStore) Delete(requestID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(`DELETE FROM %s WHERE request_id = $1;`, checkpointTable)
	if _, err := s.pool.Exec(ctx, query, requestID); err != nil {
		return fmt.Errorf("delete checkpoint: %w", err)
	}
	return nil
}

// ListOrphaned returns checkpoints older than the provided duration.
func (s *PostgresCheckpointStore) ListOrphaned(olderThan time.Duration) ([]*ExecutionCheckpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cutoff := time.Now().Add(-olderThan)
	query := fmt.Sprintf(`
        SELECT request_id, graph_id, graph_version, node_id, state_data, created_at, expires_at, trace_id, failure_count, last_error, last_attempt
        FROM %s
        WHERE created_at < $1;`, checkpointTable)

	rows, err := s.pool.Query(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list orphaned: %w", err)
	}
	defer rows.Close()

	var results []*ExecutionCheckpoint
	for rows.Next() {
		var cp ExecutionCheckpoint
		var lastAttempt *time.Time
		if err := rows.Scan(
			&cp.RequestID,
			&cp.GraphID,
			&cp.GraphVersion,
			&cp.NodeID,
			&cp.StateData,
			&cp.CreatedAt,
			&cp.ExpiresAt,
			&cp.TraceID,
			&cp.FailureCount,
			&cp.LastError,
			&lastAttempt,
		); err != nil {
			return nil, fmt.Errorf("scan orphaned: %w", err)
		}
		if lastAttempt != nil {
			cp.LastAttempt = *lastAttempt
		}
		results = append(results, &cp)
	}

	return results, nil
}

// RecordFailure increments failure count and captures last error/attempt timestamp.
func (s *PostgresCheckpointStore) RecordFailure(requestID string, err error) (*ExecutionCheckpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(`
        UPDATE %s
        SET failure_count = failure_count + 1,
            last_error = $2,
            last_attempt = NOW()
        WHERE request_id = $1
        RETURNING graph_id, graph_version, node_id, state_data, created_at, expires_at, trace_id, failure_count, last_error, last_attempt;`, checkpointTable)

	row := s.pool.QueryRow(ctx, query, requestID, err.Error())

	var cp ExecutionCheckpoint
	cp.RequestID = requestID
	if scanErr := row.Scan(
		&cp.GraphID,
		&cp.GraphVersion,
		&cp.NodeID,
		&cp.StateData,
		&cp.CreatedAt,
		&cp.ExpiresAt,
		&cp.TraceID,
		&cp.FailureCount,
		&cp.LastError,
		&cp.LastAttempt,
	); scanErr != nil {
		if scanErr == pgx.ErrNoRows {
			return nil, ErrCheckpointNotFound
		}
		return nil, fmt.Errorf("record failure: %w", scanErr)
	}

	return &cp, nil
}

// MoveToCheckpointDLQ moves a checkpoint to the DLQ table and deletes it from the active table.
// This uses READ COMMITTED isolation (default). For stronger guarantees, use MoveToCheckpointDLQSerializable.
func (s *PostgresCheckpointStore) MoveToCheckpointDLQ(requestID string, finalError string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin dlq transaction: %w", err)
	}
	defer tx.Rollback(ctx) // safe if already committed

	insertQuery := fmt.Sprintf(`
        INSERT INTO %s (request_id, original_request_id, graph_id, graph_version, node_id, state_data, failure_count, last_error, last_attempt)
        SELECT request_id, request_id, graph_id, graph_version, node_id, state_data, failure_count, $2, last_attempt
        FROM %s
        WHERE request_id = $1
        ON CONFLICT (request_id) DO NOTHING;`, checkpointDLQTable, checkpointTable)

	tag, err := tx.Exec(ctx, insertQuery, requestID, finalError)
	if err != nil {
		return fmt.Errorf("insert into dlq: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCheckpointNotFound
	}

	deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE request_id = $1;`, checkpointTable)
	if _, err := tx.Exec(ctx, deleteQuery, requestID); err != nil {
		return fmt.Errorf("delete after dlq insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit dlq transaction: %w", err)
	}
	return nil
}

// MoveToCheckpointDLQSerializable moves a checkpoint to the DLQ table using SERIALIZABLE
// isolation with automatic retry on serialization failures. This provides stronger guarantees
// for concurrent access scenarios.
func (s *PostgresCheckpointStore) MoveToCheckpointDLQSerializable(requestID string, finalError string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := DefaultSerializationConfig()

	return s.WithSerializableRetry(ctx, config, func(ctx context.Context, tx pgx.Tx) error {
		// Use SELECT FOR UPDATE to lock the row and prevent concurrent modifications
		selectQuery := fmt.Sprintf(`
			SELECT request_id FROM %s WHERE request_id = $1 FOR UPDATE;`, checkpointTable)

		var foundID string
		err := tx.QueryRow(ctx, selectQuery, requestID).Scan(&foundID)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrCheckpointNotFound
			}
			return fmt.Errorf("select for update: %w", err)
		}

		insertQuery := fmt.Sprintf(`
			INSERT INTO %s (request_id, original_request_id, graph_id, graph_version, node_id, state_data, failure_count, last_error, last_attempt)
			SELECT request_id, request_id, graph_id, graph_version, node_id, state_data, failure_count, $2, last_attempt
			FROM %s
			WHERE request_id = $1
			ON CONFLICT (request_id) DO NOTHING;`, checkpointDLQTable, checkpointTable)

		tag, err := tx.Exec(ctx, insertQuery, requestID, finalError)
		if err != nil {
			return fmt.Errorf("insert into dlq: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrCheckpointNotFound
		}

		deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE request_id = $1;`, checkpointTable)
		if _, err := tx.Exec(ctx, deleteQuery, requestID); err != nil {
			return fmt.Errorf("delete after dlq insert: %w", err)
		}

		return nil
	})
}

// CreateOrUpdateSerializable creates a checkpoint if it doesn't exist, or returns an error
// if it does. Uses SERIALIZABLE isolation to prevent race conditions in "check-then-act" patterns.
func (s *PostgresCheckpointStore) CreateOrUpdateSerializable(cp *ExecutionCheckpoint, invariantCheck func(existing *ExecutionCheckpoint) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := DefaultSerializationConfig()

	return s.WithSerializableRetry(ctx, config, func(ctx context.Context, tx pgx.Tx) error {
		// Check if checkpoint exists
		selectQuery := fmt.Sprintf(`
			SELECT graph_id, graph_version, node_id, state_data, created_at, expires_at, trace_id, failure_count, last_error, last_attempt
			FROM %s WHERE request_id = $1 FOR UPDATE;`, checkpointTable)

		row := tx.QueryRow(ctx, selectQuery, cp.RequestID)

		var existing ExecutionCheckpoint
		existing.RequestID = cp.RequestID
		var lastAttempt *time.Time

		err := row.Scan(
			&existing.GraphID,
			&existing.GraphVersion,
			&existing.NodeID,
			&existing.StateData,
			&existing.CreatedAt,
			&existing.ExpiresAt,
			&existing.TraceID,
			&existing.FailureCount,
			&existing.LastError,
			&lastAttempt,
		)

		if err == nil {
			// Checkpoint exists - run invariant check
			if lastAttempt != nil {
				existing.LastAttempt = *lastAttempt
			}
			if invariantCheck != nil {
				if checkErr := invariantCheck(&existing); checkErr != nil {
					return checkErr
				}
			}
			// If invariant check passes, we could update here, but for now just succeed
			return nil
		}

		if err != pgx.ErrNoRows {
			return fmt.Errorf("select existing: %w", err)
		}

		// Checkpoint doesn't exist - create it
		insertQuery := fmt.Sprintf(`
			INSERT INTO %s (request_id, graph_id, graph_version, node_id, state_data, created_at, expires_at, trace_id, failure_count, last_error, last_attempt)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (request_id) DO NOTHING;`, checkpointTable)

		_, err = tx.Exec(ctx, insertQuery,
			cp.RequestID,
			cp.GraphID,
			cp.GraphVersion,
			cp.NodeID,
			cp.StateData,
			cp.CreatedAt,
			cp.ExpiresAt,
			cp.TraceID,
			cp.FailureCount,
			cp.LastError,
			nullableTime(cp.LastAttempt),
		)
		if err != nil {
			return fmt.Errorf("insert checkpoint: %w", err)
		}

		return nil
	})
}

func nullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}
