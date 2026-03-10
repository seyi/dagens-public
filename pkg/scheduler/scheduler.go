package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/policy"
	"github.com/seyi/dagens/pkg/registry"
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

type nodeCapacity struct {
	ReservedInFlight int
	ReportedInFlight int
	MaxConcurrency   int
	LastUpdated      time.Time
}

// Scheduler manages the execution of jobs and their tasks
type Scheduler struct {
	registry          registry.Registry // Interface for node discovery
	executor          TaskExecutor
	jobs              map[string]*Job
	jobQueue          chan *Job
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

// NewSchedulerWithConfig creates a new scheduler with the specified configuration
func NewSchedulerWithConfig(reg registry.Registry, executor TaskExecutor, config SchedulerConfig) *Scheduler {
	config.Validate()

	var affinityMap *AffinityMap
	if config.EnableStickiness {
		affinityMap = NewAffinityMap(config.AffinityTTL, config.AffinityCleanupInterval)
	}

	s := &Scheduler{
		registry:          reg,
		executor:          executor,
		jobs:              make(map[string]*Job),
		jobQueue:          make(chan *Job, config.JobQueueSize),
		stopChan:          make(chan struct{}),
		config:            config,
		affinityMap:       affinityMap,
		nodeCapacity:      make(map[string]*nodeCapacity),
		dispatchCooldowns: make(map[string]time.Time),
		transitionStore:   NewInMemoryTransitionStore(),
		jobSequences:      make(map[string]uint64),
		leadership:        defaultLeadershipProvider(),
	}
	observability.GetMetrics().SetTaskQueueDepths(schedulerMetricsID, 0, cap(s.jobQueue))
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
	}

	if err := s.startLeadershipProvider(); err != nil {
		log.Printf("failed to start scheduler leadership provider: %v", err)
		return
	}

	s.mu.Lock()
	if s.stopRequested {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	s.wg.Add(1)
	go s.run()
}

// Stop stops the scheduler and cleans up resources
func (s *Scheduler) Stop() {
	s.mu.Lock()
	s.stopRequested = true
	cancel := s.recoveryCancel
	leadershipCancel := s.leadershipCancel
	leadershipStop := s.leadershipStop
	s.leadershipCancel = nil
	s.leadershipStop = nil
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

	metrics := observability.GetMetrics()
	select {
	case s.jobQueue <- job:
		job.Status = JobPending
		s.jobs[job.ID] = job
		metrics.SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
		s.mu.Unlock()
		s.recordInitialTransitions(ctx, job)
		return nil
	default:
		metrics.SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
		s.mu.Unlock()
		return ErrJobQueueFull
	}
}

// GetJob returns a job by ID
func (s *Scheduler) GetJob(jobID string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	job, exists := s.jobs[jobID]
	if !exists {
		return nil, fmt.Errorf("job %s not found", jobID)
	}
	return job, nil
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
		telemetry.GetGlobalTelemetry().GetLogger().Warn("node capacity report missing timestamp; using server receipt time", map[string]interface{}{
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
			observability.GetMetrics().SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
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
			s.executeJob(job)
		}
	}
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
	case s.jobQueue <- job:
		observability.GetMetrics().SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
	}
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

// executeJob executes a single job
func (s *Scheduler) executeJob(job *Job) {
	s.updateJobStatus(job, JobRunning)
	log.Printf("Starting execution of job %s (%s)", job.ID, job.Name)

	// Create a root span for the job
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	ctx, span := tracer.StartSpan(context.Background(), "job.execute")
	defer span.End()

	span.SetAttribute("job.id", job.ID)
	span.SetAttribute("job.name", job.Name)
	startTransition := TransitionJobRunning
	if job.LifecycleState == JobStateAwaitingHuman {
		startTransition = TransitionJobResumed
	}
	s.recordJobTransition(ctx, span, job, startTransition, JobStateRunning, "")

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

// executeStage executes a stage and its tasks
func (s *Scheduler) executeStage(ctx context.Context, stage *Stage) error {
	stage.Status = JobRunning
	log.Printf("Starting stage %s with %d tasks", stage.ID, len(stage.Tasks))

	// Create a span for the stage
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	stageCtx, span := tracer.StartSpan(ctx, "stage.execute")
	defer span.End()

	span.SetAttribute("stage.id", stage.ID)
	span.SetAttribute("task_count", len(stage.Tasks))

	// Get healthy nodes
	nodes := s.registry.GetHealthyNodes()
	if len(nodes) == 0 {
		err := fmt.Errorf("no healthy nodes available for execution")
		span.SetStatus(telemetry.StatusError, err.Error())
		return err
	}

	// Create a task queue and error channel
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
				errChan <- err
			}
		}(task, node)
	}

	// Wait for all started tasks
	wg.Wait()
	close(errChan)

	if selectionErr != nil {
		observability.GetMetrics().RecordSchedulerAllWorkersFull()
		telemetry.GetGlobalTelemetry().GetLogger().Warn("no worker capacity available", map[string]interface{}{
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

	observability.GetMetrics().RecordSchedulerCapacityDeferral()

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
			observability.GetMetrics().RecordSchedulerCapacityDeferralPoll()
			if s.registry == nil {
				return registry.NodeInfo{}, affinity, false, nil
			}
			nodes := s.registry.GetHealthyNodes()
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

func (s *Scheduler) executeTaskWithRetry(ctx context.Context, task *Task, initialNode registry.NodeInfo, healthyNodes []registry.NodeInfo) error {
	node := initialNode
	maxAttempts := s.config.MaxDispatchAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		task.Attempts = attempt
		if err := s.claimTaskDispatchTransition(ctx, telemetry.GetGlobalTelemetry().GetTracer().GetSpan(ctx), task, node.ID, attempt); err != nil {
			if !errors.Is(err, ErrDispatchClaimRejected) {
				return err
			}
			observability.GetMetrics().RecordSchedulerDispatchRejection("fencing_conflict")
			if attempt == maxAttempts {
				observability.GetMetrics().RecordTaskFailedMaxDispatchAttempts()
				return err
			}
			observability.GetMetrics().RecordTaskDispatchRetry()
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
			observability.GetMetrics().RecordTaskFailedMaxDispatchAttempts()
			return err
		}

		observability.GetMetrics().RecordTaskDispatchRetry()
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

// executeTask executes a single task on a specific node
func (s *Scheduler) executeTask(ctx context.Context, task *Task, node registry.NodeInfo) error {
	task.Status = JobRunning

	// Create span for task
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	taskCtx, span := tracer.StartSpan(ctx, "task.execute")
	defer span.End()

	span.SetAttribute("task.id", task.ID)
	span.SetAttribute("agent.id", task.AgentID)
	span.SetAttribute("node.id", node.ID)
	s.recordTaskTransition(taskCtx, span, task, TransitionTaskRunning, TaskStateRunning, node.ID, task.Attempts, "")

	log.Printf("Dispatching task %s (Agent: %s) to node %s", task.ID, task.AgentID, node.ID)

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
			observability.GetMetrics().RecordSchedulerDispatchRejection("capacity_conflict")
			s.markDispatchCooldown(node.ID)
			span.SetAttribute("scheduler.dispatch_cooldown", true)
		} else {
			observability.GetMetrics().RecordSchedulerDispatchRejection(dispatchRejectionReason(err))
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
				observability.GetMetrics().RecordSchedulerAffinityHit()
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
			observability.GetMetrics().RecordSchedulerAffinityStale()
			result.IsStale = true
		}
	}

	// MISS: No valid affinity exists, create one
	selectedNode, ok := s.selectNodeByCapacity(healthyNodes)
	if !ok {
		return registry.NodeInfo{}, result, false
	}
	s.affinityMap.Set(task.PartitionKey, selectedNode.ID)
	observability.GetMetrics().RecordSchedulerAffinityMiss()
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

	observability.GetMetrics().RecordSchedulerDegradedMode()
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
	observability.GetMetrics().RecordSchedulerDispatchCooldownActivation()
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
