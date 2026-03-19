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
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/retention"
	"github.com/seyi/dagens/pkg/scheduler"
)

type seedConfig struct {
	DatabaseURL    string        `json:"database_url"`
	JobCount       int           `json:"job_count"`
	TasksPerJob    int           `json:"tasks_per_job"`
	AgeDays        int           `json:"age_days"`
	ReferenceTime  string        `json:"reference_time,omitempty"`
	PerJobOffset   time.Duration `json:"per_job_offset"`
	JobIDPrefix    string        `json:"job_id_prefix"`
	EvidenceRoot   string        `json:"evidence_root"`
	Timeout        time.Duration `json:"timeout"`
}

type seedResult struct {
	TimestampUTC string            `json:"timestamp_utc"`
	Config       seedConfig        `json:"config"`
	Seeded       seedSummary       `json:"seeded"`
	Validation   validationSummary `json:"validation"`
}

type seedSummary struct {
	Jobs          int `json:"jobs"`
	Tasks         int `json:"tasks"`
	Transitions   int `json:"transitions"`
	SequenceRows  int `json:"sequence_rows"`
	SucceededJobs int `json:"succeeded_jobs"`
	FailedJobs    int `json:"failed_jobs"`
	TargetAgeDays int `json:"target_age_days"`
}

type validationSummary struct {
	Mode                         string `json:"mode"`
	EligibleJobsObserved           int64 `json:"eligible_jobs_observed"`
	EligibleTransitionRowsObserved int64 `json:"eligible_transition_rows_observed"`
	EligibleTaskRowsObserved       int64 `json:"eligible_task_rows_observed"`
	EligibleSequenceRowsObserved   int64 `json:"eligible_sequence_rows_observed"`
}

func main() {
	cfg := seedConfig{}
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for scheduler transition store")
	flag.IntVar(&cfg.JobCount, "jobs", 250, "number of eligible terminal jobs to seed")
	flag.IntVar(&cfg.TasksPerJob, "tasks-per-job", 4, "number of tasks per seeded terminal job")
	flag.IntVar(&cfg.AgeDays, "age-days", 21, "age in days to assign to seeded terminal jobs")
	flag.StringVar(&cfg.ReferenceTime, "reference-time", "", "optional RFC3339 timestamp used as the base time for deterministic retention validation")
	flag.DurationVar(&cfg.PerJobOffset, "per-job-offset", time.Second, "time delta added between seeded jobs; use 0s for a fixed timestamp cohort")
	flag.StringVar(&cfg.JobIDPrefix, "job-id-prefix", defaultJobPrefix(), "job_id prefix for seeded jobs")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write seed evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 2*time.Minute, "overall seed timeout")
	flag.Parse()

	if cfg.DatabaseURL == "" {
		exitf("database-url is required")
	}
	if cfg.JobCount <= 0 || cfg.TasksPerJob <= 0 || cfg.AgeDays <= 0 {
		exitf("jobs, tasks-per-job, and age-days must be > 0")
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

	if _, err := scheduler.NewPostgresTransitionStore(ctx, pool); err != nil {
		exitf("create postgres transition store: %v", err)
	}

	now := time.Now().UTC()
	if strings.TrimSpace(cfg.ReferenceTime) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(cfg.ReferenceTime))
		if err != nil {
			exitf("parse reference-time: %v", err)
		}
		now = parsed.UTC()
	}
	terminalAt := now.Add(-time.Duration(cfg.AgeDays) * 24 * time.Hour)
	result := seedResult{
		TimestampUTC: now.Format(time.RFC3339),
		Config:       cfg,
	}

	for i := 0; i < cfg.JobCount; i++ {
		jobID := fmt.Sprintf("%s-%06d", cfg.JobIDPrefix, i)
		finalState := scheduler.JobStateSucceeded
		taskFinalState := scheduler.TaskStateSucceeded
		if i%5 == 0 {
			finalState = scheduler.JobStateFailed
			taskFinalState = scheduler.TaskStateFailed
			result.Seeded.FailedJobs++
		} else {
			result.Seeded.SucceededJobs++
		}
		if err := seedTerminalJob(ctx, pool, jobID, cfg.TasksPerJob, terminalAt.Add(time.Duration(i)*cfg.PerJobOffset), finalState, taskFinalState); err != nil {
			exitf("seed terminal job %s: %v", jobID, err)
		}
		result.Seeded.Jobs++
		result.Seeded.Tasks += cfg.TasksPerJob
		result.Seeded.Transitions += (3 + (2 * cfg.TasksPerJob) + 1)
		result.Seeded.SequenceRows++
	}
	result.Seeded.TargetAgeDays = cfg.AgeDays

	result.Validation.Mode = "live_now_snapshot"
	if strings.TrimSpace(cfg.ReferenceTime) == "" {
		snapshot, err := retention.QueryVisibilitySnapshot(ctx, pool, cfg.AgeDays)
		if err != nil {
			exitf("post-seed validation snapshot: %v", err)
		}
		result.Validation = validationSummary{
			Mode:                         "live_now_snapshot",
			EligibleJobsObserved:           snapshot.EligibleArchive.Jobs,
			EligibleTransitionRowsObserved: snapshot.EligibleArchive.TransitionRows,
			EligibleTaskRowsObserved:       snapshot.EligibleArchive.TaskRows,
			EligibleSequenceRowsObserved:   snapshot.EligibleArchive.SequenceRows,
		}
	} else {
		result.Validation.Mode = "skipped_reference_time"
	}

	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: transition retention seed completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("job_id_prefix=%s\n", cfg.JobIDPrefix)
	fmt.Printf("jobs=%d tasks=%d transitions=%d sequence_rows=%d\n",
		result.Seeded.Jobs,
		result.Seeded.Tasks,
		result.Seeded.Transitions,
		result.Seeded.SequenceRows,
	)
	fmt.Printf("eligible_jobs_observed=%d eligible_transition_rows_observed=%d\n",
		result.Validation.EligibleJobsObserved,
		result.Validation.EligibleTransitionRowsObserved,
	)
}

