package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type rowcountConfig struct {
	DatabaseURL  string        `json:"database_url"`
	JobIDPrefix  string        `json:"job_id_prefix"`
	EvidenceRoot string        `json:"evidence_root"`
	Timeout      time.Duration `json:"timeout"`
}

type rowcountResult struct {
	TimestampUTC string         `json:"timestamp_utc"`
	Config       rowcountConfig `json:"config"`
	Remaining    rowCounts      `json:"remaining"`
}

type rowCounts struct {
	Jobs        int64 `json:"jobs"`
	Transitions int64 `json:"transitions"`
	Tasks       int64 `json:"tasks"`
	Sequences   int64 `json:"sequences"`
	DurableJobs int64 `json:"durable_jobs"`
}

func main() {
	cfg := rowcountConfig{}
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for scheduler transition store")
	flag.StringVar(&cfg.JobIDPrefix, "job-id-prefix", "", "required job_id prefix to count")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 30*time.Second, "overall timeout")
	flag.Parse()

	if cfg.DatabaseURL == "" {
		exitf("database-url is required")
	}
	if cfg.JobIDPrefix == "" {
		exitf("job-id-prefix is required")
	}
	if err := os.MkdirAll(cfg.EvidenceRoot, 0o755); err != nil {
		exitf("create evidence root: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		exitf("create pgx pool: %v", err)
	}
	defer pool.Close()

	result := rowcountResult{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Config:       cfg,
	}
	prefix := cfg.JobIDPrefix + "%"
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduler_job_transitions WHERE job_id LIKE $1`, prefix).Scan(&result.Remaining.Transitions); err != nil {
		exitf("count transitions: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduler_durable_tasks WHERE job_id LIKE $1`, prefix).Scan(&result.Remaining.Tasks); err != nil {
		exitf("count tasks: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduler_job_sequences WHERE job_id LIKE $1`, prefix).Scan(&result.Remaining.Sequences); err != nil {
		exitf("count sequences: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduler_durable_jobs WHERE job_id LIKE $1`, prefix).Scan(&result.Remaining.DurableJobs); err != nil {
		exitf("count durable jobs: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		WITH jobs AS (
			SELECT job_id FROM scheduler_job_transitions WHERE job_id LIKE $1
			UNION
			SELECT job_id FROM scheduler_durable_tasks WHERE job_id LIKE $1
			UNION
			SELECT job_id FROM scheduler_job_sequences WHERE job_id LIKE $1
			UNION
			SELECT job_id FROM scheduler_durable_jobs WHERE job_id LIKE $1
		)
		SELECT COUNT(*) FROM jobs
	`, prefix).Scan(&result.Remaining.Jobs); err != nil {
		exitf("count distinct jobs: %v", err)
	}

	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: transition archive rowcount completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("jobs=%d transitions=%d tasks=%d sequences=%d durable_jobs=%d\n",
		result.Remaining.Jobs, result.Remaining.Transitions, result.Remaining.Tasks, result.Remaining.Sequences, result.Remaining.DurableJobs)
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-transition-archive-rowcount-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func writeResults(evidenceRoot string, result rowcountResult) error {
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
			"jobs=%d\n"+
			"transitions=%d\n"+
			"tasks=%d\n"+
			"sequences=%d\n"+
			"durable_jobs=%d\n",
		evidenceRoot,
		result.Remaining.Jobs,
		result.Remaining.Transitions,
		result.Remaining.Tasks,
		result.Remaining.Sequences,
		result.Remaining.DurableJobs,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
