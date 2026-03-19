package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/policy"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote"
	"github.com/seyi/dagens/pkg/telemetry"
)

// TaskExecutor defines the interface for executing tasks on nodes
type TaskExecutor interface {
	ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error)
}

var ErrJobQueueFull = errors.New("job queue is full")
var ErrNoWorkerCapacity = errors.New("all healthy workers are at capacity")
var ErrSchedulerRecovering = errors.New("scheduler is recovering state")
var ErrTransitionStoreSetAfterStart = errors.New("transition store can only be set before scheduler start")
var ErrLeadershipProviderSetAfterStart = errors.New("leadership provider can only be set before scheduler start")
var ErrDispatchClaimRejected = errors.New("dispatch claim rejected by transition store fencing")

const schedulerMetricsID = "default"
const maxTransitionErrorSummaryRunes = 1024
const malformedReplayQuarantineTTL = 30 * time.Second
const staleInFlightReconcileTimeout = time.Minute

type nodeCapacity struct {
	ReservedInFlight int
	ReportedInFlight int
	MaxConcurrency   int
	LastUpdated      time.Time
}

// ReconcileMode defines the target behavior for durable state reconciliation.
type ReconcileMode int

const (
	// ReconcileModeLeader enables full reconciliation including job enqueueing.
	ReconcileModeLeader ReconcileMode = iota
	// ReconcileModeFollower enables warm-replay only (no enqueueing).
	ReconcileModeFollower
)

// Scheduler manages the execution of jobs and their tasks
type Scheduler struct {
	registry          registry.Registry // Interface for node discovery
	executor          TaskExecutor
	jobs              map[string]*Job
	jobQueue          chan *Job
	queuedJobs        map[string]struct{}
	mu                sync.RWMutex
	stopChan          chan struct{}
	wg                sync.WaitGroup
	config            SchedulerConfig
	affinityMap       *AffinityMap // Sticky scheduling affinity tracking
	nodeIndex         int          // Round-robin counter for node selection
	nodeCapacity      map[string]*nodeCapacity
	dispatchCooldowns map[string]time.Time
	transitionStore   TransitionStore
	transitionMu      sync.Mutex
	jobSequences      map[string]uint64
	leadership        LeadershipProvider
	started           bool
	recovering        bool
	stopRequested     bool
	recoveryCancel    context.CancelFunc
	leadershipCancel  context.CancelFunc
	leadershipStop    func()
	executionCtx      context.Context
	executionCancel   context.CancelFunc
	replayQuarantine  map[string]time.Time
	pendingEnqueue    int
	lastReconcileTime time.Time
	metrics           *observability.Metrics
	tracer            telemetry.Tracer
	logger            telemetry.Logger
	stopOnce          sync.Once
}

// RegistryInterface defines the subset of registry functionality needed by the scheduler
type RegistryInterface interface {
	GetHealthyNodes() []registry.NodeInfo
	GetNode(nodeID string) (registry.NodeInfo, bool)
}

// NewScheduler creates a new scheduler with default configuration
func NewScheduler(reg registry.Registry, executor TaskExecutor) *Scheduler {
	return NewSchedulerWithConfig(reg, executor, DefaultSchedulerConfig())
}

// SchedulerDependencies allows callers to inject observability dependencies.
// Any nil dependency falls back to package-level defaults.
type SchedulerDependencies struct {
	Metrics *observability.Metrics
	Tracer  telemetry.Tracer
	Logger  telemetry.Logger
}

// NewSchedulerWithConfig creates a new scheduler with the specified configuration
func NewSchedulerWithConfig(reg registry.Registry, executor TaskExecutor, config SchedulerConfig) *Scheduler {
	return NewSchedulerWithConfigAndDeps(reg, executor, config, SchedulerDependencies{})
}

// NewSchedulerWithConfigAndDeps creates a scheduler with explicit dependencies.
func NewSchedulerWithConfigAndDeps(reg registry.Registry, executor TaskExecutor, config SchedulerConfig, deps SchedulerDependencies) *Scheduler {
	config.Validate()
	return newSchedulerWithValidatedConfig(reg, executor, config, deps)
}

// NewSchedulerWithBenchmarkQueueCapacity creates a scheduler using normal config
// validation, then applies an explicit benchmark-only queue override. This keeps
// production constructor semantics unchanged while allowing scale harnesses to
// exercise larger admission buffers deliberately.
func NewSchedulerWithBenchmarkQueueCapacity(reg registry.Registry, executor TaskExecutor, config SchedulerConfig, deps SchedulerDependencies, queueCapacity int) *Scheduler {
	config.Validate()
	if queueCapacity > 0 {
		config.JobQueueSize = queueCapacity
	}
	return newSchedulerWithValidatedConfig(reg, executor, config, deps)
}

func newSchedulerWithValidatedConfig(reg registry.Registry, executor TaskExecutor, config SchedulerConfig, deps SchedulerDependencies) *Scheduler {
	var affinityMap *AffinityMap
	if config.EnableStickiness {
		affinityMap = NewAffinityMap(config.AffinityTTL, config.AffinityCleanupInterval)
	}

	metrics := deps.Metrics
	if metrics == nil {
		metrics = observability.GetMetrics()
	}
	tracer := deps.Tracer
	if tracer == nil {
		tracer = telemetry.GetGlobalTelemetry().GetTracer()
	}
	logger := deps.Logger
	if logger == nil {
		logger = telemetry.GetGlobalTelemetry().GetLogger()
	}

	s := &Scheduler{
		registry:          reg,
		executor:          executor,
		jobs:              make(map[string]*Job),
		jobQueue:          make(chan *Job, config.JobQueueSize),
		queuedJobs:        make(map[string]struct{}),
		stopChan:          make(chan struct{}),
		config:            config,
		affinityMap:       affinityMap,
		nodeCapacity:      make(map[string]*nodeCapacity),
		dispatchCooldowns: make(map[string]time.Time),
		transitionStore:   NewInMemoryTransitionStore(),
		jobSequences:      make(map[string]uint64),
		leadership:        defaultLeadershipProvider(),
		replayQuarantine:  make(map[string]time.Time),
		metrics:           metrics,
		tracer:            tracer,
		logger:            logger,
	}
	s.metrics.SetTaskQueueDepths(schedulerMetricsID, 0, cap(s.jobQueue))
	return s
}

// Start starts the scheduler's main loop
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.started || s.recovering || s.stopRequested {
		s.mu.Unlock()
		return
	}
	s.recovering = true
	recoveryCtx, cancel := context.WithTimeout(context.Background(), s.config.RecoveryTimeout)
	s.recoveryCancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.recovering = false
		s.recoveryCancel = nil
		s.mu.Unlock()
	}()
	defer cancel()

	if err := s.RecoverFromTransitions(recoveryCtx); err != nil {
		log.Printf("failed to recover scheduler state from transitions: %v", err)
		s.emitOperationalAlert(context.Background(), "scheduler.recovery.failed", "critical",
			"scheduler startup recovery failed", map[string]interface{}{
				"error": err.Error(),
			})
	}

	if err := s.startLeadershipProvider(); err != nil {
		log.Printf("failed to start scheduler leadership provider: %v", err)
		s.emitOperationalAlert(context.Background(), "scheduler.leadership.start_failed", "critical",
			"failed to start scheduler leadership provider", map[string]interface{}{
				"error": err.Error(),
			})
		return
	}

	s.mu.Lock()
	if s.stopRequested {
		s.mu.Unlock()
		return
	}
	if !s.config.EnableJobRetentionCleanup {
		s.logger.Warn("job retention cleanup disabled; terminal jobs will accumulate in memory", map[string]interface{}{
			"job_retention_cleanup_enabled": false,
		})
	}
	s.started = true
	s.executionCtx, s.executionCancel = context.WithCancel(context.Background())
	s.mu.Unlock()
	s.wg.Add(1)
	go s.run()
	if s.config.EnableLeaderDurableRequeueLoop || s.config.EnableFollowerDurableRequeueLoop {
		s.wg.Add(1)
		go s.durableQueuedRequeueLoop()
	}
	if s.config.EnableJobRetentionCleanup {
		s.wg.Add(1)
		go s.jobRetentionCleanupLoop()
	}
}

