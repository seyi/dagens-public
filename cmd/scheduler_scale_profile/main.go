package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/scheduler"
)

type profileConfig struct {
	StoreBackend string
	DatabaseURL  string
	JobCount     int
	TasksPerJob  int
	Concurrency  int
	JobQueueSize int
	EvidenceRoot string
	Timeout      time.Duration
}

type profileResult struct {
	TimestampUTC string                    `json:"timestamp_utc"`
	Config       profileConfig             `json:"config"`
	Submission   submissionSummary         `json:"submission"`
	Queue        queueSummary              `json:"queue"`
	StoreOps     map[string]operationStats `json:"store_ops"`
	Timings      timingSummary             `json:"timings"`
}

type submissionSummary struct {
	Attempted         int       `json:"attempted"`
	Accepted          int       `json:"accepted"`
	QueueFullRejected int       `json:"queue_full_rejected"`
	OtherErrors       int       `json:"other_errors"`
	ThroughputPerSec  float64   `json:"throughput_per_sec"`
	LatencyMs         quantiles `json:"latency_ms"`
}

type queueSummary struct {
	ConfiguredCapacity int `json:"configured_capacity"`
	AcceptedQueuedJobs int `json:"accepted_queued_jobs"`
}

type timingSummary struct {
	TotalDurationMS float64 `json:"total_duration_ms"`
}

