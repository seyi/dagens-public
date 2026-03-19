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
	"github.com/seyi/dagens/pkg/retention"
)

type reportConfig struct {
	DatabaseURL      string
	HotRetentionDays int
	EvidenceRoot     string
	Timeout          time.Duration
}

type reportResult struct {
	TimestampUTC string       `json:"timestamp_utc"`
	Config       reportConfig `json:"config"`
	retention.VisibilitySnapshot
}

func main() {
	cfg := reportConfig{}
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for scheduler transition store")
	flag.IntVar(&cfg.HotRetentionDays, "hot-retention-days", 14, "terminal-job hot retention window in days")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 30*time.Second, "overall report timeout")
	flag.Parse()

	if cfg.DatabaseURL == "" {
		exitf("database-url is required")
	}
	if cfg.HotRetentionDays <= 0 {
		exitf("hot-retention-days must be > 0")
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

	snapshot, err := retention.QueryVisibilitySnapshot(ctx, pool, cfg.HotRetentionDays)
	if err != nil {
		exitf("query retention visibility snapshot: %v", err)
	}
	result := reportResult{
		TimestampUTC:       time.Now().UTC().Format(time.RFC3339),
		Config:             cfg,
		VisibilitySnapshot: snapshot,
	}

	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: transition retention report completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("unfinished_jobs=%d eligible_jobs=%d eligible_transition_rows=%d\n",
		result.UnfinishedJobs,
		result.EligibleArchive.Jobs,
		result.EligibleArchive.TransitionRows,
	)
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-transition-retention-report-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func writeResults(evidenceRoot string, result reportResult) error {
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
			"unfinished_jobs=%d\n"+
			"eligible_jobs=%d\n"+
			"eligible_transition_rows=%d\n"+
			"eligible_task_rows=%d\n"+
			"eligible_sequence_rows=%d\n",
		evidenceRoot,
		result.UnfinishedJobs,
		result.EligibleArchive.Jobs,
		result.EligibleArchive.TransitionRows,
		result.EligibleArchive.TaskRows,
		result.EligibleArchive.SequenceRows,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}
