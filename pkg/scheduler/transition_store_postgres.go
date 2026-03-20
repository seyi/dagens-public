package scheduler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	jobTransitionsTable = "scheduler_job_transitions"
	durableJobsTable    = "scheduler_durable_jobs"
	durableTasksTable   = "scheduler_durable_tasks"
	jobSequencesTable   = "scheduler_job_sequences"
)

// PostgresTransitionStore persists scheduler lifecycle transitions and
// materialized job/task views in PostgreSQL.
type PostgresTransitionStore struct {
	pool *pgxpool.Pool
}

// NewPostgresTransitionStore creates a transition store backed by an existing
// pgx pool and ensures required tables/indexes exist.
func NewPostgresTransitionStore(ctx context.Context, pool *pgxpool.Pool) (*PostgresTransitionStore, error) {
	store := &PostgresTransitionStore{pool: pool}
	if err := store.ensureSchema(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// NewPostgresTransitionStoreFromURL creates a pgx pool from the provided DSN
// and returns a transition store.
func NewPostgresTransitionStoreFromURL(ctx context.Context, dsn string) (*PostgresTransitionStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create transition store pool: %w", err)
	}
	store, err := NewPostgresTransitionStore(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresTransitionStore) ensureSchema(ctx context.Context) error {
	ddl := []string{
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				job_id TEXT NOT NULL,
				sequence_id BIGINT NOT NULL,
				entity_type TEXT NOT NULL,
				transition TEXT NOT NULL,
				task_id TEXT NOT NULL DEFAULT '',
				previous_state TEXT NOT NULL DEFAULT '',
				new_state TEXT NOT NULL,
				node_id TEXT NOT NULL DEFAULT '',
				attempt INTEGER NOT NULL DEFAULT 0,
				error_summary TEXT NOT NULL DEFAULT '',
				occurred_at TIMESTAMPTZ NOT NULL,
				PRIMARY KEY (job_id, sequence_id)
			);`, jobTransitionsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_occurred_at ON %s (occurred_at);`, jobTransitionsTable, jobTransitionsTable),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				job_id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				current_state TEXT NOT NULL,
				last_sequence_id BIGINT NOT NULL DEFAULT 0,
				created_at TIMESTAMPTZ NOT NULL,
				updated_at TIMESTAMPTZ NOT NULL
			);`, durableJobsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS last_sequence_id BIGINT NOT NULL DEFAULT 0;`, durableJobsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_unfinished_created_at ON %s (created_at, job_id) WHERE current_state NOT IN ('SUCCEEDED','FAILED','CANCELED');`, durableJobsTable, durableJobsTable),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				task_id TEXT PRIMARY KEY,
				job_id TEXT NOT NULL,
				stage_id TEXT NOT NULL DEFAULT '',
				node_id TEXT NOT NULL DEFAULT '',
				agent_id TEXT NOT NULL DEFAULT '',
				agent_name TEXT NOT NULL DEFAULT '',
				input_json TEXT NOT NULL DEFAULT '',
				partition_key TEXT NOT NULL DEFAULT '',
				current_state TEXT NOT NULL,
				last_attempt INTEGER NOT NULL DEFAULT 0,
				updated_at TIMESTAMPTZ NOT NULL
			);`, durableTasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS agent_id TEXT NOT NULL DEFAULT '';`, durableTasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS agent_name TEXT NOT NULL DEFAULT '';`, durableTasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS input_json TEXT NOT NULL DEFAULT '';`, durableTasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS partition_key TEXT NOT NULL DEFAULT '';`, durableTasksTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_job_id ON %s (job_id);`, durableTasksTable, durableTasksTable),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				job_id TEXT PRIMARY KEY,
				last_sequence_id BIGINT NOT NULL DEFAULT 0,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);`, jobSequencesTable),
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ensure transition store schema begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Serialize schema initialization across concurrent control-plane startups.
	// The lock key is deterministic and local to this deployment.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(743209155197540836)`); err != nil {
		return fmt.Errorf("ensure transition store schema advisory lock: %w", err)
	}

	for _, stmt := range ddl {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("ensure transition store schema: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ensure transition store schema commit: %w", err)
	}
	return nil
}