func seedTerminalJob(ctx context.Context, pool *pgxpool.Pool, jobID string, tasksPerJob int, terminalAt time.Time, finalJobState scheduler.JobLifecycleState, finalTaskState scheduler.TaskLifecycleState) error {
	createdAt := terminalAt.Add(-10 * time.Minute)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var lastSequenceID uint64
	appendTransition := func(record scheduler.TransitionRecord) error {
		lastSequenceID++
		record.SequenceID = lastSequenceID
		return appendTransitionRecord(ctx, tx, record)
	}

	if err := appendTransition(scheduler.TransitionRecord{
		EntityType: scheduler.TransitionEntityJob, Transition: scheduler.TransitionJobSubmitted,
		JobID: jobID, NewState: string(scheduler.JobStateSubmitted), OccurredAt: createdAt,
	}); err != nil {
		return err
	}
	if err := appendTransition(scheduler.TransitionRecord{
		EntityType: scheduler.TransitionEntityJob, Transition: scheduler.TransitionJobQueued,
		JobID: jobID, PreviousState: string(scheduler.JobStateSubmitted), NewState: string(scheduler.JobStateQueued), OccurredAt: createdAt.Add(time.Second),
	}); err != nil {
		return err
	}
	if err := appendTransition(scheduler.TransitionRecord{
		EntityType: scheduler.TransitionEntityJob, Transition: scheduler.TransitionJobRunning,
		JobID: jobID, PreviousState: string(scheduler.JobStateQueued), NewState: string(scheduler.JobStateRunning), OccurredAt: createdAt.Add(2 * time.Second),
	}); err != nil {
		return err
	}

	for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
		taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
		createdRecord := scheduler.TransitionRecord{
			EntityType: scheduler.TransitionEntityTask, Transition: scheduler.TransitionTaskCreated,
			JobID: jobID, TaskID: taskID, NewState: string(scheduler.TaskStatePending), OccurredAt: createdAt.Add(time.Duration(3+taskIndex) * time.Second),
		}
		if err := appendTransition(createdRecord); err != nil {
			return err
		}
		finalTransition := scheduler.TransitionTaskSucceeded
		if finalTaskState == scheduler.TaskStateFailed {
			finalTransition = scheduler.TransitionTaskFailed
		}
		if err := appendTransition(scheduler.TransitionRecord{
			EntityType: scheduler.TransitionEntityTask, Transition: finalTransition,
			JobID: jobID, TaskID: taskID, PreviousState: string(scheduler.TaskStatePending), NewState: string(finalTaskState),
			OccurredAt: createdAt.Add(time.Duration(3+tasksPerJob+taskIndex) * time.Second),
		}); err != nil {
			return err
		}
		inputJSON, err := toJSON(agent.AgentInput{Instruction: fmt.Sprintf("retention seed task %d", taskIndex)})
		if err != nil {
			return fmt.Errorf("marshal task input: %w", err)
		}
		if err := upsertTaskRecord(ctx, tx, scheduler.DurableTaskRecord{
			TaskID:       taskID,
			JobID:        jobID,
			StageID:      "stage-1",
			NodeID:       fmt.Sprintf("worker-%02d", taskIndex%16),
			AgentID:      fmt.Sprintf("agent-%02d", taskIndex%8),
			AgentName:    fmt.Sprintf("agent-%02d", taskIndex%8),
			InputJSON:    inputJSON,
			PartitionKey: fmt.Sprintf("partition-%02d", taskIndex%4),
			CurrentState: finalTaskState,
			LastAttempt:  1,
			UpdatedAt:    terminalAt,
		}); err != nil {
			return err
		}
	}

	jobFinalTransition := scheduler.TransitionJobSucceeded
	if finalJobState == scheduler.JobStateFailed {
		jobFinalTransition = scheduler.TransitionJobFailed
	}
	if err := appendTransition(scheduler.TransitionRecord{
		EntityType: scheduler.TransitionEntityJob, Transition: jobFinalTransition,
		JobID: jobID, PreviousState: string(scheduler.JobStateRunning), NewState: string(finalJobState), OccurredAt: terminalAt,
	}); err != nil {
		return err
	}

	if err := upsertJobRecord(ctx, tx, scheduler.DurableJobRecord{
		JobID:          jobID,
		Name:           "retention-validation-job",
		CurrentState:   finalJobState,
		LastSequenceID: lastSequenceID,
		CreatedAt:      createdAt,
		UpdatedAt:      terminalAt,
	}); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO scheduler_job_sequences (job_id, last_sequence_id, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (job_id) DO UPDATE SET
			last_sequence_id = EXCLUDED.last_sequence_id,
			updated_at = EXCLUDED.updated_at
	`, jobID, int64(lastSequenceID), terminalAt); err != nil {
		return fmt.Errorf("upsert job sequence: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed transaction: %w", err)
	}
	return nil
}

func toJSON(v interface{}) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func defaultJobPrefix() string {
	return "retention-archive-seed-" + time.Now().UTC().Format("20060102T150405Z")
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-transition-retention-seed-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func writeResults(evidenceRoot string, result seedResult) error {
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
			"job_id_prefix=%s\n"+
			"jobs=%d\n"+
			"tasks=%d\n"+
			"transitions=%d\n"+
			"sequence_rows=%d\n"+
			"validation_mode=%s\n"+
			"eligible_jobs_observed=%d\n"+
			"eligible_transition_rows_observed=%d\n",
		evidenceRoot,
		result.Config.JobIDPrefix,
		result.Seeded.Jobs,
		result.Seeded.Tasks,
		result.Seeded.Transitions,
		result.Seeded.SequenceRows,
		result.Validation.Mode,
		result.Validation.EligibleJobsObserved,
		result.Validation.EligibleTransitionRowsObserved,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}

func appendTransitionRecord(ctx context.Context, tx pgx.Tx, record scheduler.TransitionRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO scheduler_job_transitions (
			job_id, sequence_id, entity_type, transition, task_id, previous_state,
			new_state, node_id, attempt, error_summary, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`,
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
	return nil
}

func upsertJobRecord(ctx context.Context, tx pgx.Tx, job scheduler.DurableJobRecord) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO scheduler_durable_jobs (job_id, name, current_state, last_sequence_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (job_id) DO UPDATE SET
			name = EXCLUDED.name,
			current_state = EXCLUDED.current_state,
			last_sequence_id = GREATEST(scheduler_durable_jobs.last_sequence_id, EXCLUDED.last_sequence_id),
			updated_at = EXCLUDED.updated_at
	`,
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

func upsertTaskRecord(ctx context.Context, tx pgx.Tx, task scheduler.DurableTaskRecord) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO scheduler_durable_tasks (task_id, job_id, stage_id, node_id, agent_id, agent_name, input_json, partition_key, current_state, last_attempt, updated_at)
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
			updated_at = EXCLUDED.updated_at
	`,
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

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
