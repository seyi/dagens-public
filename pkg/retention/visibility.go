package retention

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type VisibilitySnapshot struct {
	UnfinishedJobs  int64                `json:"unfinished_jobs"`
	TerminalBuckets []TerminalAgeBucket  `json:"terminal_buckets"`
	EligibleArchive EligibleArchiveStats `json:"eligible_archive"`
}

type TerminalAgeBucket struct {
	Label string `json:"label"`
	Jobs  int64  `json:"jobs"`
}

type EligibleArchiveStats struct {
	HotRetentionDays int64 `json:"hot_retention_days"`
	Jobs             int64 `json:"jobs"`
	TransitionRows   int64 `json:"transition_rows"`
	TaskRows         int64 `json:"task_rows"`
	SequenceRows     int64 `json:"sequence_rows"`
}

func QueryVisibilitySnapshot(ctx context.Context, pool *pgxpool.Pool, hotRetentionDays int) (VisibilitySnapshot, error) {
	if pool == nil {
		return VisibilitySnapshot{}, fmt.Errorf("pool is required")
	}
	if hotRetentionDays <= 0 {
		return VisibilitySnapshot{}, fmt.Errorf("hotRetentionDays must be > 0")
	}

	snapshot := VisibilitySnapshot{}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM scheduler_durable_jobs
		WHERE current_state NOT IN ('SUCCEEDED','FAILED','CANCELED')
	`).Scan(&snapshot.UnfinishedJobs); err != nil {
		return VisibilitySnapshot{}, fmt.Errorf("count unfinished jobs: %w", err)
	}

	buckets, err := queryTerminalBuckets(ctx, pool)
	if err != nil {
		return VisibilitySnapshot{}, fmt.Errorf("query terminal buckets: %w", err)
	}
	snapshot.TerminalBuckets = buckets

	eligible, err := queryEligibleArchive(ctx, pool, hotRetentionDays)
	if err != nil {
		return VisibilitySnapshot{}, fmt.Errorf("query eligible archive stats: %w", err)
	}
	snapshot.EligibleArchive = eligible

	return snapshot, nil
}

func queryTerminalBuckets(ctx context.Context, pool *pgxpool.Pool) ([]TerminalAgeBucket, error) {
	rows, err := pool.Query(ctx, `
		WITH terminal_jobs AS (
			SELECT
				CASE
					WHEN updated_at >= NOW() - INTERVAL '7 days' THEN 'lt_7d'
					WHEN updated_at >= NOW() - INTERVAL '14 days' THEN '7_to_14d'
					WHEN updated_at >= NOW() - INTERVAL '30 days' THEN '14_to_30d'
					WHEN updated_at >= NOW() - INTERVAL '90 days' THEN '30_to_90d'
					ELSE 'gte_90d'
				END AS bucket
			FROM scheduler_durable_jobs
			WHERE current_state IN ('SUCCEEDED','FAILED','CANCELED')
		)
		SELECT bucket, COUNT(*)
		FROM terminal_jobs
		GROUP BY bucket
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int64{
		"lt_7d":     0,
		"7_to_14d":  0,
		"14_to_30d": 0,
		"30_to_90d": 0,
		"gte_90d":   0,
	}
	for rows.Next() {
		var bucket string
		var count int64
		if err := rows.Scan(&bucket, &count); err != nil {
			return nil, err
		}
		counts[bucket] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return []TerminalAgeBucket{
		{Label: "lt_7d", Jobs: counts["lt_7d"]},
		{Label: "7_to_14d", Jobs: counts["7_to_14d"]},
		{Label: "14_to_30d", Jobs: counts["14_to_30d"]},
		{Label: "30_to_90d", Jobs: counts["30_to_90d"]},
		{Label: "gte_90d", Jobs: counts["gte_90d"]},
	}, nil
}

func queryEligibleArchive(ctx context.Context, pool *pgxpool.Pool, hotRetentionDays int) (EligibleArchiveStats, error) {
	stats := EligibleArchiveStats{HotRetentionDays: int64(hotRetentionDays)}
	intervalExpr := fmt.Sprintf("%d days", hotRetentionDays)

	if err := pool.QueryRow(ctx, `
		WITH eligible_jobs AS (
			SELECT job_id
			FROM scheduler_durable_jobs
			WHERE current_state IN ('SUCCEEDED','FAILED','CANCELED')
			  AND updated_at < NOW() - $1::interval
		)
		SELECT COUNT(*) FROM eligible_jobs
	`, intervalExpr).Scan(&stats.Jobs); err != nil {
		return stats, err
	}

	if err := pool.QueryRow(ctx, `
		WITH eligible_jobs AS (
			SELECT job_id
			FROM scheduler_durable_jobs
			WHERE current_state IN ('SUCCEEDED','FAILED','CANCELED')
			  AND updated_at < NOW() - $1::interval
		)
		SELECT COUNT(*)
		FROM scheduler_job_transitions t
		JOIN eligible_jobs e ON e.job_id = t.job_id
	`, intervalExpr).Scan(&stats.TransitionRows); err != nil {
		return stats, err
	}

	if err := pool.QueryRow(ctx, `
		WITH eligible_jobs AS (
			SELECT job_id
			FROM scheduler_durable_jobs
			WHERE current_state IN ('SUCCEEDED','FAILED','CANCELED')
			  AND updated_at < NOW() - $1::interval
		)
		SELECT COUNT(*)
		FROM scheduler_durable_tasks t
		JOIN eligible_jobs e ON e.job_id = t.job_id
	`, intervalExpr).Scan(&stats.TaskRows); err != nil {
		return stats, err
	}

	if err := pool.QueryRow(ctx, `
		WITH eligible_jobs AS (
			SELECT job_id
			FROM scheduler_durable_jobs
			WHERE current_state IN ('SUCCEEDED','FAILED','CANCELED')
			  AND updated_at < NOW() - $1::interval
		)
		SELECT COUNT(*)
		FROM scheduler_job_sequences s
		JOIN eligible_jobs e ON e.job_id = s.job_id
	`, intervalExpr).Scan(&stats.SequenceRows); err != nil {
		return stats, err
	}

	return stats, nil
}
