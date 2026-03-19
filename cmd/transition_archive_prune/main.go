package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pruneSchemaVersion = "transition-archive-v1"

type pruneConfig struct {
	DatabaseURL      string        `json:"database_url"`
	ManifestPath     string        `json:"manifest_path"`
	BatchLimit       int           `json:"batch_limit"`
	DryRun           bool          `json:"dry_run"`
	ReferenceTimeRaw string        `json:"reference_time,omitempty"`
	EvidenceRoot     string        `json:"evidence_root"`
	Timeout          time.Duration `json:"timeout"`
}

type pruneManifest struct {
	SchemaVersion    string               `json:"schema_version"`
	ArchiveTimestamp string               `json:"archive_timestamp"`
	HotRetentionDays int                  `json:"hot_retention_days"`
	BatchLimit       int                  `json:"batch_limit"`
	SelectedJobs     int                  `json:"selected_jobs"`
	ExportedJobs     int                  `json:"exported_jobs"`
	BundleDir        string               `json:"bundle_dir"`
	Bundles          []pruneManifestEntry `json:"bundles"`
	Verification     pruneVerification    `json:"verification"`
}

type pruneManifestEntry struct {
	JobID          string `json:"job_id"`
	CurrentState   string `json:"current_state"`
	UpdatedAtUTC   string `json:"updated_at_utc"`
	RelativePath   string `json:"relative_path"`
	SHA256         string `json:"sha256"`
	TransitionRows int    `json:"transition_rows"`
	TaskRows       int    `json:"task_rows"`
	SequenceRows   int    `json:"sequence_rows"`
	LastSequenceID int64  `json:"last_sequence_id"`
}

type pruneVerification struct {
	Enabled              bool   `json:"enabled"`
	Verified             bool   `json:"verified"`
	VerifiedAtUTC        string `json:"verified_at_utc,omitempty"`
	VerifiedBundles      int    `json:"verified_bundles"`
	VerifiedBytes        int64  `json:"verified_bytes"`
	ManifestChecksPassed bool   `json:"manifest_checks_passed"`
}

type pruneResult struct {
	TimestampUTC string             `json:"timestamp_utc"`
	Config       pruneConfig        `json:"config"`
	Summary      pruneSummary       `json:"summary"`
	Jobs         []pruneJobDecision `json:"jobs"`
}

type pruneSummary struct {
	ManifestJobs           int `json:"manifest_jobs"`
	CheckedJobs            int `json:"checked_jobs"`
	EligibleJobs           int `json:"eligible_jobs"`
	DeletedJobs            int `json:"deleted_jobs"`
	DeletedTransitions     int `json:"deleted_transitions"`
	DeletedTasks           int `json:"deleted_tasks"`
	DeletedSequences       int `json:"deleted_sequences"`
	DeletedDurableJobs     int `json:"deleted_durable_jobs"`
}

type pruneJobDecision struct {
	JobID               string `json:"job_id"`
	CurrentState        string `json:"current_state"`
	LastSequenceID      int64  `json:"last_sequence_id"`
	TransitionRows      int    `json:"transition_rows"`
	TaskRows            int    `json:"task_rows"`
	SequenceRows        int    `json:"sequence_rows"`
	Eligible            bool   `json:"eligible"`
	Action              string `json:"action"`
	Reason              string `json:"reason,omitempty"`
	DeletedTransitions  int64  `json:"deleted_transitions,omitempty"`
	DeletedTasks        int64  `json:"deleted_tasks,omitempty"`
	DeletedSequences    int64  `json:"deleted_sequences,omitempty"`
	DeletedDurableJobs  int64  `json:"deleted_durable_jobs,omitempty"`
}

type liveJobSnapshot struct {
	CurrentState   string
	LastSequenceID int64
	UpdatedAt      time.Time
	TransitionRows int
	TaskRows       int
	SequenceRows   int
}