// NextSequenceID returns the next durable sequence ID for a given job.
// Sequence IDs are monotonic per job across process restarts.
func (s *PostgresTransitionStore) NextSequenceID(ctx context.Context, jobID string) (uint64, error) {
	if jobID == "" {
		return 0, fmt.Errorf("job_id is required")
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (job_id, last_sequence_id, updated_at)
		VALUES (
			$1,
			GREATEST(
				COALESCE((SELECT last_sequence_id FROM %s WHERE job_id = $1), 0),
				COALESCE((SELECT MAX(sequence_id) FROM %s WHERE job_id = $1), 0)
			) + 1,
			NOW()
		)
		ON CONFLICT (job_id) DO UPDATE SET
			last_sequence_id = %s.last_sequence_id + 1,
			updated_at = NOW()
		RETURNING last_sequence_id;`, jobSequencesTable, durableJobsTable, jobTransitionsTable, jobSequencesTable)

	var seq int64
	if err := s.pool.QueryRow(ctx, query, jobID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("next durable sequence id: %w", err)
	}
	return uint64(seq), nil
}

// Close releases the underlying pool.
func (s *PostgresTransitionStore) Close() {
	s.pool.Close()
}

// WithTx executes transition writes atomically using a single SQL transaction.
func (s *PostgresTransitionStore) WithTx(ctx context.Context, fn func(tx TransitionStoreTx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transition transaction: %w", err)
	}

	txStore := &postgresTransitionStoreTx{tx: tx}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("commit transition transaction: %w", err)
	}
	return nil
}

func (s *PostgresTransitionStore) AppendTransition(ctx context.Context, record TransitionRecord) error {
	txStore := &postgresTransitionStoreTx{tx: s.pool}
	return txStore.AppendTransition(ctx, record)
}

func (s *PostgresTransitionStore) UpsertJob(ctx context.Context, job DurableJobRecord) error {
	txStore := &postgresTransitionStoreTx{tx: s.pool}
	return txStore.UpsertJob(ctx, job)
}

func (s *PostgresTransitionStore) UpsertTask(ctx context.Context, task DurableTaskRecord) error {
	txStore := &postgresTransitionStoreTx{tx: s.pool}
	return txStore.UpsertTask(ctx, task)
}

type postgresExecer interface {
	Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error)
}

type postgresTransitionStoreTx struct {
	tx postgresExecer
}

func (p *postgresTransitionStoreTx) AppendTransition(ctx context.Context, record TransitionRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (
			job_id, sequence_id, entity_type, transition, task_id, previous_state,
			new_state, node_id, attempt, error_summary, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11);`, jobTransitionsTable)

	_, err := p.tx.Exec(ctx, query,
		record.JobID,
		int64(record.SequenceID),
		string(record.EntityType),
		string(record.Transition),
		record.TaskID,
		record.PreviousState,
		record.NewState,
		record.NodeID,
		record.Attempt,
		record.ErrorSummary,
		record.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("append transition: %w", err)
	}

	// Keep materialized job sequence metadata in sync when a job record exists.
	updateJobSeq := fmt.Sprintf(`
		UPDATE %s
		SET last_sequence_id = GREATEST(last_sequence_id, $2)
		WHERE job_id = $1;`, durableJobsTable)
	if _, err := p.tx.Exec(ctx, updateJobSeq, record.JobID, int64(record.SequenceID)); err != nil {
		return fmt.Errorf("update job last_sequence_id: %w", err)
	}
	return nil
}

func (p *postgresTransitionStoreTx) UpsertJob(ctx context.Context, job DurableJobRecord) error {
	query := fmt.Sprintf(`
		INSERT INTO %s (job_id, name, current_state, last_sequence_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (job_id) DO UPDATE SET
			name = EXCLUDED.name,
			current_state = EXCLUDED.current_state,
			last_sequence_id = GREATEST(%s.last_sequence_id, EXCLUDED.last_sequence_id),
			updated_at = EXCLUDED.updated_at;`, durableJobsTable, durableJobsTable)

	_, err := p.tx.Exec(ctx, query,
		job.JobID,
		job.Name,
		string(job.CurrentState),
		int64(job.LastSequenceID),
		job.CreatedAt,
		job.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert job: %w", err)
	}
	return nil
}

