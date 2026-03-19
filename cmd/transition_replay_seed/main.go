package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/scheduler"
)

type seedConfig struct {
	DatabaseURL  string        `json:"database_url"`
	JobCount     int           `json:"job_count"`
	TasksPerJob  int           `json:"tasks_per_job"`
	Profile      string        `json:"profile"`
	JobIDPrefix  string        `json:"job_id_prefix"`
	EvidenceRoot string        `json:"evidence_root"`
	Timeout      time.Duration `json:"timeout"`
}

type seedResult struct {
	TimestampUTC string       `json:"timestamp_utc"`
	Config       seedConfig   `json:"config"`
	Seeded       seededCounts `json:"seeded"`
}

type seededCounts struct {
	Jobs              int `json:"jobs"`
	Tasks             int `json:"tasks"`
	Transitions       int `json:"transitions"`
	QueuedJobs        int `json:"queued_jobs"`
	RunningJobs       int `json:"running_jobs"`
	AwaitingHumanJobs int `json:"awaiting_human_jobs"`
}

type syntheticState string

const (
	syntheticQueued        syntheticState = "queued"
	syntheticRunning       syntheticState = "running"
	syntheticAwaitingHuman syntheticState = "awaiting_human"
)

func main() {
	cfg := seedConfig{}
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for scheduler transition store")
	flag.IntVar(&cfg.JobCount, "jobs", 60, "number of unfinished jobs to seed")
	flag.IntVar(&cfg.TasksPerJob, "tasks-per-job", 4, "number of tasks per seeded unfinished job")
	flag.StringVar(&cfg.Profile, "profile", "mixed", "unfinished-job profile: queued|running|awaiting_human|mixed")
	flag.StringVar(&cfg.JobIDPrefix, "job-id-prefix", defaultJobPrefix(), "job_id prefix for seeded jobs")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write seed evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 2*time.Minute, "overall seed timeout")
	flag.Parse()

	if cfg.DatabaseURL == "" {
		exitf("database-url is required")
	}
	if cfg.JobCount <= 0 || cfg.TasksPerJob <= 0 {
		exitf("jobs and tasks-per-job must be > 0")
	}
	if cfg.Profile != "queued" && cfg.Profile != "running" && cfg.Profile != "awaiting_human" && cfg.Profile != "mixed" {
		exitf("unsupported profile %q", cfg.Profile)
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

	base := time.Now().UTC().Add(-time.Hour)
	result := seedResult{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Config:       cfg,
	}

	for i := 0; i < cfg.JobCount; i++ {
		jobID := fmt.Sprintf("%s-%06d", cfg.JobIDPrefix, i)
		state := profileStateForJob(i, cfg.Profile)
		counts, err := seedUnfinishedJob(ctx, pool, jobID, state, cfg.TasksPerJob, base.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			exitf("seed unfinished job %s: %v", jobID, err)
		}
		result.Seeded.Jobs++
		result.Seeded.Tasks += counts.Tasks
		result.Seeded.Transitions += counts.Transitions
		switch state {
		case syntheticQueued:
			result.Seeded.QueuedJobs++
		case syntheticRunning:
			result.Seeded.RunningJobs++
		case syntheticAwaitingHuman:
			result.Seeded.AwaitingHumanJobs++
		}
	}

	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: transition replay seed completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("job_id_prefix=%s\n", cfg.JobIDPrefix)
	fmt.Printf("jobs=%d tasks=%d transitions=%d\n", result.Seeded.Jobs, result.Seeded.Tasks, result.Seeded.Transitions)
}

func profileStateForJob(index int, profile string) syntheticState {
	switch profile {
	case "queued":
		return syntheticQueued
	case "running":
		return syntheticRunning
	case "awaiting_human":
		return syntheticAwaitingHuman
	default:
		switch index % 3 {
		case 0:
			return syntheticQueued
		case 1:
			return syntheticRunning
		default:
			return syntheticAwaitingHuman
		}
	}
}