func main() {
	cfg := pruneConfig{}
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for scheduler transition store")
	flag.StringVar(&cfg.ManifestPath, "manifest-path", "", "required archive manifest path from transition_archive_export")
	flag.IntVar(&cfg.BatchLimit, "batch-limit", 100, "maximum number of manifest jobs to evaluate for prune")
	flag.BoolVar(&cfg.DryRun, "dry-run", true, "evaluate safety checks without deleting rows")
	flag.StringVar(&cfg.ReferenceTimeRaw, "reference-time", "", "optional RFC3339 timestamp for deterministic prune validation")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 60*time.Second, "overall prune timeout")
	flag.Parse()

	if cfg.DatabaseURL == "" {
		exitf("database-url is required")
	}
	if strings.TrimSpace(cfg.ManifestPath) == "" {
		exitf("manifest-path is required")
	}
	if cfg.BatchLimit <= 0 {
		exitf("batch-limit must be > 0")
	}
	if err := os.MkdirAll(cfg.EvidenceRoot, 0o755); err != nil {
		exitf("create evidence root: %v", err)
	}

	manifest, err := loadManifest(cfg.ManifestPath)
	if err != nil {
		exitf("load manifest: %v", err)
	}
	if err := validateManifest(manifest); err != nil {
		exitf("validate manifest: %v", err)
	}

	referenceTime, err := parseReferenceTime(cfg.ReferenceTimeRaw)
	if err != nil {
		exitf("parse reference-time: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		exitf("create pgx pool: %v", err)
	}
	defer pool.Close()

	limit := cfg.BatchLimit
	if len(manifest.Bundles) < limit {
		limit = len(manifest.Bundles)
	}
	result := pruneResult{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Config:       cfg,
		Summary: pruneSummary{
			ManifestJobs: len(manifest.Bundles),
		},
		Jobs: make([]pruneJobDecision, 0, limit),
	}

	for i, bundle := range manifest.Bundles[:limit] {
		if i > 0 && i%25 == 0 {
			fmt.Printf("prune_progress=%d/%d\n", i, limit)
		}
		decision, err := evaluateCandidate(ctx, pool, manifest, bundle, referenceTime)
		if err != nil {
			exitf("evaluate prune candidate %s: %v", bundle.JobID, err)
		}
		result.Summary.CheckedJobs++
		if decision.Eligible {
			result.Summary.EligibleJobs++
		}
		if !cfg.DryRun && decision.Eligible {
			if err := executePrune(ctx, pool, &decision); err != nil {
				exitf("prune job %s: %v", bundle.JobID, err)
			}
			result.Summary.DeletedJobs++
			result.Summary.DeletedTransitions += int(decision.DeletedTransitions)
			result.Summary.DeletedTasks += int(decision.DeletedTasks)
			result.Summary.DeletedSequences += int(decision.DeletedSequences)
			result.Summary.DeletedDurableJobs += int(decision.DeletedDurableJobs)
		}
		result.Jobs = append(result.Jobs, decision)
	}

	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: transition archive prune evaluation completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("checked_jobs=%d eligible_jobs=%d deleted_jobs=%d dry_run=%t\n",
		result.Summary.CheckedJobs, result.Summary.EligibleJobs, result.Summary.DeletedJobs, cfg.DryRun)
}

func loadManifest(path string) (pruneManifest, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return pruneManifest{}, err
	}
	var manifest pruneManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return pruneManifest{}, err
	}
	return manifest, nil
}

func validateManifest(manifest pruneManifest) error {
	if manifest.SchemaVersion != pruneSchemaVersion {
		return fmt.Errorf("schema_version=%q", manifest.SchemaVersion)
	}
	if !manifest.Verification.Enabled || !manifest.Verification.Verified || !manifest.Verification.ManifestChecksPassed {
		return fmt.Errorf("manifest verification state is not fully passed")
	}
	if manifest.Verification.VerifiedBundles < manifest.ExportedJobs {
		return fmt.Errorf("verified_bundles=%d exported_jobs=%d", manifest.Verification.VerifiedBundles, manifest.ExportedJobs)
	}
	return nil
}