func (p *postgresTransitionStoreTx) UpsertTask(ctx context.Context, task DurableTaskRecord) error {
	query := fmt.Sprintf(`
		INSERT INTO %s (task_id, job_id, stage_id, node_id, agent_id, agent_name, input_json, partition_key, current_state, last_attempt, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (task_id) DO UPDATE SET
			job_id = EXCLUDED.job_id,
			stage_id = EXCLUDED.stage_id,
			node_id = EXCLUDED.node_id,
			agent_id = EXCLUDED.agent_id,
			agent_name = EXCLUDED.agent_name,
			input_json = EXCLUDED.input_json,
			partition_key = EXCLUDED.partition_key,
			current_state = EXCLUDED.current_state,
			last_attempt = EXCLUDED.last_attempt,
			updated_at = EXCLUDED.updated_at;`, durableTasksTable)

	_, err := p.tx.Exec(ctx, query,
		task.TaskID,
		task.JobID,
		task.StageID,
		task.NodeID,
		task.AgentID,
		task.AgentName,
		task.InputJSON,
		task.PartitionKey,
		string(task.CurrentState),
		task.LastAttempt,
		task.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert task: %w", err)
	}
	return nil
}

func (p *postgresTransitionStoreTx) ClaimTaskDispatch(ctx context.Context, claim TaskDispatchClaim) (bool, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s (task_id, job_id, stage_id, node_id, current_state, last_attempt, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (task_id) DO UPDATE SET
			job_id = EXCLUDED.job_id,
			stage_id = EXCLUDED.stage_id,
			node_id = EXCLUDED.node_id,
			current_state = EXCLUDED.current_state,
			last_attempt = EXCLUDED.last_attempt,
			updated_at = EXCLUDED.updated_at
		WHERE %s.last_attempt < EXCLUDED.last_attempt
		  AND %s.current_state NOT IN ($8, $9);`, durableTasksTable, durableTasksTable, durableTasksTable)

	tag, err := p.tx.Exec(ctx, query,
		claim.TaskID,
		claim.JobID,
		claim.StageID,
		claim.NodeID,
		string(TaskStateDispatched),
		claim.Attempt,
		claim.UpdatedAt,
		string(TaskStateRunning),
		string(TaskStateSucceeded),
	)
	if err != nil {
		return false, fmt.Errorf("claim task dispatch: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *PostgresTransitionStore) ListUnfinishedJobs(ctx context.Context) ([]DurableJobRecord, error) {
	query := fmt.Sprintf(`
		SELECT job_id, name, current_state, last_sequence_id, created_at, updated_at
		FROM %s
		WHERE current_state NOT IN ('SUCCEEDED','FAILED','CANCELED')
		ORDER BY created_at ASC, job_id ASC;`, durableJobsTable)

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list unfinished jobs: %w", err)
	}
	defer rows.Close()

	out := make([]DurableJobRecord, 0)
	for rows.Next() {
		var record DurableJobRecord
		var state string
		var lastSeq int64
		if err := rows.Scan(&record.JobID, &record.Name, &state, &lastSeq, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan unfinished job: %w", err)
		}
		record.CurrentState = JobLifecycleState(state)
		record.LastSequenceID = uint64(lastSeq)
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unfinished jobs: %w", err)
	}
	return out, nil
}

func (s *PostgresTransitionStore) ListTransitionsByJob(ctx context.Context, jobID string) ([]TransitionRecord, error) {
	query := fmt.Sprintf(`
		SELECT sequence_id, entity_type, transition, job_id, task_id, previous_state,
		       new_state, node_id, attempt, error_summary, occurred_at
		FROM %s
		WHERE job_id = $1
		ORDER BY sequence_id ASC;`, jobTransitionsTable)

	rows, err := s.pool.Query(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("list transitions by job: %w", err)
	}
	defer rows.Close()

	out := make([]TransitionRecord, 0)
	for rows.Next() {
		var record TransitionRecord
		var seq int64
		var entityType string
		var transition string
		if err := rows.Scan(
			&seq,
			&entityType,
			&transition,
			&record.JobID,
			&record.TaskID,
			&record.PreviousState,
			&record.NewState,
			&record.NodeID,
			&record.Attempt,
			&record.ErrorSummary,
			&record.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("scan transition: %w", err)
		}
		record.SequenceID = uint64(seq)
		record.EntityType = TransitionEntityType(entityType)
		record.Transition = TransitionType(transition)
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transitions by job: %w", err)
	}
	return out, nil
}

func (s *PostgresTransitionStore) ListTransitionsByJobs(ctx context.Context, jobIDs []string) (map[string][]TransitionRecord, error) {
	result := make(map[string][]TransitionRecord, len(jobIDs))
	if len(jobIDs) == 0 {
		return result, nil
	}
	for _, jobID := range jobIDs {
		result[jobID] = []TransitionRecord{}
	}

	query := fmt.Sprintf(`
		SELECT sequence_id, entity_type, transition, job_id, task_id, previous_state,
		       new_state, node_id, attempt, error_summary, occurred_at
		FROM %s
		WHERE job_id = ANY($1)
		ORDER BY job_id ASC, sequence_id ASC;`, jobTransitionsTable)

	rows, err := s.pool.Query(ctx, query, jobIDs)
	if err != nil {
		return nil, fmt.Errorf("list transitions by jobs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var record TransitionRecord
		var seq int64
		var entityType string
		var transition string
		if err := rows.Scan(
			&seq,
			&entityType,
			&transition,
			&record.JobID,
			&record.TaskID,
			&record.PreviousState,
			&record.NewState,
			&record.NodeID,
			&record.Attempt,
			&record.ErrorSummary,
			&record.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("scan transition by jobs: %w", err)
		}
		record.SequenceID = uint64(seq)
		record.EntityType = TransitionEntityType(entityType)
		record.Transition = TransitionType(transition)
		result[record.JobID] = append(result[record.JobID], record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transitions by jobs: %w", err)
	}
	return result, nil
}

func (s *PostgresTransitionStore) ListTasksByJob(ctx context.Context, jobID string) ([]DurableTaskRecord, error) {
	query := fmt.Sprintf(`
		SELECT task_id, job_id, stage_id, node_id, agent_id, agent_name, input_json, partition_key, current_state, last_attempt, updated_at
		FROM %s
		WHERE job_id = $1
		ORDER BY task_id ASC;`, durableTasksTable)

	rows, err := s.pool.Query(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("list tasks by job: %w", err)
	}
	defer rows.Close()

	out := make([]DurableTaskRecord, 0)
	for rows.Next() {
		var task DurableTaskRecord
		var state string
		if err := rows.Scan(
			&task.TaskID,
			&task.JobID,
			&task.StageID,
			&task.NodeID,
			&task.AgentID,
			&task.AgentName,
			&task.InputJSON,
			&task.PartitionKey,
			&state,
			&task.LastAttempt,
			&task.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan task by job: %w", err)
		}
		task.CurrentState = TaskLifecycleState(state)
		out = append(out, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks by job: %w", err)
	}
	return out, nil
}

func (s *PostgresTransitionStore) ListTasksByJobs(ctx context.Context, jobIDs []string) (map[string][]DurableTaskRecord, error) {
	result := make(map[string][]DurableTaskRecord, len(jobIDs))
	if len(jobIDs) == 0 {
		return result, nil
	}
	for _, jobID := range jobIDs {
		result[jobID] = []DurableTaskRecord{}
	}

	query := fmt.Sprintf(`
		SELECT task_id, job_id, stage_id, node_id, agent_id, agent_name, input_json, partition_key, current_state, last_attempt, updated_at
		FROM %s
		WHERE job_id = ANY($1)
		ORDER BY job_id ASC, task_id ASC;`, durableTasksTable)

	rows, err := s.pool.Query(ctx, query, jobIDs)
	if err != nil {
		return nil, fmt.Errorf("list tasks by jobs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var task DurableTaskRecord
		var state string
		if err := rows.Scan(
			&task.TaskID,
			&task.JobID,
			&task.StageID,
			&task.NodeID,
			&task.AgentID,
			&task.AgentName,
			&task.InputJSON,
			&task.PartitionKey,
			&state,
			&task.LastAttempt,
			&task.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan task by jobs: %w", err)
		}
		task.CurrentState = TaskLifecycleState(state)
		result[task.JobID] = append(result[task.JobID], task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks by jobs: %w", err)
	}
	return result, nil
}