func seedUnfinishedJob(ctx context.Context, pool *pgxpool.Pool, jobID string, state syntheticState, tasksPerJob int, createdAt time.Time) (seededCounts, error) {
	counts := seededCounts{Tasks: tasksPerJob}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return counts, fmt.Errorf("begin seed transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var lastSequenceID uint64
	appendTransition := func(record scheduler.TransitionRecord) error {
		lastSequenceID++
		record.SequenceID = lastSequenceID
		if err := appendTransitionRecord(ctx, tx, record); err != nil {
			return err
		}
		counts.Transitions++
		return nil
	}

	currentTaskState := scheduler.TaskStatePending
	currentJobState := scheduler.JobStateQueued
	updatedAt := createdAt

	for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
		taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
		inputJSON, err := toJSON(agent.AgentInput{Instruction: fmt.Sprintf("replay seed task %d", taskIndex)})
		if err != nil {
			return counts, fmt.Errorf("marshal task input: %w", err)
		}
		if err := upsertTaskRecord(ctx, tx, scheduler.DurableTaskRecord{
			TaskID:       taskID,
			JobID:        jobID,
			StageID:      "stage-1",
			NodeID:       "",
			AgentID:      fmt.Sprintf("agent-%02d", taskIndex%8),
			AgentName:    fmt.Sprintf("agent-%02d", taskIndex%8),
			InputJSON:    inputJSON,
			PartitionKey: fmt.Sprintf("partition-%02d", taskIndex%4),
			CurrentState: scheduler.TaskStatePending,
			LastAttempt:  0,
			UpdatedAt:    createdAt,
		}); err != nil {
			return counts, err
		}
	}

	if err := appendTransition(scheduler.TransitionRecord{
		EntityType: scheduler.TransitionEntityJob,
		Transition: scheduler.TransitionJobSubmitted,
		JobID:      jobID,
		NewState:   string(scheduler.JobStateSubmitted),
		OccurredAt: createdAt,
	}); err != nil {
		return counts, err
	}

	for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
		taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
		if err := appendTransition(scheduler.TransitionRecord{
			EntityType: scheduler.TransitionEntityTask,
			Transition: scheduler.TransitionTaskCreated,
			JobID:      jobID,
			TaskID:     taskID,
			NewState:   string(scheduler.TaskStatePending),
			OccurredAt: createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond),
		}); err != nil {
			return counts, err
		}
		updatedAt = createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond)
	}

	if err := appendTransition(scheduler.TransitionRecord{
		EntityType:    scheduler.TransitionEntityJob,
		Transition:    scheduler.TransitionJobQueued,
		JobID:         jobID,
		PreviousState: string(scheduler.JobStateSubmitted),
		NewState:      string(scheduler.JobStateQueued),
		OccurredAt:    createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond),
	}); err != nil {
		return counts, err
	}
	updatedAt = createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond)

	switch state {
	case syntheticRunning, syntheticAwaitingHuman:
		currentTaskState = scheduler.TaskStateRunning
		currentJobState = scheduler.JobStateRunning
		for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
			taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
			nodeID := fmt.Sprintf("worker-%02d", taskIndex%16)
			if err := appendTransition(scheduler.TransitionRecord{
				EntityType:    scheduler.TransitionEntityTask,
				Transition:    scheduler.TransitionTaskDispatched,
				JobID:         jobID,
				TaskID:        taskID,
				PreviousState: string(scheduler.TaskStatePending),
				NewState:      string(scheduler.TaskStateDispatched),
				NodeID:        nodeID,
				Attempt:       1,
				OccurredAt:    createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond),
			}); err != nil {
				return counts, err
			}
			if err := appendTransition(scheduler.TransitionRecord{
				EntityType:    scheduler.TransitionEntityTask,
				Transition:    scheduler.TransitionTaskRunning,
				JobID:         jobID,
				TaskID:        taskID,
				PreviousState: string(scheduler.TaskStateDispatched),
				NewState:      string(scheduler.TaskStateRunning),
				NodeID:        nodeID,
				Attempt:       1,
				OccurredAt:    createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond),
			}); err != nil {
				return counts, err
			}
		}
		if err := appendTransition(scheduler.TransitionRecord{
			EntityType:    scheduler.TransitionEntityJob,
			Transition:    scheduler.TransitionJobRunning,
			JobID:         jobID,
			PreviousState: string(scheduler.JobStateQueued),
			NewState:      string(scheduler.JobStateRunning),
			OccurredAt:    createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond),
		}); err != nil {
			return counts, err
		}
		updatedAt = createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond)
	}

	if state == syntheticAwaitingHuman {
		currentJobState = scheduler.JobStateAwaitingHuman
		if err := appendTransition(scheduler.TransitionRecord{
			EntityType:    scheduler.TransitionEntityJob,
			Transition:    scheduler.TransitionJobAwaitingHuman,
			JobID:         jobID,
			PreviousState: string(scheduler.JobStateRunning),
			NewState:      string(scheduler.JobStateAwaitingHuman),
			OccurredAt:    createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond),
		}); err != nil {
			return counts, err
		}
		updatedAt = createdAt.Add(time.Duration(lastSequenceID) * time.Millisecond)
	}

	for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
		taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
		inputJSON, err := toJSON(agent.AgentInput{Instruction: fmt.Sprintf("replay seed task %d", taskIndex)})
		if err != nil {
			return counts, fmt.Errorf("marshal task input: %w", err)
		}
		taskRecord := scheduler.DurableTaskRecord{
			TaskID:       taskID,
			JobID:        jobID,
			StageID:      "stage-1",
			NodeID:       fmt.Sprintf("worker-%02d", taskIndex%16),
			AgentID:      fmt.Sprintf("agent-%02d", taskIndex%8),
			AgentName:    fmt.Sprintf("agent-%02d", taskIndex%8),
			InputJSON:    inputJSON,
			PartitionKey: fmt.Sprintf("partition-%02d", taskIndex%4),
			CurrentState: currentTaskState,
			LastAttempt:  1,
			UpdatedAt:    updatedAt,
		}
		if currentTaskState == scheduler.TaskStatePending {
			taskRecord.NodeID = ""
			taskRecord.LastAttempt = 0
		}
		if err := upsertTaskRecord(ctx, tx, taskRecord); err != nil {
			return counts, err
		}
	}

	if err := upsertJobRecord(ctx, tx, scheduler.DurableJobRecord{
		JobID:          jobID,
		Name:           "replay-validation-job",
		CurrentState:   currentJobState,
		LastSequenceID: lastSequenceID,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}); err != nil {
		return counts, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO scheduler_job_sequences (job_id, last_sequence_id, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (job_id) DO UPDATE SET
			last_sequence_id = EXCLUDED.last_sequence_id,
			updated_at = EXCLUDED.updated_at
	`, jobID, int64(lastSequenceID), updatedAt); err != nil {
		return counts, fmt.Errorf("upsert job sequence: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return counts, fmt.Errorf("commit seed transaction: %w", err)
	}
	return counts, nil
}

func toJSON(v interface{}) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func defaultJobPrefix() string {
	return "replay-archive-seed-" + time.Now().UTC().Format("20060102T150405Z")
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-transition-replay-seed-%s", time.Now().UTC().Format("20060102T150405Z")))
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
			"queued_jobs=%d\n"+
			"running_jobs=%d\n"+
			"awaiting_human_jobs=%d\n",
		evidenceRoot,
		result.Config.JobIDPrefix,
		result.Seeded.Jobs,
		result.Seeded.Tasks,
		result.Seeded.Transitions,
		result.Seeded.QueuedJobs,
		result.Seeded.RunningJobs,
		result.Seeded.AwaitingHumanJobs,
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
