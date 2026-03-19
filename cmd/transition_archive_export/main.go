package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const archiveSchemaVersion = "transition-archive-v1"

type exportConfig struct {
	DatabaseURL      string        `json:"database_url"`
	HotRetentionDays int           `json:"hot_retention_days"`
	BatchLimit       int           `json:"batch_limit"`
	JobIDPrefix      string        `json:"job_id_prefix,omitempty"`
	DryRun           bool          `json:"dry_run"`
	VerifyBundles    bool          `json:"verify_bundles"`
	ReferenceTimeRaw string        `json:"reference_time,omitempty"`
	EvidenceRoot     string        `json:"evidence_root"`
	Timeout          time.Duration `json:"timeout"`
}

type exportResult struct {
	TimestampUTC   string         `json:"timestamp_utc"`
	Config         exportConfig   `json:"config"`
	Manifest       exportManifest `json:"manifest"`
	SelectedJobIDs []string       `json:"selected_job_ids,omitempty"`
}

type exportManifest struct {
	SchemaVersion    string           `json:"schema_version"`
	ArchiveTimestamp string           `json:"archive_timestamp"`
	HotRetentionDays int              `json:"hot_retention_days"`
	BatchLimit       int              `json:"batch_limit"`
	SelectedJobs     int              `json:"selected_jobs"`
	ExportedJobs     int              `json:"exported_jobs"`
	BundleDir        string           `json:"bundle_dir"`
	Bundles          []exportedBundle `json:"bundles"`
	Verification     verification     `json:"verification"`
}