// Stop stops the scheduler and cleans up resources
func (s *Scheduler) Stop() {
	s.mu.Lock()
	s.stopRequested = true
	cancel := s.recoveryCancel
	leadershipCancel := s.leadershipCancel
	leadershipStop := s.leadershipStop
	executionCancel := s.executionCancel
	s.leadershipCancel = nil
	s.leadershipStop = nil
	s.executionCancel = nil
	s.executionCtx = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if leadershipCancel != nil {
		leadershipCancel()
	}
	if leadershipStop != nil {
		leadershipStop()
	}
	if executionCancel != nil {
		executionCancel()
	}

	s.stopOnce.Do(func() {
		close(s.stopChan)
	})
	s.wg.Wait()

	// Stop affinity map cleanup goroutine
	if s.affinityMap != nil {
		s.affinityMap.Stop()
	}
}

// SubmitJob submits a job for execution
func (s *Scheduler) SubmitJob(job *Job) error {
	return s.SubmitJobWithContext(context.Background(), job)
}

// CurrentDispatchAuthority returns whether this scheduler instance is currently
// allowed to accept and dispatch work under the active leadership provider.
func (s *Scheduler) CurrentDispatchAuthority(ctx context.Context) (LeadershipAuthority, error) {
	return s.dispatchAuthority(ctx)
}

// SubmitJobWithContext submits a job for execution and uses ctx for initial
// lifecycle transition persistence.
func (s *Scheduler) SubmitJobWithContext(ctx context.Context, job *Job) error {
	s.mu.Lock()
	if s.recovering {
		s.mu.Unlock()
		return ErrSchedulerRecovering
	}

	if _, exists := s.jobs[job.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("job %s already exists", job.ID)
	}

	metrics := s.metrics
	if len(s.jobQueue)+s.pendingEnqueue < cap(s.jobQueue) {
		// Reserve queue capacity before unlocking so concurrent submissions cannot
		// over-admit while initial transitions are being persisted.
		s.pendingEnqueue++
		job.Status = JobPending
		s.jobs[job.ID] = job
		s.mu.Unlock()

		s.recordInitialTransitions(ctx, job)

		s.mu.Lock()
		s.pendingEnqueue--
		enqueued := s.enqueueJobLocked(job)
		s.mu.Unlock()
		if enqueued {
			return nil
		}

		// Rare fallback if queue was filled by non-submission paths (for example
		// requeue/reconcile) while this submission was persisting transitions.
		s.logger.Warn("job accepted but immediate enqueue deferred; durable reconcile will retry", map[string]interface{}{
			"job_id": job.ID,
		})
		return nil
	}
	metrics.SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
	s.mu.Unlock()
	return ErrJobQueueFull
}

