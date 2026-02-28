package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/seyi/dagens/pkg/policy"
)

const (
	// AuditTable is the name of the policy audit table
	AuditTable = "policy_audit_log"
	
	// DefaultInsertTimeout is the default timeout for insert operations
	DefaultInsertTimeout = 5 * time.Second
)

// validTableNameRegex enforces safe table names to prevent SQL injection via interpolation.
// It ensures the name starts with a letter or underscore, followed by alphanumeric characters or underscores.
var validTableNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Ensure PostgresAuditLogger implements policy.AuditLogger
var _ policy.AuditLogger = (*PostgresAuditLogger)(nil)

// PostgresConfig holds configuration for the PostgresAuditLogger
type PostgresConfig struct {
	// ConnectionString is the Postgres connection string (used if pool is not provided)
	ConnectionString string
	// TableName overrides the default table name (optional)
	TableName string
	// InsertTimeout overrides the default insert timeout (optional)
	InsertTimeout time.Duration
	// MigrateOnStart determines if the schema should be created on startup
	MigrateOnStart bool
}

// PostgresAuditLogger persists audit events to PostgreSQL for compliance and queryability.
type PostgresAuditLogger struct {
	pool      *pgxpool.Pool
	logger    *slog.Logger
	tableName string
	timeout   time.Duration
}

// NewPostgresAuditLogger creates a new PostgreSQL-backed audit logger from an existing pool.
func NewPostgresAuditLogger(ctx context.Context, pool *pgxpool.Pool, cfg PostgresConfig, logger *slog.Logger) (*PostgresAuditLogger, error) {
	if logger == nil {
		logger = slog.Default()
	}

	tableName := AuditTable
	if cfg.TableName != "" {
		if !validTableNameRegex.MatchString(cfg.TableName) {
			return nil, fmt.Errorf("invalid table name: %s", cfg.TableName)
		}
		tableName = cfg.TableName
	}

	timeout := DefaultInsertTimeout
	if cfg.InsertTimeout > 0 {
		timeout = cfg.InsertTimeout
	}

	auditLogger := &PostgresAuditLogger{
		pool:      pool,
		logger:    logger,
		tableName: tableName,
		timeout:   timeout,
	}

	if cfg.MigrateOnStart {
		if err := auditLogger.ensureSchema(ctx); err != nil {
			return nil, fmt.Errorf("failed to migrate schema: %w", err)
		}
		logger.Info("Audit table schema ensured", "table", tableName)
	}

	return auditLogger, nil
}

// NewPostgresAuditLoggerFromURL creates a new connection pool and logger.
func NewPostgresAuditLoggerFromURL(ctx context.Context, cfg PostgresConfig, logger *slog.Logger) (*PostgresAuditLogger, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Set reasonable pool defaults if not specified in URL
	if poolConfig.MaxConns == 0 {
		poolConfig.MaxConns = 10
	}
	if poolConfig.MinConns == 0 {
		poolConfig.MinConns = 2
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}

	return NewPostgresAuditLogger(ctx, pool, cfg, logger)
}

// ensureSchema creates the audit table and indexes if they don't exist
func (l *PostgresAuditLogger) ensureSchema(ctx context.Context) error {
	// Generate a stable lock key from table name to prevent concurrent DDL execution
	// across different instances.
	lockKey := hashStringToBigInt(l.tableName)

	// Acquire session-level advisory lock
	if _, err := l.pool.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("acquire schema lock: %w", err)
	}
	defer func() {
		// Use Background context to ensure unlock happens even if the original ctx is cancelled
		if _, err := l.pool.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey); err != nil {
			l.logger.Warn("Failed to release schema advisory lock", "table", l.tableName, "error", err)
		}
	}()

	ddl := []string{
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				id              UUID PRIMARY KEY,
				created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				session_id      TEXT NOT NULL,
				job_id          TEXT NOT NULL,
				node_id         TEXT NOT NULL,
				trace_id        TEXT,
				span_id         TEXT,
				input_hash      TEXT NOT NULL,
				output_hash     TEXT NOT NULL,
				final_action    TEXT NOT NULL,
				final_output    TEXT,
				results         JSONB NOT NULL DEFAULT '[]',
				duration_ms     BIGINT NOT NULL
			);`, l.tableName),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%[1]s_session_id ON %[1]s(session_id);`, l.tableName),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%[1]s_job_id ON %[1]s(job_id);`, l.tableName),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%[1]s_trace_id ON %[1]s(trace_id);`, l.tableName),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%[1]s_action ON %[1]s(final_action);`, l.tableName),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%[1]s_created_at ON %[1]s(created_at);`, l.tableName),
		// Composite index for common compliance queries
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%[1]s_action_time ON %[1]s(final_action, created_at);`, l.tableName),
	}

	for _, stmt := range ddl {
		if _, err := l.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("execute ddl: %w", err)
		}
	}
	return nil
}

// Log persists an audit event to PostgreSQL
func (l *PostgresAuditLogger) Log(ctx context.Context, event *policy.AuditEvent) error {
	// Serialize results to JSON
	// Optimization: For very high throughput, consider streaming JSON or using a faster library
	resultsJSON, err := json.Marshal(event.Results)
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}

	insertCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	query := fmt.Sprintf(`
		INSERT INTO %s (
			id, created_at, session_id, job_id, node_id, trace_id, span_id,
			input_hash, output_hash, final_action, final_output,
			results, duration_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, l.tableName)

	_, err = l.pool.Exec(insertCtx, query,
		event.ID,
		event.Timestamp,
		event.SessionID,
		event.JobID,
		event.NodeID,
		event.TraceID,
		event.SpanID,
		event.InputHash,
		event.OutputHash,
		string(event.FinalAction),
		event.FinalOutput,
		resultsJSON,
		event.DurationMs,
	)

	if err != nil {
		l.logger.Error("Failed to insert audit event", "error", err, "event_id", event.ID)
		return fmt.Errorf("insert audit event: %w", err)
	}

	// Emit structured log for OTEL/Real-time observability.
	// This allows correlation in logging backends (Grafana/Datadog) without querying Postgres.
	l.logger.Info("Policy Audit Event",
		"event_id", event.ID,
		"action", event.FinalAction,
		"session_id", event.SessionID,
		"trace_id", event.TraceID,
		"span_id", event.SpanID,
		"duration_ms", event.DurationMs,
		"rules_matched", len(event.Results),
	)

	return nil
}