type exportedBundle struct {
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

type verification struct {
	Enabled              bool   `json:"enabled"`
	Verified             bool   `json:"verified"`
	VerifiedAtUTC        string `json:"verified_at_utc,omitempty"`
	VerifiedBundles      int    `json:"verified_bundles"`
	VerifiedBytes        int64  `json:"verified_bytes"`
	ManifestChecksPassed bool   `json:"manifest_checks_passed"`
}

type archiveBundle struct {
	SchemaVersion    string           `json:"schema_version"`
	ArchiveTimestamp string           `json:"archive_timestamp"`
	HotRetentionDays int              `json:"hot_retention_days"`
	Job              durableJobRow    `json:"job"`
	Tasks            []durableTaskRow `json:"tasks"`
	Transitions      []transitionRow  `json:"transitions"`
	Sequences        []jobSequenceRow `json:"sequences"`
}

type durableJobRow struct {
	JobID          string `json:"job_id"`
	Name           string `json:"name"`
	CurrentState   string `json:"current_state"`
	LastSequenceID int64  `json:"last_sequence_id"`
	CreatedAtUTC   string `json:"created_at_utc"`
	UpdatedAtUTC   string `json:"updated_at_utc"`
}

type durableTaskRow struct {
	TaskID       string `json:"task_id"`
	JobID        string `json:"job_id"`
	StageID      string `json:"stage_id"`
	NodeID       string `json:"node_id"`
	AgentID      string `json:"agent_id"`
	AgentName    string `json:"agent_name"`
	InputJSON    string `json:"input_json"`
	PartitionKey string `json:"partition_key"`
	CurrentState string `json:"current_state"`
	LastAttempt  int    `json:"last_attempt"`
	UpdatedAtUTC string `json:"updated_at_utc"`
}

type transitionRow struct {
	JobID         string `json:"job_id"`
	SequenceID    int64  `json:"sequence_id"`
	EntityType    string `json:"entity_type"`
	Transition    string `json:"transition"`
	TaskID        string `json:"task_id"`
	PreviousState string `json:"previous_state"`
	NewState      string `json:"new_state"`
	NodeID        string `json:"node_id"`
	Attempt       int    `json:"attempt"`
	ErrorSummary  string `json:"error_summary"`
	OccurredAtUTC string `json:"occurred_at_utc"`
}

type jobSequenceRow struct {
	JobID          string `json:"job_id"`
	LastSequenceID int64  `json:"last_sequence_id"`
	UpdatedAtUTC   string `json:"updated_at_utc"`
}

type eligibleJob struct {
	JobID          string
	CurrentState   string
	LastSequenceID int64
	UpdatedAt      time.Time
}

var invalidBundleName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func main() {
	cfg := exportConfig{}
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for scheduler transition store")
	flag.IntVar(&cfg.HotRetentionDays, "hot-retention-days", 14, "terminal-job hot retention window in days; 0 exports all currently terminal jobs")
	flag.IntVar(&cfg.BatchLimit, "batch-limit", 100, "maximum number of eligible terminal jobs to export")
	flag.StringVar(&cfg.JobIDPrefix, "job-id-prefix", "", "optional job_id prefix filter for targeted export validation")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "list eligible jobs without exporting bundles")
	flag.BoolVar(&cfg.VerifyBundles, "verify-bundles", true, "re-read exported bundles and verify checksums plus manifest metadata")
	flag.StringVar(&cfg.ReferenceTimeRaw, "reference-time", "", "optional RFC3339 timestamp for deterministic retention cutoff validation")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 60*time.Second, "overall export timeout")
	flag.Parse()

	if cfg.DatabaseURL == "" {
		exitf("database-url is required")
	}
	if cfg.HotRetentionDays < 0 {
		exitf("hot-retention-days must be >= 0")
	}
	if cfg.BatchLimit <= 0 {
		exitf("batch-limit must be > 0")
	}
	if err := os.MkdirAll(filepath.Join(cfg.EvidenceRoot, "bundles"), 0o755); err != nil {
		exitf("create evidence root: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		exitf("create pgx pool: %v", err)
	}
	defer pool.Close()

	archiveTimestamp := time.Now().UTC()
	referenceTime, err := parseReferenceTime(cfg.ReferenceTimeRaw)
	if err != nil {
		exitf("parse reference-time: %v", err)
	}
	eligible, err := listEligibleJobs(ctx, pool, cfg.HotRetentionDays, cfg.BatchLimit, cfg.JobIDPrefix, referenceTime)
	if err != nil {
		exitf("list eligible jobs: %v", err)
	}

	manifest := exportManifest{
		SchemaVersion:    archiveSchemaVersion,
		ArchiveTimestamp: archiveTimestamp.Format(time.RFC3339),
		HotRetentionDays: cfg.HotRetentionDays,
		BatchLimit:       cfg.BatchLimit,
		SelectedJobs:     len(eligible),
		BundleDir:        filepath.Join(cfg.EvidenceRoot, "bundles"),
		Bundles:          make([]exportedBundle, 0, len(eligible)),
		Verification: verification{
			Enabled: shouldVerifyBundles(cfg),
		},
	}

	selectedJobIDs := make([]string, 0, len(eligible))
	for i, job := range eligible {
		selectedJobIDs = append(selectedJobIDs, job.JobID)
		if cfg.DryRun {
			continue
		}
		if i > 0 && i%25 == 0 {
			fmt.Printf("export_progress=%d/%d\n", i, len(eligible))
		}
		bundle, exported, err := exportJobBundle(ctx, pool, archiveTimestamp, cfg.HotRetentionDays, cfg.EvidenceRoot, job)
		if err != nil {
			exitf("export bundle for job %s: %v", job.JobID, err)
		}
		manifest.Bundles = append(manifest.Bundles, exported)
		manifest.ExportedJobs++
		_ = bundle
	}
	if manifest.Verification.Enabled {
		verified, err := verifyManifest(cfg.EvidenceRoot, manifest)
		if err != nil {
			exitf("verify exported bundles: %v", err)
		}
		manifest.Verification = verified
	}

	result := exportResult{
		TimestampUTC:   archiveTimestamp.Format(time.RFC3339),
		Config:         cfg,
		Manifest:       manifest,
		SelectedJobIDs: selectedJobIDs,
	}
	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: transition archive export completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("selected_jobs=%d exported_jobs=%d dry_run=%t\n", manifest.SelectedJobs, manifest.ExportedJobs, cfg.DryRun)
	if manifest.Verification.Enabled {
		fmt.Printf("verified_bundles=%d verified_bytes=%d\n", manifest.Verification.VerifiedBundles, manifest.Verification.VerifiedBytes)
	}
}