// GetJob returns a job by ID
func (s *Scheduler) GetJob(jobID string) (*Job, error) {
	s.mu.RLock()
	job, exists := s.jobs[jobID]
	store := s.transitionStore
	s.mu.RUnlock()
	if exists {
		return job, nil
	}
	if store == nil {
		return nil, fmt.Errorf("job %s not found", jobID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	replayed, err := ReplayJobState(ctx, store, jobID)
	if err != nil {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	if replayed.Job.CurrentState == "" && len(replayed.Tasks) == 0 && len(replayed.Transitions) == 0 {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	hydrated := buildRecoveredRuntimeJob(replayed)
	if hydrated == nil {
		return nil, fmt.Errorf("job %s not found", jobID)
	}

	s.mu.Lock()
	if existing, ok := s.jobs[jobID]; ok {
		s.mu.Unlock()
		return existing, nil
	}
	s.jobs[jobID] = hydrated
	s.seedJobSequenceLocked(jobID, replayed.Job.LastSequenceID, replayed.Transitions)
	s.mu.Unlock()

	return hydrated, nil
}

// ResumeAwaitingHumanJob resumes a job that is currently waiting on a HITL
// pause boundary. The supplied context fields are merged into each task input
// before the job is re-executed so resumed work can observe callback outcome.
func (s *Scheduler) ResumeAwaitingHumanJob(ctx context.Context, jobID string, resumeContext map[string]interface{}) error {
	job, err := s.GetJob(jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("job %s not found", jobID)
	}

	s.mu.RLock()
	state := job.LifecycleState
	s.mu.RUnlock()
	if state != JobStateAwaitingHuman && !IsTerminalJobState(state) {
		if refreshed := s.refreshJobFromDurableState(ctx, jobID); refreshed != nil {
			job = refreshed
			s.mu.RLock()
			state = job.LifecycleState
			s.mu.RUnlock()
		}
	}

	if IsTerminalJobState(state) {
		return nil
	}
	if state != JobStateAwaitingHuman {
		return fmt.Errorf("job %s is not awaiting human input (state=%s)", jobID, state)
	}

	if len(resumeContext) > 0 {
		for _, stage := range job.Stages {
			if stage == nil {
				continue
			}
			stage.Status = JobPending
			for _, task := range stage.Tasks {
				if task == nil {
					continue
				}
				if task.Input == nil {
					task.Input = &agent.AgentInput{}
				}
				if task.Input.Context == nil {
					task.Input.Context = make(map[string]interface{})
				}
				for key, value := range resumeContext {
					task.Input.Context[key] = value
				}
				task.Status = JobPending
				task.LifecycleState = TaskStatePending
				task.Output = nil
			}
		}
	}

	s.executeJob(ctx, job)
	return nil
}

// GetAllJobs returns all jobs
func (s *Scheduler) GetAllJobs() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	jobs := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// SchedulerReadiness describes the current synchronization state of the scheduler.
type SchedulerReadiness struct {
	// IsRecovering is true during initial startup recovery.
	IsRecovering bool
	// IsLeader is true when this instance currently holds dispatch authority.
	IsLeader bool
	// LastReconcileTime is the last time the scheduler successfully synchronized
	// with the durable transition store.
	LastReconcileTime time.Time
}

// Readiness returns a snapshot of the scheduler's current synchronization state.
func (s *Scheduler) Readiness() SchedulerReadiness {
	s.mu.RLock()
	started := s.started
	recovering := s.recovering
	lastReconcileTime := s.lastReconcileTime
	provider := s.leadership
	s.mu.RUnlock()

	isLeader := false
	if started && !recovering && provider != nil {
		// Use a short-timeout context for the internal readiness check to avoid
		// blocking the caller on etcd/network issues.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if auth, err := provider.DispatchAuthority(ctx); err == nil {
			isLeader = auth.IsLeader
		}
	}

	return SchedulerReadiness{
		IsRecovering:      recovering,
		IsLeader:          isLeader,
		LastReconcileTime: lastReconcileTime,
	}
}

// TransitionStore exposes the configured lifecycle transition store.
func (s *Scheduler) TransitionStore() TransitionStore {
	return s.transitionStore
}

// SetTransitionStore replaces the scheduler transition store.
// Intended for startup wiring and tests. It must be called before Start().
func (s *Scheduler) SetTransitionStore(store TransitionStore) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.recovering {
		return ErrTransitionStoreSetAfterStart
	}
	s.transitionStore = store
	return nil
}

// SetLeadershipProvider sets the HA leadership provider used to gate dispatch.
// Intended for startup wiring and tests. It must be called before Start().
func (s *Scheduler) SetLeadershipProvider(provider LeadershipProvider) error {
	if provider == nil {
		provider = defaultLeadershipProvider()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.recovering {
		return ErrLeadershipProviderSetAfterStart
	}
	s.leadership = provider
	return nil
}

// UpdateNodeCapacity updates the scheduler's latest capacity snapshot for a node.
// This uses server receipt time and is kept for compatibility.
func (s *Scheduler) UpdateNodeCapacity(nodeID string, inFlight, maxConcurrency int) {
	s.UpdateNodeCapacityAt(nodeID, inFlight, maxConcurrency, time.Now())
}

// UpdateNodeCapacityAt updates the scheduler's latest capacity snapshot for a node
// using the provided report time from the worker heartbeat.
func (s *Scheduler) UpdateNodeCapacityAt(nodeID string, inFlight, maxConcurrency int, reportedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	capacity := s.getOrCreateNodeCapacityLocked(nodeID)
	if inFlight < 0 {
		inFlight = 0
	}
	if maxConcurrency <= 0 {
		maxConcurrency = s.config.DefaultWorkerMaxConcurrency
	}
	if maxConcurrency > s.config.MaxWorkerConcurrencyCap {
		maxConcurrency = s.config.MaxWorkerConcurrencyCap
	}
	if reportedAt.IsZero() {
		s.logger.Warn("node capacity report missing timestamp; using server receipt time", map[string]interface{}{
			"node_id": nodeID,
		})
		reportedAt = time.Now()
	}

	capacity.ReportedInFlight = inFlight
	capacity.MaxConcurrency = maxConcurrency
	capacity.LastUpdated = reportedAt
}

// run is the main scheduler loop
func (s *Scheduler) run() {
	defer s.wg.Done()

	for {
		select {
		case <-s.stopChan:
			return
		case job := <-s.jobQueue:
			s.markJobDequeued(job)
			s.metrics.SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
			if !s.isExecutableDequeuedJob(job) {
				continue
			}
			authority, err := s.dispatchAuthority(context.Background())
			if err != nil {
				log.Printf("scheduler leadership check failed; dispatch deferred for job %s: %v", job.ID, err)
				s.requeueJob(job)
				if !s.waitLeadershipRetry() {
					return
				}
				continue
			}
			if !authority.IsLeader {
				log.Printf("scheduler dispatch blocked: follower mode for job %s (leader_id=%s epoch=%s)",
					job.ID, authority.LeaderID, authority.Epoch)
				s.requeueJob(job)
				if !s.waitLeadershipRetry() {
					return
				}
				continue
			}
			if refreshed := s.refreshJobFromDurableState(s.executionContextOrBackground(), job.ID); refreshed != nil {
				job = refreshed
			}
			if !s.isExecutableDequeuedJob(job) {
				continue
			}
			s.executeJob(s.executionContextOrBackground(), job)
		}
	}
}

func (s *Scheduler) durableQueuedRequeueLoop() {
	defer s.wg.Done()

	// Initial poll interval depends on leadership status
	pollInterval := s.config.FollowerDurableRequeuePollInterval
	authority, err := s.dispatchAuthority(s.executionContextOrBackground())
	if err == nil && authority.IsLeader {
		pollInterval = s.config.LeaderDurableRequeuePollInterval
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			authority, err := s.dispatchAuthority(s.executionContextOrBackground())
			if err != nil {
				log.Printf("scheduler leadership check failed during reconcile: %v", err)
				continue
			}

			// Adjust ticker if leadership changed
			newInterval := s.config.FollowerDurableRequeuePollInterval
			mode := ReconcileModeFollower
			if authority.IsLeader {
				newInterval = s.config.LeaderDurableRequeuePollInterval
				mode = ReconcileModeLeader
			}
			if newInterval != pollInterval {
				pollInterval = newInterval
				ticker.Reset(pollInterval)
			}

			// Skip if mode is disabled
			if (mode == ReconcileModeLeader && !s.config.EnableLeaderDurableRequeueLoop) ||
				(mode == ReconcileModeFollower && !s.config.EnableFollowerDurableRequeueLoop) {
				continue
			}

			reconcileCtx, cancel := s.reconcileContext()
			err = s.reconcileDurableQueuedJobsOnce(reconcileCtx, mode)
			cancel()
			if err != nil {
				log.Printf("scheduler durable queued-job reconcile failed (mode=%v): %v", mode, err)
			}
		}
	}
}

func (s *Scheduler) isExecutableDequeuedJob(job *Job) bool {
	if job == nil {
		return false
	}
	s.mu.RLock()
	current := job
	if latest, ok := s.jobs[job.ID]; ok && latest != nil {
		current = latest
	}
	state := current.LifecycleState
	s.mu.RUnlock()
	switch state {
	case JobStateSubmitted, JobStateQueued:
		return true
	default:
		log.Printf("scheduler skipped dequeued job %s with non-executable lifecycle state %s", job.ID, state)
		return false
	}
}

func (s *Scheduler) refreshJobFromDurableState(ctx context.Context, jobID string) *Job {
	if strings.TrimSpace(jobID) == "" || s.transitionStore == nil {
		return nil
	}
	replayed, err := ReplayJobState(ctx, s.transitionStore, jobID)
	if err != nil {
		return nil
	}
	if replayed.Job.CurrentState == "" && len(replayed.Tasks) == 0 && len(replayed.Transitions) == 0 {
		return nil
	}

	hydrated := buildRecoveredRuntimeJob(replayed)
	if hydrated == nil {
		return nil
	}
	hydrated.Name = replayed.Job.Name
	if hydrated.CreatedAt.IsZero() {
		hydrated.CreatedAt = replayed.Job.CreatedAt
	}
	if hydrated.UpdatedAt.IsZero() {
		hydrated.UpdatedAt = replayed.Job.UpdatedAt
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.jobs[jobID]; ok && existing != nil {
		if hydrated.Name == "" {
			hydrated.Name = existing.Name
		}
		if hydrated.CreatedAt.IsZero() {
			hydrated.CreatedAt = existing.CreatedAt
		}
		if len(hydrated.Metadata) == 0 && len(existing.Metadata) > 0 {
			hydrated.Metadata = existing.Metadata
		}
	}
	s.jobs[jobID] = hydrated
	s.seedJobSequenceLocked(jobID, replayed.Job.LastSequenceID, replayed.Transitions)
	return hydrated
}

func (s *Scheduler) reconcileDurableQueuedJobsOnce(ctx context.Context, mode ReconcileMode) error {
	if s.transitionStore == nil {
		return nil
	}

	jobs, err := s.transitionStore.ListUnfinishedJobs(ctx)
	if err != nil {
		return err
	}

	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].JobID < jobs[j].JobID
		}
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})

	for _, durableJob := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if until, ok := s.replayQuarantineUntil(durableJob.JobID); ok {
			if time.Now().Before(until) {
				continue
			}
			s.clearReplayQuarantine(durableJob.JobID)
		}

		if mode == ReconcileModeLeader && s.tryEnqueueExistingQueuedJob(durableJob.JobID) {
			continue
		}

		replayed, err := ReplayJobState(ctx, s.transitionStore, durableJob.JobID)
		if err != nil {
			if mode == ReconcileModeLeader {
				s.setReplayQuarantine(durableJob.JobID, time.Now().Add(malformedReplayQuarantineTTL))
				log.Printf("scheduler durable queued-job reconcile skipped malformed replay for job %s: %v", durableJob.JobID, err)
				s.failDurableQueuedJobForReconcile(ctx, durableJob, fmt.Sprintf("reconcile replay failed: %v", err))
			}
			continue
		}
		s.clearReplayQuarantine(durableJob.JobID)

		replayed.Job.Name = durableJob.Name
		replayed.Job.LastSequenceID = durableJob.LastSequenceID
		if replayed.Job.CreatedAt.IsZero() {
			replayed.Job.CreatedAt = durableJob.CreatedAt
		}
		if replayed.Job.UpdatedAt.IsZero() {
			replayed.Job.UpdatedAt = durableJob.UpdatedAt
		}
		if len(replayed.Transitions) > 0 {
			last := replayed.Transitions[len(replayed.Transitions)-1].SequenceID
			if last > replayed.Job.LastSequenceID {
				replayed.Job.LastSequenceID = last
			}
		}

		if mode == ReconcileModeLeader && replayed.Job.CurrentState == JobStateRunning && allReplayedTasksPending(replayed.Tasks) {
			s.failDurableQueuedJobForReconcile(ctx, durableJob, "reconcile detected orphan running state without dispatched/running tasks after leadership loss")
			continue
		}
		if mode == ReconcileModeLeader && replayed.Job.CurrentState == JobStateRunning &&
			hasStaleInFlightReplayedTask(replayed.Tasks, time.Now().UTC(), staleInFlightReconcileTimeout) {
			s.failDurableQueuedJobForReconcile(ctx, durableJob, "reconcile detected stale in-flight task after leadership loss")
			continue
		}

		s.mu.Lock()

		job, exists := s.jobs[replayed.Job.JobID]
		if !exists {
			job = buildRecoveredRuntimeJob(replayed)
			s.jobs[job.ID] = job
			s.seedJobSequenceLocked(job.ID, replayed.Job.LastSequenceID, replayed.Transitions)
		} else if replayed.Job.LastSequenceID > s.jobSequences[replayed.Job.JobID] {
			// Warm replay update for existing job visibility
			job.LifecycleState = replayed.Job.CurrentState
			job.UpdatedAt = replayed.Job.UpdatedAt
			s.seedJobSequenceLocked(job.ID, replayed.Job.LastSequenceID, replayed.Transitions)
		}

		if mode != ReconcileModeLeader {
			s.mu.Unlock()
			continue
		}

		if job.LifecycleState == JobStateSubmitted {
			s.recordJobTransition(ctx, nil, job, TransitionJobQueued, JobStateQueued, "")
		}
		if job.LifecycleState != JobStateQueued {
			s.mu.Unlock()
			continue
		}
		if !isSafeForQueuedResume(job) {
			s.mu.Unlock()
			continue
		}
		if missingTaskFields := recoveredQueuedMissingExecutionFieldCount(job, s.config.EnableStickiness); missingTaskFields > 0 {
			s.mu.Unlock()
			s.failDurableQueuedJobForReconcile(ctx, durableJob, fmt.Sprintf("reconcile skipped auto-resume: recovered queued job missing execution fields (%d)", missingTaskFields))
			continue
		}
		s.enqueueJobLocked(job)
		s.mu.Unlock()
	}
	s.recordReconcileSuccess(time.Now(), mode)
	return nil
}

