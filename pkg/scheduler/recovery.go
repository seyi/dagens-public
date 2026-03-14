package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

const recoveryContextCheckEvery = 32

// RecoverFromTransitions rebuilds the scheduler's in-memory visibility state
// from transition replay. This is visibility-first recovery: it restores job
// and task views but does not enqueue or redispatch work.
func (s *Scheduler) RecoverFromTransitions(ctx context.Context) error {
	start := time.Now()
	metrics := s.metrics
	logger := s.logger

	if s.transitionStore == nil {
		metrics.RecordSchedulerRecoveryRun("no_store")
		metrics.RecordSchedulerRecoveryDuration(time.Since(start))
		logger.Info("scheduler startup recovery skipped: no transition store", nil)
		return nil
	}

	replayed, err := ReplayStateFromStore(ctx, s.transitionStore)
	if err != nil {
		status := "failed"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = "canceled"
		}
		jobID := ""
		replayReason := err.Error()
		var replayErr *ReplayJobError
		if errors.As(err, &replayErr) {
			jobID = replayErr.JobID
			if replayErr.Err != nil {
				replayReason = replayErr.Err.Error()
			}
		}
		metrics.RecordSchedulerRecoveryRun(status)
		metrics.RecordSchedulerRecoveryDuration(time.Since(start))
		logger.Warn("scheduler startup recovery failed", map[string]interface{}{
			"error":        err.Error(),
			"status":       status,
			"job_id":       jobID,
			"replay_error": replayReason,
		})
		s.emitOperationalAlert(ctx, "scheduler.recovery.failed", "critical",
			"scheduler startup recovery failed", map[string]interface{}{
				"status":       status,
				"job_id":       jobID,
				"replay_error": replayReason,
				"error":        err.Error(),
			})
		return err
	}

	recoveredJobs := 0
	seededJobs := 0
	resumedQueuedJobs := 0
	skippedQueuedJobs := 0
	skippedUnsafeQueuedJobs := 0
	var seededMaxSequence uint64
	recoveredForResume := make([]*Job, 0, len(replayed))
	processed := 0
	batchSize := s.config.RecoveryBatchSize
	if batchSize <= 0 {
		batchSize = 128
	}
	for batchStart := 0; batchStart < len(replayed); batchStart += batchSize {
		if err := ctx.Err(); err != nil {
			metrics.RecordSchedulerRecoveryRun("canceled")
			metrics.RecordSchedulerRecoveryDuration(time.Since(start))
			logger.Warn("scheduler startup recovery canceled during apply", map[string]interface{}{
				"error":          err.Error(),
				"processed_jobs": processed,
				"total_jobs":     len(replayed),
			})
			return err
		}

		batchEnd := batchStart + batchSize
		if batchEnd > len(replayed) {
			batchEnd = len(replayed)
		}

		s.mu.Lock()
		for _, r := range replayed[batchStart:batchEnd] {
			processed++
			if processed%recoveryContextCheckEvery == 0 {
				if err := ctx.Err(); err != nil {
					s.mu.Unlock()
					metrics.RecordSchedulerRecoveryRun("canceled")
					metrics.RecordSchedulerRecoveryDuration(time.Since(start))
					logger.Warn("scheduler startup recovery canceled during locked apply", map[string]interface{}{
						"error":          err.Error(),
						"processed_jobs": processed,
						"total_jobs":     len(replayed),
					})
					return err
				}
			}

			if _, exists := s.jobs[r.Job.JobID]; exists {
				seeded, maxSeq := s.seedJobSequenceLocked(r.Job.JobID, r.Job.LastSequenceID, r.Transitions)
				if seeded {
					seededJobs++
					if maxSeq > seededMaxSequence {
						seededMaxSequence = maxSeq
					}
				}
				continue
			}

			job := buildRecoveredRuntimeJob(r)

			s.jobs[job.ID] = job
			recoveredForResume = append(recoveredForResume, job)
			seeded, maxSeq := s.seedJobSequenceLocked(job.ID, r.Job.LastSequenceID, r.Transitions)
			if seeded {
				seededJobs++
				if maxSeq > seededMaxSequence {
					seededMaxSequence = maxSeq
				}
			}
			recoveredJobs++
		}
		s.mu.Unlock()
		if err := ctx.Err(); err != nil {
			metrics.RecordSchedulerRecoveryRun("canceled")
			metrics.RecordSchedulerRecoveryDuration(time.Since(start))
			logger.Warn("scheduler startup recovery canceled after batch apply", map[string]interface{}{
				"error":          err.Error(),
				"processed_jobs": processed,
				"total_jobs":     len(replayed),
			})
			return err
		}
	}

	if s.config.EnableResumeRecoveredQueuedJobs {
		s.mu.Lock()
		resumedQueuedJobs, skippedQueuedJobs, skippedUnsafeQueuedJobs = s.resumeRecoveredQueuedJobsLocked(recoveredForResume)
		s.mu.Unlock()
	}

	metrics.RecordSchedulerRecoveryRun("succeeded")
	metrics.RecordSchedulerRecoveredJobs(recoveredJobs)
	metrics.RecordSchedulerRecoveryResumedQueuedJobs(resumedQueuedJobs)
	metrics.RecordSchedulerRecoveryResumeSkippedQueuedJobs(skippedQueuedJobs)
	metrics.RecordSchedulerRecoveryResumeSkippedUnsafeQueuedJobs(skippedUnsafeQueuedJobs)
	metrics.RecordSchedulerRecoveryDuration(time.Since(start))
	logger.Info("scheduler startup recovery completed", map[string]interface{}{
		"recovered_jobs":             recoveredJobs,
		"seeded_jobs":                seededJobs,
		"seeded_max_sequence":        seededMaxSequence,
		"resumed_queued_jobs":        resumedQueuedJobs,
		"skipped_queued_jobs":        skippedQueuedJobs,
		"skipped_unsafe_queued_jobs": skippedUnsafeQueuedJobs,
	})

	return nil
}

