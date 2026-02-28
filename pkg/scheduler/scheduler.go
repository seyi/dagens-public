package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/policy"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/telemetry"
)

// TaskExecutor defines the interface for executing tasks on nodes
type TaskExecutor interface {
	ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error)
}

// Scheduler manages the execution of jobs and their tasks
type Scheduler struct {
	registry    registry.Registry // Interface for node discovery
	executor    TaskExecutor
	jobs        map[string]*Job
	jobQueue    chan *Job
	mu          sync.RWMutex
	stopChan    chan struct{}
	wg          sync.WaitGroup
	config      SchedulerConfig
	affinityMap *AffinityMap // Sticky scheduling affinity tracking
	nodeIndex   int          // Round-robin counter for node selection
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

	return &Scheduler{
		registry:    reg,
		executor:    executor,
		jobs:        make(map[string]*Job),
		jobQueue:    make(chan *Job, config.JobQueueSize),
		stopChan:    make(chan struct{}),
		config:      config,
		affinityMap: affinityMap,
	}
}

// Start starts the scheduler's main loop
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go s.run()
}

// Stop stops the scheduler and cleans up resources
func (s *Scheduler) Stop() {
	close(s.stopChan)
	s.wg.Wait()

	// Stop affinity map cleanup goroutine
	if s.affinityMap != nil {
		s.affinityMap.Stop()
	}
}

// SubmitJob submits a job for execution
func (s *Scheduler) SubmitJob(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("job %s already exists", job.ID)
	}

	job.Status = JobPending
	s.jobs[job.ID] = job

	select {
	case s.jobQueue <- job:
		return nil
	default:
		return fmt.Errorf("job queue is full")
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

// run is the main scheduler loop
func (s *Scheduler) run() {
	defer s.wg.Done()

	for {
		select {
		case <-s.stopChan:
			return
		case job := <-s.jobQueue:
			s.executeJob(job)
		}
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

	// Execute stages sequentially
	for _, stage := range job.Stages {
		if err := s.executeStage(ctx, stage); err != nil {
			log.Printf("Job %s failed at stage %s: %v", job.ID, stage.ID, err)
			
			// Check for policy violation
			var policyErr *policy.ErrPolicyViolation
			if errors.As(err, &policyErr) {
				s.updateJobStatus(job, JobBlocked)
				span.SetStatus(telemetry.StatusError, "policy_blocked: "+policyErr.Reason)
			} else {
				s.updateJobStatus(job, JobFailed)
				span.SetStatus(telemetry.StatusError, err.Error())
			}
			return
		}
	}

	s.updateJobStatus(job, JobCompleted)
	span.SetStatus(telemetry.StatusOK, "Job completed successfully")
	log.Printf("Job %s completed successfully", job.ID)
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
	for _, task := range stage.Tasks {
		node, affinityResult := s.selectNodeForTask(task, nodes)

		// Add affinity info to span
		if affinityResult.PartitionKey != "" {
			span.SetAttribute("affinity.partition_key", affinityResult.PartitionKey)
			span.SetAttribute("affinity.is_hit", affinityResult.IsHit)
			span.SetAttribute("affinity.is_stale", affinityResult.IsStale)
		}

		wg.Add(1)
		go func(t *Task, n registry.NodeInfo) {
			defer wg.Done()

			if err := s.executeTask(stageCtx, t, n); err != nil {
				errChan <- err
			}
		}(task, node)
	}

	// Wait for all tasks
	wg.Wait()
	close(errChan)

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
		return err
	}

	stage.Status = JobCompleted
	span.SetStatus(telemetry.StatusOK, "Stage completed")
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

		task.Status = JobFailed
		span.SetStatus(telemetry.StatusError, err.Error())
		return fmt.Errorf("task %s failed on node %s: %w", task.ID, node.ID, err)
	}

	// Store output in the task object
	task.Output = output
	task.Status = JobCompleted
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

// selectNodeForTask selects a node for the given task using sticky scheduling
// If the task has a PartitionKey and stickiness is enabled, it will try to route
// to the same node as previous tasks with the same PartitionKey.
func (s *Scheduler) selectNodeForTask(task *Task, healthyNodes []registry.NodeInfo) (registry.NodeInfo, AffinityResult) {
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
		node := s.roundRobinSelect(healthyNodes)
		result.NodeID = node.ID
		return node, result
	}

	// Check if we have an existing affinity for this partition key
	entry := s.affinityMap.Get(task.PartitionKey)

	if entry != nil {
		// Check if the affinity node is still healthy
		if node, healthy := healthyNodeSet[entry.NodeID]; healthy {
			// HIT: Use the sticky node
			s.affinityMap.Touch(task.PartitionKey)
			result.NodeID = entry.NodeID
			result.IsHit = true
			log.Printf("[AFFINITY] HIT: PartitionKey=%s -> Node=%s (hits=%d)",
				task.PartitionKey, entry.NodeID, entry.HitCount+1)
			return node, result
		}

		// STALE: The affinity node is no longer healthy
		log.Printf("[AFFINITY] STALE: PartitionKey=%s, Node=%s is unhealthy, re-routing",
			task.PartitionKey, entry.NodeID)
		s.affinityMap.Delete(task.PartitionKey)
		result.IsStale = true
	}

	// MISS: No valid affinity exists, create one
	selectedNode := s.roundRobinSelect(healthyNodes)
	s.affinityMap.Set(task.PartitionKey, selectedNode.ID)
	result.NodeID = selectedNode.ID

	log.Printf("[AFFINITY] MISS: PartitionKey=%s -> Node=%s (new affinity)",
		task.PartitionKey, selectedNode.ID)

	return selectedNode, result
}

// roundRobinSelect selects the next node in round-robin fashion
func (s *Scheduler) roundRobinSelect(nodes []registry.NodeInfo) registry.NodeInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	node := nodes[s.nodeIndex%len(nodes)]
	s.nodeIndex++
	return node
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