func (s *Scheduler) recordReconcileSuccess(at time.Time, mode ReconcileMode) {
	s.mu.Lock()
	s.lastReconcileTime = at
	s.mu.Unlock()
	s.metrics.RecordSchedulerReconcileSucceeded(reconcileModeLabel(mode))
}

func reconcileModeLabel(mode ReconcileMode) string {
	if mode == ReconcileModeLeader {
		return "leader"
	}
	return "follower"
}

func allReplayedTasksPending(tasks map[string]DurableTaskRecord) bool {
	if len(tasks) == 0 {
		return true
	}
	for _, task := range tasks {
		if task.CurrentState != "" && task.CurrentState != TaskStatePending {
			return false
		}
	}
	return true
}

func hasStaleInFlightReplayedTask(tasks map[string]DurableTaskRecord, now time.Time, timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	cutoff := now.Add(-timeout)
	for _, task := range tasks {
		switch task.CurrentState {
		case TaskStateDispatched, TaskStateRunning:
			if task.UpdatedAt.IsZero() || task.UpdatedAt.Before(cutoff) {
				return true
			}
		}
	}
	return false
}

func (s *Scheduler) tryEnqueueExistingQueuedJob(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[jobID]
	if !exists || job == nil || job.LifecycleState != JobStateQueued {
		return false
	}
	if !isSafeForQueuedResume(job) {
		return false
	}
	if recoveredQueuedMissingExecutionFieldCount(job, s.config.EnableStickiness) > 0 {
		return false
	}
	if _, alreadyQueued := s.queuedJobs[job.ID]; alreadyQueued {
		return true
	}
	return s.enqueueJobLocked(job)
}

func (s *Scheduler) failDurableQueuedJobForReconcile(ctx context.Context, durableJob DurableJobRecord, reason string) {
	if durableJob.JobID == "" {
		return
	}
	authority, err := s.dispatchAuthority(ctx)
	if err != nil || !authority.IsLeader {
		log.Printf("scheduler durable queued-job reconcile failure blocked without leadership for job %s: %v", durableJob.JobID, err)
		return
	}

	s.mu.Lock()
	job, exists := s.jobs[durableJob.JobID]
	if !exists || job == nil {
		job = &Job{
			ID:             durableJob.JobID,
			Name:           durableJob.Name,
			Stages:         make([]*Stage, 0),
			Status:         runtimeJobStatusFromLifecycle(durableJob.CurrentState),
			LifecycleState: durableJob.CurrentState,
			CreatedAt:      durableJob.CreatedAt,
			UpdatedAt:      durableJob.UpdatedAt,
			Metadata: map[string]interface{}{
				"recovered": true,
			},
		}
		s.jobs[job.ID] = job
	}
	currentState := job.LifecycleState
	s.mu.Unlock()
	if IsTerminalJobState(currentState) {
		return
	}

	s.updateJobStatus(job, JobFailed)
	if CanTransitionJobState(job.LifecycleState, JobStateFailed) {
		s.recordJobTransition(ctx, nil, job, TransitionJobFailed, JobStateFailed, reason)
		return
	}

	// Force terminal quarantine for unrecoverable histories that cannot accept
	// a legal lifecycle transition (for example SUBMITTED-only orphaned jobs).
	now := time.Now().UTC()
	job.LifecycleState = JobStateFailed
	job.UpdatedAt = now
	if err := s.transitionStore.UpsertJob(ctx, DurableJobRecord{
		JobID:          durableJob.JobID,
		Name:           durableJob.Name,
		CurrentState:   JobStateFailed,
		LastSequenceID: durableJob.LastSequenceID,
		CreatedAt:      durableJob.CreatedAt,
		UpdatedAt:      now,
	}); err != nil {
		log.Printf("scheduler durable queued-job reconcile failed to force-quarantine job %s: %v", durableJob.JobID, err)
	}
}

func (s *Scheduler) setReplayQuarantine(jobID string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replayQuarantine[jobID] = until
}

func (s *Scheduler) replayQuarantineUntil(jobID string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	until, ok := s.replayQuarantine[jobID]
	return until, ok
}

func (s *Scheduler) clearReplayQuarantine(jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.replayQuarantine, jobID)
}

func (s *Scheduler) dispatchAuthority(ctx context.Context) (LeadershipAuthority, error) {
	s.mu.RLock()
	provider := s.leadership
	s.mu.RUnlock()
	if provider == nil {
		provider = defaultLeadershipProvider()
	}
	return provider.DispatchAuthority(ctx)
}

func (s *Scheduler) startLeadershipProvider() error {
	s.mu.Lock()
	provider := s.leadership
	if provider == nil {
		provider = defaultLeadershipProvider()
		s.leadership = provider
	}
	leaderCtx, cancel := context.WithCancel(context.Background())
	s.leadershipCancel = cancel
	s.mu.Unlock()

	cleanup, err := StartLeadershipProviderIfLifecycle(leaderCtx, provider)
	if err != nil {
		cancel()
		s.mu.Lock()
		s.leadershipCancel = nil
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.leadershipStop = cleanup
	s.mu.Unlock()
	return nil
}

func (s *Scheduler) requeueJob(job *Job) {
	if job == nil {
		return
	}
	select {
	case <-s.stopChan:
		return
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueueJobLocked(job)
}

func (s *Scheduler) waitLeadershipRetry() bool {
	timer := time.NewTimer(s.config.LeadershipRetryInterval)
	defer timer.Stop()
	select {
	case <-s.stopChan:
		return false
	case <-timer.C:
		return true
	}
}

func (s *Scheduler) enqueueJobLocked(job *Job) bool {
	if job == nil {
		return false
	}
	if _, exists := s.queuedJobs[job.ID]; exists {
		return false
	}
	select {
	case s.jobQueue <- job:
		s.queuedJobs[job.ID] = struct{}{}
		s.metrics.SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
		return true
	default:
		s.metrics.SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
		return false
	}
}

func (s *Scheduler) markJobDequeued(job *Job) {
	if job == nil {
		return
	}
	s.mu.Lock()
	delete(s.queuedJobs, job.ID)
	s.mu.Unlock()
}

// executeJob executes a single job.
func (s *Scheduler) executeJob(ctx context.Context, job *Job) {
	s.mu.Lock()
	if job != nil {
		if _, exists := s.jobs[job.ID]; !exists {
			s.jobs[job.ID] = job
		}
	}
	s.mu.Unlock()

	s.updateJobStatus(job, JobRunning)
	log.Printf("Starting execution of job %s (%s)", job.ID, job.Name)

	// Preserve parent cancellation/trace context when available.
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := s.tracer.StartSpan(ctx, "job.execute")
	defer span.End()

	span.SetAttribute("job.id", job.ID)
	span.SetAttribute("job.name", job.Name)

	// Submission can enqueue before initial transitions are fully persisted.
	// Preserve SUBMITTED -> QUEUED ordering up front, but defer RUNNING/RESUMED
	// for jobs with tasks until first durable dispatch claim succeeds. This
	// avoids orphan RUNNING jobs on leader loss before any task is dispatched.
	if job.LifecycleState == JobStateSubmitted {
		s.recordJobTransition(ctx, span, job, TransitionJobQueued, JobStateQueued, "")
	}

	hasTasks := false
	for _, stage := range job.Stages {
		if stage != nil && len(stage.Tasks) > 0 {
			hasTasks = true
			break
		}
	}
	if !hasTasks {
		startTransition := TransitionJobRunning
		if job.LifecycleState == JobStateAwaitingHuman {
			startTransition = TransitionJobResumed
		}
		s.recordJobTransition(ctx, span, job, startTransition, JobStateRunning, "")
	}

	// Execute stages sequentially
	for _, stage := range job.Stages {
		if err := s.executeStage(ctx, stage); err != nil {
			log.Printf("Job %s failed at stage %s: %v", job.ID, stage.ID, err)

			// Check for policy violation
			var policyErr *policy.ErrPolicyViolation
			if errors.As(err, &policyErr) {
				s.updateJobStatus(job, JobBlocked)
				s.recordJobTransition(ctx, span, job, TransitionJobFailed, JobStateFailed, err.Error())
				span.SetStatus(telemetry.StatusError, "policy_blocked: "+policyErr.Reason)
			} else if isHumanPauseError(err) {
				s.updateJobStatus(job, JobAwaitingHuman)
				s.recordJobTransition(ctx, span, job, TransitionJobAwaitingHuman, JobStateAwaitingHuman, err.Error())
				span.SetStatus(telemetry.StatusOK, "job awaiting human input")
			} else {
				s.updateJobStatus(job, JobFailed)
				s.recordJobTransition(ctx, span, job, TransitionJobFailed, JobStateFailed, err.Error())
				span.SetStatus(telemetry.StatusError, err.Error())
			}
			return
		}
	}

	s.updateJobStatus(job, JobCompleted)
	s.recordJobTransition(ctx, span, job, TransitionJobSucceeded, JobStateSucceeded, "")
	span.SetStatus(telemetry.StatusOK, "Job completed successfully")
	log.Printf("Job %s completed successfully", job.ID)
}

func isHumanPauseError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "human interaction pending") ||
		strings.Contains(msg, "execution paused")
}