func (s *Scheduler) resumeRecoveredQueuedJobsLocked(recoveredJobs []*Job) (int, int, int) {
	resumed := 0
	skipped := 0
	skippedUnsafe := 0
	logger := s.logger
	for _, job := range recoveredJobs {
		if job == nil || job.LifecycleState != JobStateQueued {
			continue
		}
		if !isSafeForQueuedResume(job) {
			skippedUnsafe++
			logger.Warn("recovered queued job skipped due to non-pending task state", map[string]interface{}{
				"job_id": job.ID,
			})
			continue
		}
		if missingTaskFields := recoveredQueuedMissingExecutionFieldCount(job, s.config.EnableStickiness); missingTaskFields > 0 {
			// Recovery intentionally rebuilds visibility-first task state only. We
			// warn here to make it explicit when queued auto-resume proceeds with
			// tasks that do not carry full execution payload fields.
			logger.Warn("recovered queued job has tasks missing execution fields; resume relies on downstream task hydration", map[string]interface{}{
				"job_id":               job.ID,
				"missing_task_fields":  missingTaskFields,
				"total_stages":         len(job.Stages),
				"visibility_only_view": true,
			})
		}
		if s.enqueueJobLocked(job) {
			resumed++
			continue
		}
		skipped++
		logger.Warn("recovered queued job skipped due to queue saturation", map[string]interface{}{
			"job_id":         job.ID,
			"queue_depth":    len(s.jobQueue),
			"queue_capacity": cap(s.jobQueue),
		})
	}
	s.metrics.SetTaskQueueDepths(schedulerMetricsID, len(s.jobQueue), cap(s.jobQueue))
	return resumed, skipped, skippedUnsafe
}

func buildRecoveredRuntimeJob(r ReplayedJobState) *Job {
	job := &Job{
		ID:             r.Job.JobID,
		Name:           r.Job.Name,
		Stages:         make([]*Stage, 0),
		Status:         runtimeJobStatusFromLifecycle(r.Job.CurrentState),
		LifecycleState: r.Job.CurrentState,
		CreatedAt:      r.Job.CreatedAt,
		UpdatedAt:      r.Job.UpdatedAt,
		// Recovery currently rebuilds a minimal visibility view and does not
		// preserve arbitrary prior runtime metadata keys.
		Metadata: map[string]interface{}{
			"recovered": true,
		},
	}
	// NOTE: This is a visibility-first reconstruction. Task execution payload
	// fields (AgentID, AgentName, Input, Output, PartitionKey) are not restored
	// from transition replay and must be hydrated by higher-level job/task state
	// sources before full re-execution assumptions are made.

	stageByID := make(map[string]*Stage)
	for _, taskRecord := range r.Tasks {
		stageID := taskRecord.StageID
		if stageID == "" {
			stageID = "recovered"
		}

		stage, ok := stageByID[stageID]
		if !ok {
			stage = &Stage{
				ID:     stageID,
				JobID:  job.ID,
				Tasks:  make([]*Task, 0),
				Status: JobPending,
			}
			stageByID[stageID] = stage
		}

		stage.Tasks = append(stage.Tasks, &Task{
			ID:             taskRecord.TaskID,
			StageID:        stageID,
			JobID:          taskRecord.JobID,
			AgentID:        taskRecord.AgentID,
			AgentName:      taskRecord.AgentName,
			Input:          unmarshalRecoveredAgentInput(taskRecord.InputJSON),
			PartitionKey:   taskRecord.PartitionKey,
			Status:         runtimeTaskStatusFromLifecycle(taskRecord.CurrentState),
			LifecycleState: taskRecord.CurrentState,
			Attempts:       taskRecord.LastAttempt,
		})
	}

	stageIDs := make([]string, 0, len(stageByID))
	for id := range stageByID {
		stageIDs = append(stageIDs, id)
	}
	sort.Strings(stageIDs)
	for _, id := range stageIDs {
		stage := stageByID[id]
		stage.Status = deriveStageStatus(stage.Tasks)
		job.Stages = append(job.Stages, stage)
	}

	return job
}