func listEligibleJobs(ctx context.Context, pool *pgxpool.Pool, hotRetentionDays, batchLimit int, jobIDPrefix string, referenceTime *time.Time) ([]eligibleJob, error) {
	// intervalExpr is intentionally rendered from a validated integer. PostgreSQL interval
	// placeholders are awkward in this form, and the value is not sourced from raw user SQL.
	intervalExpr := fmt.Sprintf("%d days", hotRetentionDays)
	query := `
		SELECT job_id, current_state, last_sequence_id, updated_at
		FROM scheduler_durable_jobs
		WHERE current_state IN ('SUCCEEDED','FAILED','CANCELED')
	`
	args := []interface{}{intervalExpr}
	if referenceTime != nil {
		query += ` AND updated_at < $2::timestamptz - $1::interval`
		args = append(args, referenceTime.UTC())
	} else {
		query += ` AND updated_at < NOW() - $1::interval`
	}
	if jobIDPrefix != "" {
		query += fmt.Sprintf(" AND job_id LIKE $%d", len(args)+1)
		args = append(args, jobIDPrefix+"%")
	}
	query += fmt.Sprintf(" ORDER BY updated_at ASC, job_id ASC LIMIT $%d", len(args)+1)
	args = append(args, batchLimit)
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []eligibleJob
	for rows.Next() {
		var job eligibleJob
		if err := rows.Scan(&job.JobID, &job.CurrentState, &job.LastSequenceID, &job.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func exportJobBundle(ctx context.Context, pool *pgxpool.Pool, archiveTimestamp time.Time, hotRetentionDays int, evidenceRoot string, job eligibleJob) (archiveBundle, exportedBundle, error) {
	// Bundle export is a best-effort multi-query snapshot, not a SERIALIZABLE point-in-time read.
	// This is acceptable for archive/export tooling, but operators should not read it as a live
	// mutation-consistent transactional snapshot.
	bundle, err := loadBundle(ctx, pool, archiveTimestamp, hotRetentionDays, job.JobID)
	if err != nil {
		return archiveBundle{}, exportedBundle{}, err
	}

	encoded, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return archiveBundle{}, exportedBundle{}, err
	}

	fileName := sanitizeBundleFileName(job.JobID) + ".json"
	relativePath := filepath.Join("bundles", fileName)
	targetPath := filepath.Join(evidenceRoot, relativePath)
	if err := os.WriteFile(targetPath, append(encoded, '\n'), 0o644); err != nil {
		return archiveBundle{}, exportedBundle{}, err
	}

	sum := sha256.Sum256(append(encoded, '\n'))
	exported := exportedBundle{
		JobID:          job.JobID,
		CurrentState:   job.CurrentState,
		UpdatedAtUTC:   job.UpdatedAt.UTC().Format(time.RFC3339),
		RelativePath:   relativePath,
		SHA256:         hex.EncodeToString(sum[:]),
		TransitionRows: len(bundle.Transitions),
		TaskRows:       len(bundle.Tasks),
		SequenceRows:   len(bundle.Sequences),
		LastSequenceID: bundle.Job.LastSequenceID,
	}
	return bundle, exported, nil
}

func loadBundle(ctx context.Context, pool *pgxpool.Pool, archiveTimestamp time.Time, hotRetentionDays int, jobID string) (archiveBundle, error) {
	bundle := archiveBundle{
		SchemaVersion:    archiveSchemaVersion,
		ArchiveTimestamp: archiveTimestamp.Format(time.RFC3339),
		HotRetentionDays: hotRetentionDays,
	}

	var job durableJobRow
	if err := pool.QueryRow(ctx, `
		SELECT job_id, name, current_state, last_sequence_id, created_at, updated_at
		FROM scheduler_durable_jobs
		WHERE job_id = $1
	`, jobID).Scan(
		&job.JobID,
		&job.Name,
		&job.CurrentState,
		&job.LastSequenceID,
		(*timestampString)(&job.CreatedAtUTC),
		(*timestampString)(&job.UpdatedAtUTC),
	); err != nil {
		if err == pgx.ErrNoRows {
			return archiveBundle{}, fmt.Errorf("durable job missing")
		}
		return archiveBundle{}, err
	}
	bundle.Job = job

	tasks, err := queryTasks(ctx, pool, jobID)
	if err != nil {
		return archiveBundle{}, err
	}
	bundle.Tasks = tasks

	transitions, err := queryTransitions(ctx, pool, jobID)
	if err != nil {
		return archiveBundle{}, err
	}
	bundle.Transitions = transitions

	sequences, err := querySequences(ctx, pool, jobID)
	if err != nil {
		return archiveBundle{}, err
	}
	bundle.Sequences = sequences

	return bundle, nil
}

func queryTasks(ctx context.Context, pool *pgxpool.Pool, jobID string) ([]durableTaskRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT task_id, job_id, stage_id, node_id, agent_id, agent_name, input_json, partition_key, current_state, last_attempt, updated_at
		FROM scheduler_durable_tasks
		WHERE job_id = $1
		ORDER BY task_id ASC
	`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []durableTaskRow
	for rows.Next() {
		var task durableTaskRow
		if err := rows.Scan(
			&task.TaskID,
			&task.JobID,
			&task.StageID,
			&task.NodeID,
			&task.AgentID,
			&task.AgentName,
			&task.InputJSON,
			&task.PartitionKey,
			&task.CurrentState,
			&task.LastAttempt,
			(*timestampString)(&task.UpdatedAtUTC),
		); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func queryTransitions(ctx context.Context, pool *pgxpool.Pool, jobID string) ([]transitionRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT job_id, sequence_id, entity_type, transition, task_id, previous_state, new_state, node_id, attempt, error_summary, occurred_at
		FROM scheduler_job_transitions
		WHERE job_id = $1
		ORDER BY sequence_id ASC
	`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transitions []transitionRow
	for rows.Next() {
		var transition transitionRow
		if err := rows.Scan(
			&transition.JobID,
			&transition.SequenceID,
			&transition.EntityType,
			&transition.Transition,
			&transition.TaskID,
			&transition.PreviousState,
			&transition.NewState,
			&transition.NodeID,
			&transition.Attempt,
			&transition.ErrorSummary,
			(*timestampString)(&transition.OccurredAtUTC),
		); err != nil {
			return nil, err
		}
		transitions = append(transitions, transition)
	}
	return transitions, rows.Err()
}

func querySequences(ctx context.Context, pool *pgxpool.Pool, jobID string) ([]jobSequenceRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT job_id, last_sequence_id, updated_at
		FROM scheduler_job_sequences
		WHERE job_id = $1
	`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sequences []jobSequenceRow
	for rows.Next() {
		var seq jobSequenceRow
		if err := rows.Scan(
			&seq.JobID,
			&seq.LastSequenceID,
			(*timestampString)(&seq.UpdatedAtUTC),
		); err != nil {
			return nil, err
		}
		sequences = append(sequences, seq)
	}
	return sequences, rows.Err()
}

type timestampString string

func (t *timestampString) Scan(src interface{}) error {
	switch v := src.(type) {
	case time.Time:
		*t = timestampString(v.UTC().Format(time.RFC3339))
		return nil
	default:
		return fmt.Errorf("unsupported timestamp source %T", src)
	}
}

func sanitizeBundleFileName(jobID string) string {
	safe := invalidBundleName.ReplaceAllString(strings.TrimSpace(jobID), "_")
	if safe == "" {
		return "job"
	}
	return safe
}

func shouldVerifyBundles(cfg exportConfig) bool {
	return cfg.VerifyBundles && !cfg.DryRun
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

func verifyManifest(evidenceRoot string, manifest exportManifest) (verification, error) {
	result := verification{
		Enabled: true,
		Verified: true,
		VerifiedAtUTC: time.Now().UTC().Format(time.RFC3339),
		ManifestChecksPassed: true,
	}
	for _, bundleMeta := range manifest.Bundles {
		targetPath := filepath.Join(evidenceRoot, bundleMeta.RelativePath)
		payload, err := os.ReadFile(targetPath)
		if err != nil {
			return verification{}, fmt.Errorf("read bundle %s: %w", bundleMeta.JobID, err)
		}
		result.VerifiedBytes += int64(len(payload))

		sum := sha256.Sum256(payload)
		if actual := hex.EncodeToString(sum[:]); actual != bundleMeta.SHA256 {
			return verification{}, fmt.Errorf("bundle %s checksum mismatch: got %s want %s", bundleMeta.JobID, actual, bundleMeta.SHA256)
		}

		var bundle archiveBundle
		if err := json.Unmarshal(payload, &bundle); err != nil {
			return verification{}, fmt.Errorf("decode bundle %s: %w", bundleMeta.JobID, err)
		}
		if bundle.SchemaVersion != archiveSchemaVersion {
			return verification{}, fmt.Errorf("bundle %s schema_version=%q", bundleMeta.JobID, bundle.SchemaVersion)
		}
		if bundle.Job.JobID != bundleMeta.JobID {
			return verification{}, fmt.Errorf("bundle %s job_id mismatch: payload=%q", bundleMeta.JobID, bundle.Job.JobID)
		}
		if bundle.Job.CurrentState != bundleMeta.CurrentState {
			return verification{}, fmt.Errorf("bundle %s current_state mismatch: payload=%q manifest=%q", bundleMeta.JobID, bundle.Job.CurrentState, bundleMeta.CurrentState)
		}
		if len(bundle.Tasks) != bundleMeta.TaskRows {
			return verification{}, fmt.Errorf("bundle %s task_rows mismatch: payload=%d manifest=%d", bundleMeta.JobID, len(bundle.Tasks), bundleMeta.TaskRows)
		}
		if len(bundle.Transitions) != bundleMeta.TransitionRows {
			return verification{}, fmt.Errorf("bundle %s transition_rows mismatch: payload=%d manifest=%d", bundleMeta.JobID, len(bundle.Transitions), bundleMeta.TransitionRows)
		}
		if len(bundle.Sequences) != bundleMeta.SequenceRows {
			return verification{}, fmt.Errorf("bundle %s sequence_rows mismatch: payload=%d manifest=%d", bundleMeta.JobID, len(bundle.Sequences), bundleMeta.SequenceRows)
		}
		if bundle.Job.LastSequenceID != bundleMeta.LastSequenceID {
			return verification{}, fmt.Errorf("bundle %s last_sequence_id mismatch: payload=%d manifest=%d", bundleMeta.JobID, bundle.Job.LastSequenceID, bundleMeta.LastSequenceID)
		}
		result.VerifiedBundles++
	}
	return result, nil
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-transition-archive-export-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func writeResults(evidenceRoot string, result exportResult) error {
	manifestPath := filepath.Join(evidenceRoot, "manifest.json")
	manifestJSON, err := json.MarshalIndent(result.Manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, append(manifestJSON, '\n'), 0o644); err != nil {
		return err
	}

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(evidenceRoot, "result.json"), append(resultJSON, '\n'), 0o644); err != nil {
		return err
	}

	summary := fmt.Sprintf(
		"status=PASS\n"+
			"evidence_root=%s\n"+
			"manifest_json=%s\n"+
			"selected_jobs=%d\n"+
			"exported_jobs=%d\n"+
			"verify_bundles=%t\n"+
			"verified_bundles=%d\n"+
			"verified_bytes=%d\n",
		evidenceRoot,
		manifestPath,
		result.Manifest.SelectedJobs,
		result.Manifest.ExportedJobs,
		result.Manifest.Verification.Enabled,
		result.Manifest.Verification.VerifiedBundles,
		result.Manifest.Verification.VerifiedBytes,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
