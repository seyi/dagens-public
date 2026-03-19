package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/scheduler"
)

type benchmarkConfig struct {
	StoreBackend      string
	DatabaseURL       string
	JobCount          int
	TasksPerJob       int
	Profile           string
	RecoveryBatchSize int
	ResumeRecovered   bool
	EvidenceRoot      string
	RecoveryTimeout   time.Duration
}

type benchmarkResult struct {
	TimestampUTC string           `json:"timestamp_utc"`
	Config       benchmarkConfig  `json:"config"`
	Seeded       seededCounts     `json:"seeded"`
	Timings      benchmarkTimings `json:"timings"`
	Replay       replaySummary    `json:"replay"`
	Recovery     recoverySummary  `json:"recovery"`
	Memory       memorySummary    `json:"memory"`
}

type seededCounts struct {
	Jobs        int `json:"jobs"`
	Tasks       int `json:"tasks"`
	Transitions int `json:"transitions"`
}

type benchmarkTimings struct {
	StoreSetupDurationMS float64 `json:"store_setup_duration_ms"`
	SeedDurationMS     float64 `json:"seed_duration_ms"`
	ReplayDurationMS   float64 `json:"replay_duration_ms"`
	RecoveryDurationMS float64 `json:"recovery_duration_ms"`
	TotalDurationMS    float64 `json:"total_duration_ms"`
}

type replaySummary struct {
	RecoveredJobs int `json:"recovered_jobs"`
}

type recoverySummary struct {
	JobsInMemory int `json:"jobs_in_memory"`
	QueuedDepth  int `json:"queued_depth"`
}

type memorySummary struct {
	AllocBeforeBytes uint64 `json:"alloc_before_bytes"`
	AllocAfterSeed   uint64 `json:"alloc_after_seed_bytes"`
	AllocAfterReplay uint64 `json:"alloc_after_replay_bytes"`
	AllocAfterRecov  uint64 `json:"alloc_after_recovery_bytes"`
}