func normalizeTransitionErrorSummary(summary string) string {
	if summary == "" {
		return ""
	}

	// Replace non-printable control characters and collapse whitespace so
	// transition rows stay query-friendly across stores.
	var b strings.Builder
	b.Grow(len(summary))
	for _, r := range summary {
		if unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune(' ')
	}
	normalized := strings.Join(strings.Fields(strings.TrimSpace(b.String())), " ")
	if normalized == "" {
		return ""
	}

	if runeCount := len([]rune(normalized)); runeCount <= maxTransitionErrorSummaryRunes {
		return normalized
	}
	runes := []rune(normalized)
	return string(runes[:maxTransitionErrorSummaryRunes])
}

func marshalAgentInput(input *agent.AgentInput) string {
	if input == nil {
		return ""
	}
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return string(data)
}

// executeStage executes a stage and its tasks
func (s *Scheduler) executeStage(ctx context.Context, stage *Stage) error {
	stage.Status = JobRunning
	log.Printf("Starting stage %s with %d tasks", stage.ID, len(stage.Tasks))

	// Create a span for the stage
	stageCtx, span := s.tracer.StartSpan(ctx, "stage.execute")
	defer span.End()

	span.SetAttribute("stage.id", stage.ID)
	span.SetAttribute("task_count", len(stage.Tasks))

	// Get healthy nodes
	nodes := s.dispatchEligibleNodes(s.registry.GetHealthyNodes())
	if len(nodes) == 0 {
		err := fmt.Errorf("no healthy nodes available for execution")
		span.SetStatus(telemetry.StatusError, err.Error())
		return err
	}

	// Create a task queue and error channel.
	// errChan is buffered to len(stage.Tasks) so task goroutines can report one
	// error each without blocking on receiver timing.
	var wg sync.WaitGroup
	errChan := make(chan error, len(stage.Tasks))

	// Select nodes for each task using sticky scheduling
	var selectionErr error
	startedTasks := 0
	for _, task := range stage.Tasks {
		if err := stageCtx.Err(); err != nil {
			selectionErr = err
			break
		}
		node, affinityResult, ok, err := s.selectNodeForTaskWithDeferral(stageCtx, task, nodes)
		if err != nil {
			selectionErr = err
			break
		}
		if !ok {
			selectionErr = ErrNoWorkerCapacity
			break
		}
		startedTasks++

		// Add affinity info to span
		if affinityResult.PartitionKey != "" {
			span.SetAttribute("affinity.partition_key", affinityResult.PartitionKey)
			span.SetAttribute("affinity.is_hit", affinityResult.IsHit)
			span.SetAttribute("affinity.is_stale", affinityResult.IsStale)
		}

		wg.Add(1)
		go func(t *Task, n registry.NodeInfo) {
			defer wg.Done()
			if err := s.executeTaskWithRetry(stageCtx, t, n, nodes); err != nil {
				select {
				case <-stageCtx.Done():
					return
				case errChan <- err:
				}
			}
		}(task, node)
	}

	// Wait for all started tasks
	wg.Wait()
	close(errChan)

	if selectionErr != nil {
		s.metrics.RecordSchedulerAllWorkersFull()
		s.logger.Warn("no worker capacity available", map[string]interface{}{
			"stage_id":        stage.ID,
			"healthy_workers": len(nodes),
			"started_tasks":   startedTasks,
			"pending_tasks":   len(stage.Tasks) - startedTasks,
		})
		span.SetAttribute("scheduler.no_worker_capacity", true)
		span.SetAttribute("scheduler.started_tasks", startedTasks)
		span.SetAttribute("scheduler.pending_tasks", len(stage.Tasks)-startedTasks)
		stage.Status = JobFailed
		span.SetStatus(telemetry.StatusError, selectionErr.Error())
		return fmt.Errorf("stage %s failed: %w", stage.ID, selectionErr)
	}

	// Check for errors
	// For V1, any task failure fails the stage
	for err := range errChan {
		stage.Status = JobFailed
		// Check if any error was a policy violation
		var policyErr *policy.ErrPolicyViolation
		if errors.As(err, &policyErr) {
			stage.Status = JobBlocked
		}
		span.SetStatus(telemetry.StatusError, err.Error())
		return fmt.Errorf("stage %s failed: %w", stage.ID, err)
	}

	stage.Status = JobCompleted
	span.SetStatus(telemetry.StatusOK, "Stage completed")
	return nil
}

func (s *Scheduler) selectNodeForTaskWithDeferral(ctx context.Context, task *Task, initialNodes []registry.NodeInfo) (registry.NodeInfo, AffinityResult, bool, error) {
	node, affinity, ok := s.selectNodeForTask(task, initialNodes)
	if ok {
		return node, affinity, true, nil
	}
	if !s.config.EnableStageCapacityDeferral {
		return registry.NodeInfo{}, affinity, false, nil
	}

	s.metrics.RecordSchedulerCapacityDeferral()

	timeout := s.config.StageCapacityDeferralTimeout
	poll := s.config.StageCapacityDeferralPollInterval
	if timeout <= 0 || poll <= 0 {
		return registry.NodeInfo{}, affinity, false, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return registry.NodeInfo{}, affinity, false, ctx.Err()
		case <-timer.C:
			return registry.NodeInfo{}, affinity, false, nil
		case <-ticker.C:
			s.metrics.RecordSchedulerCapacityDeferralPoll()
			if s.registry == nil {
				return registry.NodeInfo{}, affinity, false, nil
			}
			nodes := s.dispatchEligibleNodes(s.registry.GetHealthyNodes())
			if len(nodes) == 0 {
				continue
			}
			node, affinity, ok = s.selectNodeForTask(task, nodes)
			if ok {
				return node, affinity, true, nil
			}
		}
	}
}