func unmarshalRecoveredAgentInput(raw string) *agent.AgentInput {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var input agent.AgentInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil
	}
	return &input
}

// isSafeForQueuedResume enforces the startup queued-job resume safety policy.
// Empty task lifecycle state is treated as safe/unknown (equivalent to pending
// for resume gating), while any progressed non-pending task state blocks
// automatic resume for the recovered QUEUED job.
func isSafeForQueuedResume(job *Job) bool {
	if job == nil {
		return false
	}
	for _, stage := range job.Stages {
		if stage == nil {
			continue
		}
		for _, task := range stage.Tasks {
			if task == nil {
				continue
			}
			// Only pending/unknown tasks are safe to auto-resume for a recovered
			// QUEUED job. Any progressed lifecycle state remains visibility-only.
			if task.LifecycleState != "" && task.LifecycleState != TaskStatePending {
				return false
			}
		}
	}
	return true
}

func recoveredQueuedMissingExecutionFieldCount(job *Job, stickinessRequired bool) int {
	if job == nil {
		return 0
	}
	missing := 0
	for _, stage := range job.Stages {
		if stage == nil {
			continue
		}
		for _, task := range stage.Tasks {
			if task == nil {
				continue
			}
			missingExecutionFields := task.AgentID == "" || task.AgentName == "" || task.Input == nil
			missingStickinessField := stickinessRequired && task.PartitionKey == ""
			if missingExecutionFields || missingStickinessField {
				missing++
			}
		}
	}
	return missing
}

func (s *Scheduler) seedJobSequenceLocked(jobID string, durableLastSeq uint64, transitions []TransitionRecord) (bool, uint64) {
	maxSeq := durableLastSeq
	for _, tr := range transitions {
		if tr.SequenceID > maxSeq {
			maxSeq = tr.SequenceID
		}
	}
	if maxSeq == 0 {
		return false, 0
	}
	if current, ok := s.jobSequences[jobID]; !ok || maxSeq > current {
		s.jobSequences[jobID] = maxSeq
		return true, maxSeq
	}
	return false, maxSeq
}

func runtimeJobStatusFromLifecycle(state JobLifecycleState) JobStatus {
	switch state {
	case JobStateRunning:
		return JobRunning
	case JobStateAwaitingHuman:
		return JobAwaitingHuman
	case JobStateSucceeded:
		return JobCompleted
	case JobStateFailed, JobStateCanceled:
		return JobFailed
	case JobStateSubmitted, JobStateQueued:
		return JobPending
	default:
		return JobPending
	}
}

func runtimeTaskStatusFromLifecycle(state TaskLifecycleState) JobStatus {
	switch state {
	case TaskStateRunning:
		return JobRunning
	case TaskStateSucceeded:
		return JobCompleted
	case TaskStateFailed:
		return JobFailed
	case TaskStatePending, TaskStateDispatched:
		return JobPending
	default:
		return JobPending
	}
}

func deriveStageStatus(tasks []*Task) JobStatus {
	// INVARIANT: task.Status is expected to be synchronized with
	// task.LifecycleState (via runtimeTaskStatusFromLifecycle during recovery
	// reconstruction) before this helper is called.
	if len(tasks) == 0 {
		return JobPending
	}

	hasRunning := false
	hasFailed := false
	allCompleted := true

	for _, task := range tasks {
		switch task.Status {
		case JobFailed, JobBlocked:
			hasFailed = true
			allCompleted = false
		case JobRunning:
			hasRunning = true
			allCompleted = false
		case JobCompleted:
			// keep scanning
		default:
			allCompleted = false
		}
	}

	if hasFailed {
		return JobFailed
	}
	if hasRunning {
		return JobRunning
	}
	if allCompleted {
		return JobCompleted
	}
	return JobPending
}