func main() {
	cfg := benchmarkConfig{}
	flag.StringVar(&cfg.StoreBackend, "store-backend", "memory", "benchmark store backend: memory|postgres")
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for postgres-backed benchmark mode")
	flag.IntVar(&cfg.JobCount, "jobs", 2000, "number of unfinished jobs to seed")
	flag.IntVar(&cfg.TasksPerJob, "tasks-per-job", 8, "number of tasks per synthetic job")
	flag.StringVar(&cfg.Profile, "profile", "mixed", "synthetic lifecycle profile: queued|running|awaiting_human|mixed")
	flag.IntVar(&cfg.RecoveryBatchSize, "recovery-batch-size", 256, "scheduler recovery batch size")
	flag.BoolVar(&cfg.ResumeRecovered, "resume-recovered-queued-jobs", false, "enable queued-job auto-resume during recovery")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write benchmark evidence")
	flag.DurationVar(&cfg.RecoveryTimeout, "recovery-timeout", 10*time.Minute, "timeout for replay/recovery phases")
	flag.Parse()

	if cfg.JobCount <= 0 {
		exitf("jobs must be > 0")
	}
	if cfg.TasksPerJob <= 0 {
		exitf("tasks-per-job must be > 0")
	}
	if cfg.StoreBackend != "memory" && cfg.StoreBackend != "postgres" {
		exitf("unsupported store-backend %q", cfg.StoreBackend)
	}
	if cfg.StoreBackend == "postgres" && cfg.DatabaseURL == "" {
		exitf("database-url is required for postgres store-backend")
	}
	if cfg.Profile != "queued" && cfg.Profile != "running" && cfg.Profile != "awaiting_human" && cfg.Profile != "mixed" {
		exitf("unsupported profile %q", cfg.Profile)
	}

	if err := os.MkdirAll(cfg.EvidenceRoot, 0o755); err != nil {
		exitf("create evidence root: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.RecoveryTimeout)
	defer cancel()

	result := benchmarkResult{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Config:       cfg,
	}

	setupStart := time.Now()
	store, closeStore, err := mustCreateStore(ctx, cfg)
	if err != nil {
		exitf("create benchmark store: %v", err)
	}
	defer closeStore()
	result.Timings.StoreSetupDurationMS = durationMillis(time.Since(setupStart))

	start := time.Now()

	result.Memory.AllocBeforeBytes = currentAllocBytes()

	seedStart := time.Now()
	seeded, err := seedSyntheticHistory(ctx, store, cfg)
	if err != nil {
		exitf("seed synthetic history: %v", err)
	}
	result.Seeded = seeded
	result.Timings.SeedDurationMS = durationMillis(time.Since(seedStart))
	result.Memory.AllocAfterSeed = currentAllocBytes()

	replayStart := time.Now()
	replayed, err := scheduler.ReplayStateFromStore(ctx, store)
	if err != nil {
		exitf("replay state from store: %v", err)
	}
	result.Timings.ReplayDurationMS = durationMillis(time.Since(replayStart))
	result.Replay.RecoveredJobs = len(replayed)
	result.Memory.AllocAfterReplay = currentAllocBytes()

	s := scheduler.NewScheduler(nil, nil)
	schedCfg := scheduler.DefaultSchedulerConfig()
	schedCfg.RecoveryBatchSize = cfg.RecoveryBatchSize
	schedCfg.EnableResumeRecoveredQueuedJobs = cfg.ResumeRecovered
	s = scheduler.NewSchedulerWithConfig(nil, nil, schedCfg)
	if err := s.SetTransitionStore(store); err != nil {
		exitf("set transition store: %v", err)
	}

	recoveryStart := time.Now()
	if err := s.RecoverFromTransitions(ctx); err != nil {
		exitf("recover from transitions: %v", err)
	}
	result.Timings.RecoveryDurationMS = durationMillis(time.Since(recoveryStart))
	result.Memory.AllocAfterRecov = currentAllocBytes()

	allJobs := s.GetAllJobs()
	result.Recovery.JobsInMemory = len(allJobs)
	result.Recovery.QueuedDepth = countQueuedJobs(allJobs)
	result.Timings.TotalDurationMS = durationMillis(time.Since(start))

	if err := writeResultFiles(cfg.EvidenceRoot, result); err != nil {
		exitf("write result files: %v", err)
	}

	fmt.Printf("PASS: replay/recovery benchmark completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("seeded_jobs=%d seeded_tasks=%d seeded_transitions=%d\n", result.Seeded.Jobs, result.Seeded.Tasks, result.Seeded.Transitions)
	fmt.Printf("replay_duration_ms=%.2f recovery_duration_ms=%.2f total_duration_ms=%.2f\n",
		result.Timings.ReplayDurationMS,
		result.Timings.RecoveryDurationMS,
		result.Timings.TotalDurationMS,
	)
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-replay-benchmark-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func durationMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func currentAllocBytes() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc
}

func countQueuedJobs(jobs []*scheduler.Job) int {
	count := 0
	for _, job := range jobs {
		if job != nil && job.LifecycleState == scheduler.JobStateQueued {
			count++
		}
	}
	return count
}

func writeResultFiles(evidenceRoot string, result benchmarkResult) error {
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
			"jobs=%d\n"+
			"tasks=%d\n"+
			"transitions=%d\n"+
			"profile=%s\n"+
			"store_backend=%s\n"+
			"store_setup_duration_ms=%.2f\n"+
			"replay_duration_ms=%.2f\n"+
			"recovery_duration_ms=%.2f\n"+
			"total_duration_ms=%.2f\n",
		evidenceRoot,
		result.Seeded.Jobs,
		result.Seeded.Tasks,
		result.Seeded.Transitions,
		result.Config.Profile,
		result.Config.StoreBackend,
		result.Timings.StoreSetupDurationMS,
		result.Timings.ReplayDurationMS,
		result.Timings.RecoveryDurationMS,
		result.Timings.TotalDurationMS,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}

func seedSyntheticHistory(ctx context.Context, store scheduler.TransitionStore, cfg benchmarkConfig) (seededCounts, error) {
	counts := seededCounts{}
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < cfg.JobCount; i++ {
		jobID := fmt.Sprintf("bench-job-%06d", i)
		state := profileStateForJob(i, cfg.Profile)
		seeded, err := seedSyntheticJob(ctx, store, jobID, state, cfg.TasksPerJob, base.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			return counts, err
		}
		counts.Jobs++
		counts.Tasks += seeded.Tasks
		counts.Transitions += seeded.Transitions
	}
	return counts, nil
}

type seedWriter interface {
	AppendTransition(ctx context.Context, record scheduler.TransitionRecord) error
	UpsertJob(ctx context.Context, job scheduler.DurableJobRecord) error
	UpsertTask(ctx context.Context, task scheduler.DurableTaskRecord) error
}

type syntheticState string

const (
	syntheticQueued        syntheticState = "queued"
	syntheticRunning       syntheticState = "running"
	syntheticAwaitingHuman syntheticState = "awaiting_human"
)

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

func seedSyntheticJob(ctx context.Context, store scheduler.TransitionStore, jobID string, state syntheticState, tasksPerJob int, createdAt time.Time) (seededCounts, error) {
	counts := seededCounts{Tasks: tasksPerJob}
	run := func(writer seedWriter) error {
		sequence := uint64(1)
		currentTaskState := scheduler.TaskStatePending
		currentJobState := scheduler.JobStateQueued
		updatedAt := createdAt

		for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
			taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
			taskRecord := scheduler.DurableTaskRecord{
				TaskID:       taskID,
				JobID:        jobID,
				StageID:      "stage-1",
				NodeID:       fmt.Sprintf("worker-%02d", taskIndex%16),
				AgentID:      fmt.Sprintf("agent-%02d", taskIndex%8),
				AgentName:    fmt.Sprintf("agent-%02d", taskIndex%8),
				InputJSON:    mustJSON(agent.AgentInput{Instruction: fmt.Sprintf("synthetic task %d", taskIndex)}),
				PartitionKey: fmt.Sprintf("partition-%02d", taskIndex%4),
				UpdatedAt:    createdAt,
			}
			if err := writer.UpsertTask(ctx, taskRecord); err != nil {
				return err
			}
		}

		appendRecord := func(record scheduler.TransitionRecord) error {
			if err := writer.AppendTransition(ctx, record); err != nil {
				return err
			}
			counts.Transitions++
			sequence++
			updatedAt = record.OccurredAt
			return nil
		}

		if err := appendRecord(scheduler.TransitionRecord{
		SequenceID: sequence,
		EntityType: scheduler.TransitionEntityJob,
		Transition: scheduler.TransitionJobSubmitted,
		JobID:      jobID,
		NewState:   string(scheduler.JobStateSubmitted),
		OccurredAt: createdAt,
		}); err != nil {
			return err
		}

		for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
			taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
			if err := appendRecord(scheduler.TransitionRecord{
			SequenceID: sequence,
			EntityType: scheduler.TransitionEntityTask,
			Transition: scheduler.TransitionTaskCreated,
			JobID:      jobID,
			TaskID:     taskID,
			NewState:   string(scheduler.TaskStatePending),
			OccurredAt: createdAt.Add(time.Duration(sequence) * time.Millisecond),
			}); err != nil {
				return err
			}
		}

		if err := appendRecord(scheduler.TransitionRecord{
		SequenceID:    sequence,
		EntityType:    scheduler.TransitionEntityJob,
		Transition:    scheduler.TransitionJobQueued,
		JobID:         jobID,
		PreviousState: string(scheduler.JobStateSubmitted),
		NewState:      string(scheduler.JobStateQueued),
		OccurredAt:    createdAt.Add(time.Duration(sequence) * time.Millisecond),
		}); err != nil {
			return err
		}

		switch state {
		case syntheticRunning, syntheticAwaitingHuman:
			currentTaskState = scheduler.TaskStateRunning
			currentJobState = scheduler.JobStateRunning
			for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
				taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
				nodeID := fmt.Sprintf("worker-%02d", taskIndex%16)
				if err := appendRecord(scheduler.TransitionRecord{
				SequenceID:    sequence,
				EntityType:    scheduler.TransitionEntityTask,
				Transition:    scheduler.TransitionTaskDispatched,
				JobID:         jobID,
				TaskID:        taskID,
				PreviousState: string(scheduler.TaskStatePending),
				NewState:      string(scheduler.TaskStateDispatched),
				NodeID:        nodeID,
				Attempt:       1,
				OccurredAt:    createdAt.Add(time.Duration(sequence) * time.Millisecond),
				}); err != nil {
					return err
				}
				if err := appendRecord(scheduler.TransitionRecord{
				SequenceID:    sequence,
				EntityType:    scheduler.TransitionEntityTask,
				Transition:    scheduler.TransitionTaskRunning,
				JobID:         jobID,
				TaskID:        taskID,
				PreviousState: string(scheduler.TaskStateDispatched),
				NewState:      string(scheduler.TaskStateRunning),
				NodeID:        nodeID,
				Attempt:       1,
				OccurredAt:    createdAt.Add(time.Duration(sequence) * time.Millisecond),
				}); err != nil {
					return err
				}
			}
			if err := appendRecord(scheduler.TransitionRecord{
			SequenceID:    sequence,
			EntityType:    scheduler.TransitionEntityJob,
			Transition:    scheduler.TransitionJobRunning,
			JobID:         jobID,
			PreviousState: string(scheduler.JobStateQueued),
			NewState:      string(scheduler.JobStateRunning),
			OccurredAt:    createdAt.Add(time.Duration(sequence) * time.Millisecond),
			}); err != nil {
				return err
			}
		}

		if state == syntheticAwaitingHuman {
			currentJobState = scheduler.JobStateAwaitingHuman
			if err := appendRecord(scheduler.TransitionRecord{
			SequenceID:    sequence,
			EntityType:    scheduler.TransitionEntityJob,
			Transition:    scheduler.TransitionJobAwaitingHuman,
			JobID:         jobID,
			PreviousState: string(scheduler.JobStateRunning),
			NewState:      string(scheduler.JobStateAwaitingHuman),
			OccurredAt:    createdAt.Add(time.Duration(sequence) * time.Millisecond),
			}); err != nil {
				return err
			}
		}

		for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
			taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
			taskRecord := scheduler.DurableTaskRecord{
			TaskID:       taskID,
			JobID:        jobID,
			StageID:      "stage-1",
			NodeID:       fmt.Sprintf("worker-%02d", taskIndex%16),
			AgentID:      fmt.Sprintf("agent-%02d", taskIndex%8),
			AgentName:    fmt.Sprintf("agent-%02d", taskIndex%8),
			InputJSON:    mustJSON(agent.AgentInput{Instruction: fmt.Sprintf("synthetic task %d", taskIndex)}),
			PartitionKey: fmt.Sprintf("partition-%02d", taskIndex%4),
			CurrentState: currentTaskState,
			LastAttempt:  1,
			UpdatedAt:    updatedAt,
		}
			if currentTaskState == scheduler.TaskStatePending {
				taskRecord.NodeID = ""
				taskRecord.LastAttempt = 0
			}
			if err := writer.UpsertTask(ctx, taskRecord); err != nil {
				return err
			}
		}

		if err := writer.UpsertJob(ctx, scheduler.DurableJobRecord{
		JobID:          jobID,
		Name:           "synthetic-recovery-benchmark",
		CurrentState:   currentJobState,
		LastSequenceID: sequence - 1,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		}); err != nil {
			return err
		}
		return nil
	}

	if atomicStore, ok := store.(scheduler.AtomicTransitionStore); ok {
		if err := atomicStore.WithTx(ctx, func(tx scheduler.TransitionStoreTx) error {
			return run(tx)
		}); err != nil {
			return counts, err
		}
		return counts, nil
	}
	if err := run(store); err != nil {
		return counts, err
	}
	return counts, nil
}

func mustJSON(input agent.AgentInput) string {
	b, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func mustCreateStore(ctx context.Context, cfg benchmarkConfig) (scheduler.TransitionStore, func(), error) {
	switch cfg.StoreBackend {
	case "memory":
		return scheduler.NewInMemoryTransitionStore(), func() {}, nil
	case "postgres":
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, nil, fmt.Errorf("create pgx pool: %w", err)
		}
		store, err := scheduler.NewPostgresTransitionStore(ctx, pool)
		if err != nil {
			pool.Close()
			return nil, nil, fmt.Errorf("create postgres transition store: %w", err)
		}
		return store, pool.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported store backend %q", cfg.StoreBackend)
	}
}