func (s *Scheduler) dispatchEligibleNodes(nodes []registry.NodeInfo) []registry.NodeInfo {
	if len(nodes) == 0 {
		return nil
	}
	eligible := make([]registry.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		if node.Metadata != nil {
			if role, ok := node.Metadata["role"]; ok && strings.EqualFold(strings.TrimSpace(role), "control-plane") {
				continue
			}
		}
		skip := false
		for _, capability := range node.Capabilities {
			if strings.EqualFold(strings.TrimSpace(capability), "control-plane") {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		eligible = append(eligible, node)
	}
	return eligible
}

func (s *Scheduler) executeTaskWithRetry(ctx context.Context, task *Task, initialNode registry.NodeInfo, healthyNodes []registry.NodeInfo) error {
	node := initialNode
	maxAttempts := s.config.MaxDispatchAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		task.Attempts = attempt
		if err := s.claimTaskDispatchTransition(ctx, s.tracer.GetSpan(ctx), task, node.ID, attempt); err != nil {
			if !errors.Is(err, ErrDispatchClaimRejected) {
				return err
			}
			s.metrics.RecordSchedulerDispatchRejection("fencing_conflict")
			if attempt == maxAttempts {
				s.metrics.RecordTaskFailedMaxDispatchAttempts()
				return err
			}
			s.metrics.RecordTaskDispatchRetry()
			nextNode, _, ok := s.selectNodeForTask(task, healthyNodes)
			if !ok {
				return ErrNoWorkerCapacity
			}
			node = nextNode
			continue
		}

		s.reserveNodeSlot(node.ID)
		err := s.executeTask(ctx, task, node)
		s.releaseNodeSlot(node.ID)

		if err == nil {
			return nil
		}
		if !isDispatchCapacityConflict(err) {
			return err
		}
		if attempt == maxAttempts {
			s.metrics.RecordTaskFailedMaxDispatchAttempts()
			return err
		}

		s.metrics.RecordTaskDispatchRetry()
		nextNode, _, ok := s.selectNodeForTask(task, healthyNodes)
		if !ok {
			return ErrNoWorkerCapacity
		}
		node = nextNode
	}

	return nil
}

func (s *Scheduler) claimTaskDispatchTransition(ctx context.Context, span telemetry.Span, task *Task, nodeID string, attempt int) error {
	if task == nil {
		return nil
	}
	// Legacy/unit-test compatibility path: if no JobID is present there is no
	// durable sequence scope to claim against. Preserve runtime behavior without
	// enforcing durable dispatch fencing in this path.
	if task.JobID == "" || s.transitionStore == nil {
		task.LifecycleState = TaskStateDispatched
		return nil
	}
	previousState := task.LifecycleState
	newState := TaskStateDispatched
	if previousState == "" {
		previousState = TaskStatePending
	}
	if !CanTransitionTaskState(previousState, newState) {
		return ErrDispatchClaimRejected
	}

	seqID, err := s.nextSequenceIDWithContext(ctx, task.JobID)
	if err != nil {
		return fmt.Errorf("allocate dispatch transition sequence: %w", err)
	}

	record := TransitionRecord{
		SequenceID:    seqID,
		EntityType:    TransitionEntityTask,
		Transition:    TransitionTaskDispatched,
		JobID:         task.JobID,
		TaskID:        task.ID,
		PreviousState: string(previousState),
		NewState:      string(newState),
		NodeID:        nodeID,
		Attempt:       attempt,
		OccurredAt:    time.Now().UTC(),
	}

	// Dispatch-claim write path:
	// 1. Allocate monotonic sequence (durable when SequenceIDStore is available).
	// 2. In one persistence callback, attempt CAS-style task claim when supported.
	// 3. Append transition and refresh durable job snapshot in same write boundary.
	// 4. If CAS reject occurs, return ErrDispatchClaimRejected for retry/alternate node.
	snapshot, hasSnapshot := s.durableJobSnapshot(task.JobID, record.SequenceID, record.OccurredAt)
	err = s.persistTransitionWrite(ctx, func(tx TransitionStoreTx) error {
		claimed := true
		if claimTx, ok := tx.(DispatchClaimTransitionStoreTx); ok {
			claimResult, claimErr := claimTx.ClaimTaskDispatch(ctx, TaskDispatchClaim{
				TaskID:    task.ID,
				JobID:     task.JobID,
				StageID:   task.StageID,
				NodeID:    nodeID,
				Attempt:   attempt,
				UpdatedAt: record.OccurredAt,
			})
			if claimErr != nil {
				return claimErr
			}
			claimed = claimResult
		} else {
			if err := tx.UpsertTask(ctx, DurableTaskRecord{
				TaskID:       task.ID,
				JobID:        task.JobID,
				StageID:      task.StageID,
				NodeID:       nodeID,
				AgentID:      task.AgentID,
				AgentName:    task.AgentName,
				InputJSON:    marshalAgentInput(task.Input),
				PartitionKey: task.PartitionKey,
				CurrentState: newState,
				LastAttempt:  attempt,
				UpdatedAt:    record.OccurredAt,
			}); err != nil {
				return err
			}
		}

		if !claimed {
			return ErrDispatchClaimRejected
		}
		if err := tx.AppendTransition(ctx, record); err != nil {
			return err
		}
		if hasSnapshot {
			if err := tx.UpsertJob(ctx, snapshot); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrDispatchClaimRejected) {
			return ErrDispatchClaimRejected
		}
		return fmt.Errorf("persist dispatch claim transition: %w", err)
	}

	task.LifecycleState = newState
	s.recordJobRunningOnFirstDispatch(ctx, span, task)
	if span != nil {
		span.AddEvent("scheduler.dispatch_claim", map[string]interface{}{
			"task_id":     task.ID,
			"job_id":      task.JobID,
			"node_id":     nodeID,
			"attempt":     attempt,
			"sequence_id": record.SequenceID,
		})
	}
	return nil
}

func (s *Scheduler) recordJobRunningOnFirstDispatch(ctx context.Context, span telemetry.Span, task *Task) {
	if task == nil || task.JobID == "" {
		return
	}
	s.mu.RLock()
	job := s.jobs[task.JobID]
	s.mu.RUnlock()
	if job == nil {
		return
	}
	switch job.LifecycleState {
	case JobStateSubmitted:
		s.recordJobTransition(ctx, span, job, TransitionJobQueued, JobStateQueued, "")
		s.recordJobTransition(ctx, span, job, TransitionJobRunning, JobStateRunning, "")
	case JobStateQueued:
		s.recordJobTransition(ctx, span, job, TransitionJobRunning, JobStateRunning, "")
	case JobStateAwaitingHuman:
		s.recordJobTransition(ctx, span, job, TransitionJobResumed, JobStateRunning, "")
	}
}

// executeTask executes a single task on a specific node
func (s *Scheduler) executeTask(ctx context.Context, task *Task, node registry.NodeInfo) error {
	task.Status = JobRunning

	// Create span for task
	taskCtx, span := s.tracer.StartSpan(ctx, "task.execute")
	defer span.End()

	span.SetAttribute("task.id", task.ID)
	span.SetAttribute("agent.id", task.AgentID)
	span.SetAttribute("node.id", node.ID)
	s.recordTaskTransition(taskCtx, span, task, TransitionTaskRunning, TaskStateRunning, node.ID, task.Attempts, "")

	log.Printf("Dispatching task %s (Agent: %s) to node %s", task.ID, task.AgentID, node.ID)

	authority, err := s.dispatchAuthority(taskCtx)
	if err != nil {
		return fmt.Errorf("resolve dispatch authority before remote execution: %w", err)
	}
	if !authority.IsLeader {
		return fmt.Errorf("%w: leader_id=%s epoch=%s", remote.ErrStaleDispatchAuthority, authority.LeaderID, authority.Epoch)
	}
	taskCtx = remote.WithDispatchAuthority(taskCtx, remote.DispatchAuthority{
		LeaderID: authority.LeaderID,
		Epoch:    authority.Epoch,
		Scope:    authority.Scope,
	})

	// Execute via TaskExecutor
	output, err := s.executor.ExecuteOnNode(taskCtx, node.ID, task.AgentName, task.Input)
	if err != nil {
		// Check for policy violation
		var policyErr *policy.ErrPolicyViolation
		if errors.As(err, &policyErr) {
			task.Status = JobBlocked
			span.SetStatus(telemetry.StatusError, "policy_violation: "+policyErr.Reason)
			span.SetAttribute("policy.violation", true)
			span.SetAttribute("policy.reason", policyErr.Reason)
			return err // Propagation will fail the stage
		}

		if isDispatchCapacityConflict(err) {
			s.metrics.RecordSchedulerDispatchRejection("capacity_conflict")
			s.markDispatchCooldown(node.ID)
			span.SetAttribute("scheduler.dispatch_cooldown", true)
		} else {
			s.metrics.RecordSchedulerDispatchRejection(dispatchRejectionReason(err))
		}

		task.Status = JobFailed
		s.recordTaskTransition(taskCtx, span, task, TransitionTaskFailed, TaskStateFailed, node.ID, task.Attempts, err.Error())
		span.SetStatus(telemetry.StatusError, err.Error())
		return fmt.Errorf("task %s failed on node %s: %w", task.ID, node.ID, err)
	}

	// Store output in the task object
	task.Output = output
	task.Status = JobCompleted
	s.recordTaskTransition(taskCtx, span, task, TransitionTaskSucceeded, TaskStateSucceeded, node.ID, task.Attempts, "")
	span.SetStatus(telemetry.StatusOK, "Task completed")
	return nil
}

// updateJobStatus updates the status of a job safely
func (s *Scheduler) updateJobStatus(job *Job, status JobStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job.Status = status
	job.UpdatedAt = time.Now()
}

func (s *Scheduler) recordInitialTransitions(ctx context.Context, job *Job) {
	if job == nil {
		return
	}
	s.recordJobTransition(ctx, nil, job, TransitionJobSubmitted, JobStateSubmitted, "")
	for _, stage := range job.Stages {
		for _, task := range stage.Tasks {
			if task.JobID == "" {
				task.JobID = job.ID
			}
			if task.StageID == "" {
				task.StageID = stage.ID
			}
			s.recordTaskTransition(ctx, nil, task, TransitionTaskCreated, TaskStatePending, "", 0, "")
		}
	}
	s.recordJobTransition(ctx, nil, job, TransitionJobQueued, JobStateQueued, "")
}

func (s *Scheduler) recordJobTransition(ctx context.Context, span telemetry.Span, job *Job, transition TransitionType, newState JobLifecycleState, errorSummary string) {
	if s.transitionStore == nil || job == nil {
		return
	}
	previousState := job.LifecycleState
	errorSummary = normalizeTransitionErrorSummary(errorSummary)
	if previousState != "" && !CanTransitionJobState(previousState, newState) {
		log.Printf("skipping invalid job lifecycle transition for job %s: %s -> %s (%s)", job.ID, previousState, newState, transition)
		return
	}
	if previousState == "" && newState != JobStateSubmitted {
		log.Printf("skipping non-initial job transition without previous state for job %s: -> %s (%s)", job.ID, newState, transition)
		return
	}

	seqID, err := s.nextSequenceIDWithContext(ctx, job.ID)
	if err != nil {
		log.Printf("failed to allocate transition sequence for job %s: %v", job.ID, err)
		return
	}

	record := TransitionRecord{
		SequenceID:    seqID,
		EntityType:    TransitionEntityJob,
		Transition:    transition,
		JobID:         job.ID,
		PreviousState: string(previousState),
		NewState:      string(newState),
		ErrorSummary:  errorSummary,
		OccurredAt:    time.Now().UTC(),
	}

	now := record.OccurredAt
	if err := s.persistTransitionWrite(ctx, func(tx TransitionStoreTx) error {
		if err := tx.AppendTransition(ctx, record); err != nil {
			return err
		}
		return tx.UpsertJob(ctx, DurableJobRecord{
			JobID:          job.ID,
			Name:           job.Name,
			CurrentState:   newState,
			LastSequenceID: record.SequenceID,
			CreatedAt:      job.CreatedAt,
			UpdatedAt:      now,
		})
	}); err != nil {
		log.Printf("failed to persist job transition for job %s: %v", job.ID, err)
		return
	}
	job.LifecycleState = newState

	if span != nil {
		span.AddEvent("scheduler.transition", map[string]interface{}{
			"entity_type":   string(TransitionEntityJob),
			"transition":    string(transition),
			"job_id":        job.ID,
			"previous":      string(previousState),
			"new":           string(newState),
			"sequence_id":   record.SequenceID,
			"error_summary": errorSummary,
		})
	}
}

func (s *Scheduler) recordTaskTransition(ctx context.Context, span telemetry.Span, task *Task, transition TransitionType, newState TaskLifecycleState, nodeID string, attempt int, errorSummary string) {
	if s.transitionStore == nil || task == nil {
		return
	}
	previousState := task.LifecycleState
	errorSummary = normalizeTransitionErrorSummary(errorSummary)
	if previousState != "" && !CanTransitionTaskState(previousState, newState) {
		log.Printf("skipping invalid task lifecycle transition for task %s: %s -> %s (%s)", task.ID, previousState, newState, transition)
		return
	}
	if previousState == "" && newState != TaskStatePending {
		log.Printf("skipping non-initial task transition without previous state for task %s: -> %s (%s)", task.ID, newState, transition)
		return
	}

	seqID, err := s.nextSequenceIDWithContext(ctx, task.JobID)
	if err != nil {
		log.Printf("failed to allocate transition sequence for task %s: %v", task.ID, err)
		return
	}

	record := TransitionRecord{
		SequenceID:    seqID,
		EntityType:    TransitionEntityTask,
		Transition:    transition,
		JobID:         task.JobID,
		TaskID:        task.ID,
		PreviousState: string(previousState),
		NewState:      string(newState),
		NodeID:        nodeID,
		Attempt:       attempt,
		ErrorSummary:  errorSummary,
		OccurredAt:    time.Now().UTC(),
	}

	// Capture the job snapshot outside the persistence callback to avoid lock
	// nesting between transition-store transaction paths and Scheduler.mu reads.
	// This keeps callback critical sections narrower and easier to reason about.
	snapshot, hasSnapshot := s.durableJobSnapshot(task.JobID, record.SequenceID, record.OccurredAt)

	if err := s.persistTransitionWrite(ctx, func(tx TransitionStoreTx) error {
		if err := tx.AppendTransition(ctx, record); err != nil {
			return err
		}
		if err := tx.UpsertTask(ctx, DurableTaskRecord{
			TaskID:       task.ID,
			JobID:        task.JobID,
			StageID:      task.StageID,
			NodeID:       nodeID,
			AgentID:      task.AgentID,
			AgentName:    task.AgentName,
			InputJSON:    marshalAgentInput(task.Input),
			PartitionKey: task.PartitionKey,
			CurrentState: newState,
			LastAttempt:  attempt,
			UpdatedAt:    record.OccurredAt,
		}); err != nil {
			return err
		}
		if hasSnapshot {
			return tx.UpsertJob(ctx, snapshot)
		}
		return nil
	}); err != nil {
		log.Printf("failed to persist task transition for task %s: %v", task.ID, err)
		return
	}
	task.LifecycleState = newState

	if span != nil {
		span.AddEvent("scheduler.transition", map[string]interface{}{
			"entity_type":   string(TransitionEntityTask),
			"transition":    string(transition),
			"job_id":        task.JobID,
			"task_id":       task.ID,
			"previous":      string(previousState),
			"new":           string(newState),
			"node_id":       nodeID,
			"attempt":       attempt,
			"sequence_id":   record.SequenceID,
			"error_summary": errorSummary,
		})
	}
}

func (s *Scheduler) durableJobSnapshot(jobID string, lastSequenceID uint64, updatedAt time.Time) (DurableJobRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if job, ok := s.jobs[jobID]; ok && job != nil {
		return DurableJobRecord{
			JobID:          job.ID,
			Name:           job.Name,
			CurrentState:   job.LifecycleState,
			LastSequenceID: lastSequenceID,
			CreatedAt:      job.CreatedAt,
			UpdatedAt:      updatedAt,
		}, true
	}
	return DurableJobRecord{}, false
}

func (s *Scheduler) persistTransitionWrite(ctx context.Context, fn func(tx TransitionStoreTx) error) error {
	if s.transitionStore == nil {
		return nil
	}
	if atomicStore, ok := s.transitionStore.(AtomicTransitionStore); ok {
		return atomicStore.WithTx(ctx, fn)
	}
	return fn(s.transitionStore)
}

func (s *Scheduler) nextSequenceID(jobID string) uint64 {
	seq, err := s.nextSequenceIDWithContext(context.Background(), jobID)
	if err == nil {
		return seq
	}
	log.Printf("falling back to in-memory sequence for job %s: %v", jobID, err)
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	s.jobSequences[jobID]++
	return s.jobSequences[jobID]
}

func (s *Scheduler) nextSequenceIDWithContext(ctx context.Context, jobID string) (uint64, error) {
	if sequenceStore, ok := s.transitionStore.(SequenceIDStore); ok {
		return sequenceStore.NextSequenceID(ctx, jobID)
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	s.jobSequences[jobID]++
	return s.jobSequences[jobID], nil
}

// selectNodeForTask selects a node for the given task using sticky scheduling
// If the task has a PartitionKey and stickiness is enabled, it will try to route
// to the same node as previous tasks with the same PartitionKey.
func (s *Scheduler) selectNodeForTask(task *Task, healthyNodes []registry.NodeInfo) (registry.NodeInfo, AffinityResult, bool) {
	result := AffinityResult{
		PartitionKey: task.PartitionKey,
	}

	// Build a set of healthy node IDs for quick lookup
	healthyNodeSet := make(map[string]registry.NodeInfo, len(healthyNodes))
	for _, node := range healthyNodes {
		healthyNodeSet[node.ID] = node
	}

	// If stickiness is disabled or no partition key, use round-robin
	if !s.config.EnableStickiness || s.affinityMap == nil || task.PartitionKey == "" {
		node, ok := s.selectNodeByCapacity(healthyNodes)
		if !ok {
			return registry.NodeInfo{}, result, false
		}
		result.NodeID = node.ID
		return node, result, true
	}

	// Check if we have an existing affinity for this partition key
	entry := s.affinityMap.Get(task.PartitionKey)

	if entry != nil {
		// Check if the affinity node is still healthy
		if node, healthy := healthyNodeSet[entry.NodeID]; healthy {
			if s.nodeHasAvailableCapacity(entry.NodeID) {
				// HIT: Use the sticky node
				s.affinityMap.Touch(task.PartitionKey)
				s.metrics.RecordSchedulerAffinityHit()
				result.NodeID = entry.NodeID
				result.IsHit = true
				log.Printf("[AFFINITY] HIT: PartitionKey=%s -> Node=%s (hits=%d)",
					task.PartitionKey, entry.NodeID, entry.HitCount+1)
				return node, result, true
			}

			log.Printf("[AFFINITY] CAPACITY_BYPASS: PartitionKey=%s, Node=%s is healthy but full, selecting alternate",
				task.PartitionKey, entry.NodeID)
		}

		// STALE: The affinity node is no longer healthy
		if _, healthy := healthyNodeSet[entry.NodeID]; !healthy {
			log.Printf("[AFFINITY] STALE: PartitionKey=%s, Node=%s is unhealthy, re-routing",
				task.PartitionKey, entry.NodeID)
			s.affinityMap.Delete(task.PartitionKey)
			s.metrics.RecordSchedulerAffinityStale()
			result.IsStale = true
		}
	}

	// MISS: No valid affinity exists, create one
	selectedNode, ok := s.selectNodeByCapacity(healthyNodes)
	if !ok {
		return registry.NodeInfo{}, result, false
	}
	s.affinityMap.Set(task.PartitionKey, selectedNode.ID)
	s.metrics.RecordSchedulerAffinityMiss()
	result.NodeID = selectedNode.ID

	log.Printf("[AFFINITY] MISS: PartitionKey=%s -> Node=%s (new affinity)",
		task.PartitionKey, selectedNode.ID)

	return selectedNode, result, true
}

func (s *Scheduler) selectNodeByCapacity(nodes []registry.NodeInfo) (registry.NodeInfo, bool) {
	if len(nodes) == 1 {
		if !s.nodeHasAvailableCapacity(nodes[0].ID) {
			return registry.NodeInfo{}, false
		}
		return nodes[0], true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	freshBestAvailable := -1
	freshBestIndexes := make([]int, 0, len(nodes))
	staleBestAvailable := -1
	staleBestIndexes := make([]int, 0, len(nodes))

	for i, node := range nodes {
		if s.isInDispatchCooldownLocked(node.ID) {
			continue
		}

		capacity := s.getOrCreateNodeCapacityLocked(node.ID)
		available := s.availableCapacityLocked(capacity)
		if s.isCapacityFreshLocked(capacity) {
			if available > freshBestAvailable {
				freshBestAvailable = available
				freshBestIndexes = []int{i}
				continue
			}
			if available == freshBestAvailable {
				freshBestIndexes = append(freshBestIndexes, i)
			}
			continue
		}

		if available > staleBestAvailable {
			staleBestAvailable = available
			staleBestIndexes = []int{i}
			continue
		}
		if available == staleBestAvailable {
			staleBestIndexes = append(staleBestIndexes, i)
		}
	}

	// Selection precedence:
	// 1) fresh-capacity nodes with available slots (best available, round-robin tie-break)
	// 2) if fresh nodes exist but all are full, fail fast (do not pick stale nodes)
	// 3) use stale-capacity nodes only when no fresh node data exists (degraded mode)
	if freshBestAvailable > 0 && len(freshBestIndexes) > 0 {
		selected := nodes[freshBestIndexes[s.nodeIndex%len(freshBestIndexes)]]
		s.nodeIndex++
		return selected, true
	}

	if len(freshBestIndexes) > 0 {
		return registry.NodeInfo{}, false
	}

	if staleBestAvailable <= 0 || len(staleBestIndexes) == 0 {
		return registry.NodeInfo{}, false
	}

	s.metrics.RecordSchedulerDegradedMode()
	selected := nodes[staleBestIndexes[s.nodeIndex%len(staleBestIndexes)]]
	s.nodeIndex++
	return selected, true
}

// roundRobinSelect selects the next node in round-robin fashion
func (s *Scheduler) roundRobinSelect(nodes []registry.NodeInfo) registry.NodeInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	node := nodes[s.nodeIndex%len(nodes)]
	s.nodeIndex++
	return node
}

func (s *Scheduler) reserveNodeSlot(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	capacity := s.getOrCreateNodeCapacityLocked(nodeID)
	capacity.ReservedInFlight++
	capacity.LastUpdated = time.Now()
}

func (s *Scheduler) releaseNodeSlot(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	capacity := s.getOrCreateNodeCapacityLocked(nodeID)
	if capacity.ReservedInFlight > 0 {
		capacity.ReservedInFlight--
	}
	capacity.LastUpdated = time.Now()
}

func (s *Scheduler) getOrCreateNodeCapacityLocked(nodeID string) *nodeCapacity {
	capacity, exists := s.nodeCapacity[nodeID]
	if !exists {
		capacity = &nodeCapacity{
			MaxConcurrency: s.config.DefaultWorkerMaxConcurrency,
			LastUpdated:    time.Now(),
		}
		s.nodeCapacity[nodeID] = capacity
	}
	return capacity
}

func (s *Scheduler) nodeHasAvailableCapacity(nodeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isInDispatchCooldownLocked(nodeID) {
		return false
	}

	capacity := s.getOrCreateNodeCapacityLocked(nodeID)
	return s.availableCapacityLocked(capacity) > 0
}

func (s *Scheduler) availableCapacityLocked(capacity *nodeCapacity) int {
	inUse := capacity.ReservedInFlight
	if capacity.ReportedInFlight > inUse {
		inUse = capacity.ReportedInFlight
	}
	return capacity.MaxConcurrency - inUse
}

func (s *Scheduler) isCapacityFreshLocked(capacity *nodeCapacity) bool {
	if capacity.LastUpdated.IsZero() {
		return false
	}
	return time.Since(capacity.LastUpdated) <= s.config.CapacityTTL
}

func (s *Scheduler) isInDispatchCooldownLocked(nodeID string) bool {
	expiry, exists := s.dispatchCooldowns[nodeID]
	if !exists {
		return false
	}
	if time.Now().After(expiry) {
		delete(s.dispatchCooldowns, nodeID)
		return false
	}
	return true
}

func (s *Scheduler) markDispatchCooldown(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dispatchCooldowns[nodeID] = time.Now().Add(s.config.DispatchRejectCooldown)
	s.metrics.RecordSchedulerDispatchCooldownActivation()
}

func (s *Scheduler) executionContextOrBackground() context.Context {
	s.mu.RLock()
	ctx := s.executionCtx
	s.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *Scheduler) reconcileContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.executionContextOrBackground(), s.config.ReconcileTimeout)
}

func (s *Scheduler) jobRetentionCleanupLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.config.JobRetentionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			pruned := s.cleanupTerminalJobsOnce(time.Now())
			if pruned > 0 {
				s.logger.Info("scheduler terminal job cleanup complete", map[string]interface{}{
					"pruned_jobs": pruned,
				})
			}
		}
	}
}