// Close closes the database connection pool
func (l *PostgresAuditLogger) Close() {
	if l.pool != nil {
		l.pool.Close()
		l.logger.Info("Audit logger connection pool closed")
	}
}

// QueryBySession retrieves audit events for a specific session
func (l *PostgresAuditLogger) QueryBySession(ctx context.Context, sessionID string, limit int) ([]*policy.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	query := fmt.Sprintf(`
		SELECT id, created_at, session_id, job_id, node_id, trace_id, span_id,
		       input_hash, output_hash, final_action, final_output,
		       results, duration_ms
		FROM %s
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, l.tableName)

	rows, err := l.pool.Query(ctx, query, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query by session: %w", err)
	}
	defer rows.Close()

	return l.scanAuditEvents(rows)
}

// QueryByAction retrieves audit events by action type
func (l *PostgresAuditLogger) QueryByAction(ctx context.Context, action policy.Action, since time.Time, limit int) ([]*policy.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	query := fmt.Sprintf(`
		SELECT id, created_at, session_id, job_id, node_id, trace_id, span_id,
		       input_hash, output_hash, final_action, final_output,
		       results, duration_ms
		FROM %s
		WHERE final_action = $1 AND created_at >= $2
		ORDER BY created_at DESC
		LIMIT $3
	`, l.tableName)

	rows, err := l.pool.Query(ctx, query, string(action), since, limit)
	if err != nil {
		return nil, fmt.Errorf("query by action: %w", err)
	}
	defer rows.Close()

	return l.scanAuditEvents(rows)
}

// QueryViolations retrieves all block/redact events since a given time
func (l *PostgresAuditLogger) QueryViolations(ctx context.Context, since time.Time, limit int) ([]*policy.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	query := fmt.Sprintf(`
		SELECT id, created_at, session_id, job_id, node_id, trace_id, span_id,
		       input_hash, output_hash, final_action, final_output,
		       results, duration_ms
		FROM %s
		WHERE final_action IN ('block', 'redact') AND created_at >= $1
		ORDER BY created_at DESC
		LIMIT $2
	`, l.tableName)

	rows, err := l.pool.Query(ctx, query, since, limit)
	if err != nil {
		return nil, fmt.Errorf("query violations: %w", err)
	}
	defer rows.Close()

	return l.scanAuditEvents(rows)
}

// CountByAction returns counts of each action type since a given time
func (l *PostgresAuditLogger) CountByAction(ctx context.Context, since time.Time) (map[policy.Action]int64, error) {
	query := fmt.Sprintf(`
		SELECT final_action, COUNT(*) as count
		FROM %s
		WHERE created_at >= $1
		GROUP BY final_action
	`, l.tableName)

	rows, err := l.pool.Query(ctx, query, since)
	if err != nil {
		return nil, fmt.Errorf("count by action: %w", err)
	}
	defer rows.Close()

	counts := make(map[policy.Action]int64)
	for rows.Next() {
		var action string
		var count int64
		if err := rows.Scan(&action, &count); err != nil {
			return nil, fmt.Errorf("scan count: %w", err)
		}
		counts[policy.Action(action)] = count
	}

	return counts, rows.Err()
}

// scanAuditEvents helper to scan rows into AuditEvent structs
func (l *PostgresAuditLogger) scanAuditEvents(rows pgx.Rows) ([]*policy.AuditEvent, error) {
	var events []*policy.AuditEvent

	for rows.Next() {
		var event policy.AuditEvent
		var resultsJSON []byte
		var actionStr string
		var traceID, spanID *string

		err := rows.Scan(
			&event.ID,
			&event.Timestamp,
			&event.SessionID,
			&event.JobID,
			&event.NodeID,
			&traceID,
			&spanID,
			&event.InputHash,
			&event.OutputHash,
			&actionStr,
			&event.FinalOutput,
			&resultsJSON,
			&event.DurationMs,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		if traceID != nil {
			event.TraceID = *traceID
		}
		if spanID != nil {
			event.SpanID = *spanID
		}
		event.FinalAction = policy.Action(actionStr)

		if err := json.Unmarshal(resultsJSON, &event.Results); err != nil {
			return nil, fmt.Errorf("unmarshal results: %w", err)
		}

		events = append(events, &event)
	}

	return events, rows.Err()
}

// hashStringToBigInt generates a stable int64 from a string for use as a Postgres advisory lock key.
func hashStringToBigInt(s string) int64 {
	var hash int64 = 5381
	for i := 0; i < len(s); i++ {
		hash = ((hash << 5) + hash) + int64(s[i])
	}
	if hash < 0 {
		hash = -hash
	}
	return hash
}