type quantiles struct {
	Min float64 `json:"min"`
	Avg float64 `json:"avg"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

type operationStats struct {
	Count     int     `json:"count"`
	TotalMs   float64 `json:"total_ms"`
	AverageMs float64 `json:"average_ms"`
	P95Ms     float64 `json:"p95_ms"`
	MaxMs     float64 `json:"max_ms"`
}

type observedStore struct {
	base       scheduler.TransitionStore
	taskLookup scheduler.DurableTaskLookupStore
	closeFn    func()

	mu        sync.Mutex
	durations map[string][]time.Duration
}

func newObservedStore(ctx context.Context, cfg profileConfig) (*observedStore, error) {
	store := &observedStore{
		durations: make(map[string][]time.Duration),
	}
	switch cfg.StoreBackend {
	case "memory":
		base := scheduler.NewInMemoryTransitionStore()
		store.base = base
		store.taskLookup = base
		return store, nil
	case "postgres":
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("create scheduler scale profile pool: %w", err)
		}
		base, err := scheduler.NewPostgresTransitionStore(ctx, pool)
		if err != nil {
			pool.Close()
			return nil, err
		}
		store.base = base
		store.taskLookup = base
		store.closeFn = pool.Close
		return store, nil
	default:
		return nil, fmt.Errorf("unsupported store backend %q", cfg.StoreBackend)
	}
}

func (s *observedStore) AppendTransition(ctx context.Context, record scheduler.TransitionRecord) error {
	start := time.Now()
	err := s.base.AppendTransition(ctx, record)
	s.record("append_transition", time.Since(start))
	return err
}

func (s *observedStore) UpsertJob(ctx context.Context, job scheduler.DurableJobRecord) error {
	start := time.Now()
	err := s.base.UpsertJob(ctx, job)
	s.record("upsert_job", time.Since(start))
	return err
}

func (s *observedStore) UpsertTask(ctx context.Context, task scheduler.DurableTaskRecord) error {
	start := time.Now()
	err := s.base.UpsertTask(ctx, task)
	s.record("upsert_task", time.Since(start))
	return err
}

func (s *observedStore) ListUnfinishedJobs(ctx context.Context) ([]scheduler.DurableJobRecord, error) {
	start := time.Now()
	jobs, err := s.base.ListUnfinishedJobs(ctx)
	s.record("list_unfinished_jobs", time.Since(start))
	return jobs, err
}

func (s *observedStore) ListTransitionsByJob(ctx context.Context, jobID string) ([]scheduler.TransitionRecord, error) {
	start := time.Now()
	records, err := s.base.ListTransitionsByJob(ctx, jobID)
	s.record("list_transitions_by_job", time.Since(start))
	return records, err
}

func (s *observedStore) ListTasksByJob(ctx context.Context, jobID string) ([]scheduler.DurableTaskRecord, error) {
	if s.taskLookup == nil {
		return nil, nil
	}
	start := time.Now()
	tasks, err := s.taskLookup.ListTasksByJob(ctx, jobID)
	s.record("list_tasks_by_job", time.Since(start))
	return tasks, err
}

func (s *observedStore) WithTx(ctx context.Context, fn func(tx scheduler.TransitionStoreTx) error) error {
	atomicStore, ok := s.base.(scheduler.AtomicTransitionStore)
	if !ok {
		return fmt.Errorf("transition store does not support atomic writes")
	}
	start := time.Now()
	err := atomicStore.WithTx(ctx, func(tx scheduler.TransitionStoreTx) error {
		return fn(&observedTx{base: tx, parent: s})
	})
	s.record("with_tx", time.Since(start))
	return err
}

func (s *observedStore) Close() {
	if s.closeFn != nil {
		s.closeFn()
	}
}

func (s *observedStore) record(name string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.durations[name] = append(s.durations[name], d)
}

func (s *observedStore) stats() map[string]operationStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]operationStats, len(s.durations))
	for name, durations := range s.durations {
		out[name] = buildOperationStats(durations)
	}
	return out
}

type observedTx struct {
	base   scheduler.TransitionStoreTx
	parent *observedStore
}

func (tx *observedTx) AppendTransition(ctx context.Context, record scheduler.TransitionRecord) error {
	start := time.Now()
	err := tx.base.AppendTransition(ctx, record)
	tx.parent.record("tx_append_transition", time.Since(start))
	return err
}

func (tx *observedTx) UpsertJob(ctx context.Context, job scheduler.DurableJobRecord) error {
	start := time.Now()
	err := tx.base.UpsertJob(ctx, job)
	tx.parent.record("tx_upsert_job", time.Since(start))
	return err
}

func (tx *observedTx) UpsertTask(ctx context.Context, task scheduler.DurableTaskRecord) error {
	start := time.Now()
	err := tx.base.UpsertTask(ctx, task)
	tx.parent.record("tx_upsert_task", time.Since(start))
	return err
}

func main() {
	cfg := profileConfig{}
	flag.StringVar(&cfg.StoreBackend, "store-backend", "memory", "profile store backend: memory|postgres")
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for postgres-backed profile mode")
	flag.IntVar(&cfg.JobCount, "jobs", 5000, "number of jobs to submit")
	flag.IntVar(&cfg.TasksPerJob, "tasks-per-job", 4, "number of tasks per job")
	flag.IntVar(&cfg.Concurrency, "concurrency", 32, "number of concurrent submitters")
	flag.IntVar(&cfg.JobQueueSize, "job-queue-size", 6000, "scheduler job queue capacity")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 5*time.Minute, "overall profile timeout")
	flag.Parse()

	if err := validateConfig(cfg); err != nil {
		exitf("%v", err)
	}
	if err := os.MkdirAll(cfg.EvidenceRoot, 0o755); err != nil {
		exitf("create evidence root: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	store, err := newObservedStore(ctx, cfg)
	if err != nil {
		exitf("create observed store: %v", err)
	}
	defer store.Close()
	schedCfg := scheduler.DefaultSchedulerConfig()
	schedCfg.JobQueueSize = cfg.JobQueueSize
	s := scheduler.NewSchedulerWithBenchmarkQueueCapacity(nil, nil, schedCfg, scheduler.SchedulerDependencies{}, cfg.JobQueueSize)
	if err := s.SetTransitionStore(store); err != nil {
		exitf("set transition store: %v", err)
	}

	result := profileResult{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Config:       cfg,
	}

	start := time.Now()
	latencies, accepted, queueFullRejected, otherErrors := runSubmissionProfile(ctx, s, cfg)
	total := time.Since(start)

	result.Submission = submissionSummary{
		Attempted:         cfg.JobCount,
		Accepted:          accepted,
		QueueFullRejected: queueFullRejected,
		OtherErrors:       otherErrors,
		ThroughputPerSec:  float64(accepted) / total.Seconds(),
		LatencyMs:         buildQuantiles(latencies),
	}
	result.Queue = queueSummary{
		ConfiguredCapacity: cfg.JobQueueSize,
		AcceptedQueuedJobs: countQueuedJobs(s.GetAllJobs()),
	}
	result.StoreOps = store.stats()
	result.Timings.TotalDurationMS = durationMillis(total)

	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: scheduler scale profile completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("attempted=%d accepted=%d queue_full=%d other_errors=%d throughput_per_sec=%.2f\n",
		result.Submission.Attempted,
		result.Submission.Accepted,
		result.Submission.QueueFullRejected,
		result.Submission.OtherErrors,
		result.Submission.ThroughputPerSec,
	)
}

func validateConfig(cfg profileConfig) error {
	if cfg.JobCount <= 0 || cfg.TasksPerJob <= 0 || cfg.Concurrency <= 0 || cfg.JobQueueSize <= 0 {
		return fmt.Errorf("jobs, tasks-per-job, concurrency, and job-queue-size must all be > 0")
	}
	if cfg.StoreBackend != "memory" && cfg.StoreBackend != "postgres" {
		return fmt.Errorf("unsupported store-backend %q", cfg.StoreBackend)
	}
	if cfg.StoreBackend == "postgres" && cfg.DatabaseURL == "" {
		return fmt.Errorf("database-url is required for postgres store-backend")
	}
	return nil
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-scheduler-scale-profile-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func durationMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func runSubmissionProfile(ctx context.Context, s *scheduler.Scheduler, cfg profileConfig) ([]time.Duration, int, int, int) {
	jobCh := make(chan int)
	var accepted int64
	var queueFullRejected int64
	var otherErrors int64
	latencies := make([]time.Duration, cfg.JobCount)

	var wg sync.WaitGroup
	for worker := 0; worker < cfg.Concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for jobIndex := range jobCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				job := buildSyntheticJob(jobIndex, cfg.TasksPerJob)
				begin := time.Now()
				err := s.SubmitJobWithContext(ctx, job)
				latencies[jobIndex] = time.Since(begin)
				switch err {
				case nil:
					atomic.AddInt64(&accepted, 1)
				case scheduler.ErrJobQueueFull:
					atomic.AddInt64(&queueFullRejected, 1)
				default:
					atomic.AddInt64(&otherErrors, 1)
				}
			}
		}()
	}

	for i := 0; i < cfg.JobCount; i++ {
		jobCh <- i
	}
	close(jobCh)
	wg.Wait()

	return latencies, int(accepted), int(queueFullRejected), int(otherErrors)
}

func buildSyntheticJob(index, tasksPerJob int) *scheduler.Job {
	job := scheduler.NewJob(fmt.Sprintf("scale-job-%06d", index), "scheduler-scale-profile")
	stage := &scheduler.Stage{
		ID:    "stage-1",
		JobID: job.ID,
		Tasks: make([]*scheduler.Task, 0, tasksPerJob),
	}
	for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
		stage.Tasks = append(stage.Tasks, &scheduler.Task{
			ID:        fmt.Sprintf("%s-task-%03d", job.ID, taskIndex),
			StageID:   stage.ID,
			JobID:     job.ID,
			AgentID:   fmt.Sprintf("agent-%02d", taskIndex%8),
			AgentName: fmt.Sprintf("agent-%02d", taskIndex%8),
			Input: &agent.AgentInput{
				TaskID:      fmt.Sprintf("%s-task-%03d", job.ID, taskIndex),
				Instruction: fmt.Sprintf("synthetic submission task %d", taskIndex),
				Context:     map[string]interface{}{"job_index": index, "task_index": taskIndex},
				MaxRetries:  0,
				Timeout:     time.Second,
			},
			PartitionKey: fmt.Sprintf("partition-%02d", taskIndex%4),
			Status:       scheduler.JobPending,
		})
	}
	job.AddStage(stage)
	return job
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

func buildQuantiles(latencies []time.Duration) quantiles {
	filtered := make([]float64, 0, len(latencies))
	for _, latency := range latencies {
		if latency <= 0 {
			continue
		}
		filtered = append(filtered, durationMillis(latency))
	}
	if len(filtered) == 0 {
		return quantiles{}
	}
	sort.Float64s(filtered)
	total := 0.0
	for _, v := range filtered {
		total += v
	}
	return quantiles{
		Min: filtered[0],
		Avg: total / float64(len(filtered)),
		P50: percentile(filtered, 0.50),
		P95: percentile(filtered, 0.95),
		P99: percentile(filtered, 0.99),
		Max: filtered[len(filtered)-1],
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	index := int(float64(len(sorted)-1) * p)
	return sorted[index]
}

func buildOperationStats(durations []time.Duration) operationStats {
	if len(durations) == 0 {
		return operationStats{}
	}
	values := make([]float64, 0, len(durations))
	total := 0.0
	for _, d := range durations {
		ms := durationMillis(d)
		values = append(values, ms)
		total += ms
	}
	sort.Float64s(values)
	return operationStats{
		Count:     len(values),
		TotalMs:   total,
		AverageMs: total / float64(len(values)),
		P95Ms:     percentile(values, 0.95),
		MaxMs:     values[len(values)-1],
	}
}

func writeResults(evidenceRoot string, result profileResult) error {
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
			"attempted=%d\n"+
			"accepted=%d\n"+
			"queue_full_rejected=%d\n"+
			"other_errors=%d\n"+
			"throughput_per_sec=%.2f\n"+
			"latency_p95_ms=%.2f\n"+
			"latency_p99_ms=%.2f\n",
		evidenceRoot,
		result.Submission.Attempted,
		result.Submission.Accepted,
		result.Submission.QueueFullRejected,
		result.Submission.OtherErrors,
		result.Submission.ThroughputPerSec,
		result.Submission.LatencyMs.P95,
		result.Submission.LatencyMs.P99,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}