func (s *Scheduler) cleanupTerminalJobsOnce(now time.Time) int {
	if s.config.JobRetentionTTL <= 0 {
		return 0
	}
	cutoff := now.Add(-s.config.JobRetentionTTL)
	pruned := 0

	s.mu.Lock()
	defer s.mu.Unlock()
	for jobID, job := range s.jobs {
		if job == nil {
			delete(s.jobs, jobID)
			delete(s.jobSequences, jobID)
			delete(s.queuedJobs, jobID)
			continue
		}
		if !IsTerminalJobState(job.LifecycleState) {
			continue
		}
		if job.UpdatedAt.After(cutoff) {
			continue
		}
		delete(s.jobs, jobID)
		delete(s.jobSequences, jobID)
		delete(s.queuedJobs, jobID)
		pruned++
	}
	return pruned
}

func isDispatchCapacityConflict(err error) bool {
	if err == nil {
		return false
	}
	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "at capacity") || strings.Contains(lowered, "capacity")
}

func dispatchRejectionReason(err error) string {
	if err == nil {
		return "unknown"
	}

	lowered := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, remote.ErrStaleDispatchAuthority),
		errors.Is(err, remote.ErrMissingDispatchAuthority):
		return remote.AuthorityRejectionReason(err)
	case strings.Contains(lowered, "timeout"):
		return "network_timeout"
	case strings.Contains(lowered, "connection refused"),
		strings.Contains(lowered, "connection reset"),
		strings.Contains(lowered, "unavailable"):
		return "transport_error"
	default:
		return "dispatch_error"
	}
}

// GetAffinityStats returns statistics about the current affinity map (for observability)
func (s *Scheduler) GetAffinityStats() map[string]interface{} {
	stats := map[string]interface{}{
		"enabled": s.config.EnableStickiness,
		"ttl":     s.config.AffinityTTL.String(),
	}

	if s.affinityMap != nil {
		stats["size"] = s.affinityMap.Size()
		stats["entries"] = s.affinityMap.GetAll()
	}

	return stats
}