func evaluateCandidate(ctx context.Context, pool *pgxpool.Pool, manifest pruneManifest, bundle pruneManifestEntry, referenceTime *time.Time) (pruneJobDecision, error) {
	decision := pruneJobDecision{
		JobID:          bundle.JobID,
		CurrentState:   bundle.CurrentState,
		LastSequenceID: bundle.LastSequenceID,
		TransitionRows: bundle.TransitionRows,
		TaskRows:       bundle.TaskRows,
		SequenceRows:   bundle.SequenceRows,
		Action:         "skip",
	}
	snapshot, err := loadLiveSnapshot(ctx, pool, bundle.JobID)
	if err != nil {
		if err == pgx.ErrNoRows {
			decision.Reason = "durable job missing"
			return decision, nil
		}
		return decision, err
	}
	if !isTerminalState(snapshot.CurrentState) {
		decision.Reason = "job is not terminal"
		return decision, nil
	}
	if snapshot.CurrentState != bundle.CurrentState {
		decision.Reason = fmt.Sprintf("current_state drift: live=%s manifest=%s", snapshot.CurrentState, bundle.CurrentState)
		return decision, nil
	}
	if snapshot.LastSequenceID != bundle.LastSequenceID {
		decision.Reason = fmt.Sprintf("last_sequence_id drift: live=%d manifest=%d", snapshot.LastSequenceID, bundle.LastSequenceID)
		return decision, nil
	}
	if snapshot.TransitionRows != bundle.TransitionRows || snapshot.TaskRows != bundle.TaskRows || snapshot.SequenceRows != bundle.SequenceRows {
		decision.Reason = fmt.Sprintf("row-count drift: live=(%d,%d,%d) manifest=(%d,%d,%d)",
			snapshot.TransitionRows, snapshot.TaskRows, snapshot.SequenceRows,
			bundle.TransitionRows, bundle.TaskRows, bundle.SequenceRows)
		return decision, nil
	}
	cutoffBase := time.Now().UTC()
	if referenceTime != nil {
		cutoffBase = referenceTime.UTC()
	}
	if !snapshot.UpdatedAt.Before(cutoffBase.Add(-time.Duration(manifest.HotRetentionDays) * 24 * time.Hour)) {
		decision.Reason = "job is not older than hot retention cutoff"
		return decision, nil
	}
	decision.Eligible = true
	if snapshot.TransitionRows == 0 && snapshot.TaskRows == 0 && snapshot.SequenceRows == 0 {
		decision.Reason = "nothing to prune"
		return decision, nil
	}
	if referenceTime != nil {
		decision.Reason = fmt.Sprintf("eligible under reference_time=%s", referenceTime.UTC().Format(time.RFC3339))
	} else {
		decision.Reason = "eligible"
	}
	if decision.Eligible {
		decision.Action = "prune"
	}
	return decision, nil
}

func loadLiveSnapshot(ctx context.Context, pool *pgxpool.Pool, jobID string) (liveJobSnapshot, error) {
	var snapshot liveJobSnapshot
	if err := pool.QueryRow(ctx, `
		SELECT current_state, last_sequence_id, updated_at
		FROM scheduler_durable_jobs
		WHERE job_id = $1
	`, jobID).Scan(&snapshot.CurrentState, &snapshot.LastSequenceID, &snapshot.UpdatedAt); err != nil {
		return snapshot, err
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduler_job_transitions WHERE job_id = $1`, jobID).Scan(&snapshot.TransitionRows); err != nil {
		return snapshot, err
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduler_durable_tasks WHERE job_id = $1`, jobID).Scan(&snapshot.TaskRows); err != nil {
		return snapshot, err
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM scheduler_job_sequences WHERE job_id = $1`, jobID).Scan(&snapshot.SequenceRows); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func executePrune(ctx context.Context, pool *pgxpool.Pool, decision *pruneJobDecision) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if decision.DeletedTransitions, err = deleteRows(ctx, tx, `DELETE FROM scheduler_job_transitions WHERE job_id = $1`, decision.JobID); err != nil {
		return err
	}
	if decision.DeletedTasks, err = deleteRows(ctx, tx, `DELETE FROM scheduler_durable_tasks WHERE job_id = $1`, decision.JobID); err != nil {
		return err
	}
	if decision.DeletedSequences, err = deleteRows(ctx, tx, `DELETE FROM scheduler_job_sequences WHERE job_id = $1`, decision.JobID); err != nil {
		return err
	}
	if decision.DeletedDurableJobs, err = deleteRows(ctx, tx, `DELETE FROM scheduler_durable_jobs WHERE job_id = $1`, decision.JobID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func deleteRows(ctx context.Context, tx pgx.Tx, query, jobID string) (int64, error) {
	tag, err := tx.Exec(ctx, query, jobID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func isTerminalState(state string) bool {
	switch state {
	case "SUCCEEDED", "FAILED", "CANCELED":
		return true
	default:
		return false
	}
}

func parseReferenceTime(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-transition-archive-prune-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func writeResults(evidenceRoot string, result pruneResult) error {
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
			"manifest_jobs=%d\n"+
			"checked_jobs=%d\n"+
			"eligible_jobs=%d\n"+
			"deleted_jobs=%d\n",
		evidenceRoot,
		result.Summary.ManifestJobs,
		result.Summary.CheckedJobs,
		result.Summary.EligibleJobs,
		result.Summary.DeletedJobs,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
