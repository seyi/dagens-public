package scheduler

import (
	"context"
	"sort"
	"time"

	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/telemetry"
)

// RecoverFromTransitions rebuilds the scheduler's in-memory visibility state
// from transition replay. This is visibility-first recovery: it restores job
// and task views but does not enqueue or redispatch work.
func (s *Scheduler) RecoverFromTransitions(ctx context.Context) error {
	start := time.Now()
	metrics := observability.GetMetrics()
	logger := telemetry.GetGlobalTelemetry().GetLogger()

	if s.transitionStore == nil {
		metrics.RecordSchedulerRecoveryRun("no_store")
		metrics.RecordSchedulerRecoveryDuration(time.Since(start))
		logger.Info("scheduler startup recovery skipped: no transition store", nil)
		return nil
	}

	replayed, err := ReplayStateFromStore(ctx, s.transitionStore)
	if err != nil {
		metrics.RecordSchedulerRecoveryRun("failed")
		metrics.RecordSchedulerRecoveryDuration(time.Since(start))
		logger.Warn("scheduler startup recovery failed", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	recoveredJobs := 0
	for _, r := range replayed {
		if _, exists := s.jobs[r.Job.JobID]; exists {
			continue
		}

		job := &Job{
			ID:             r.Job.JobID,
			Name:           r.Job.Name,
			Stages:         make([]*Stage, 0),
			Status:         runtimeJobStatusFromLifecycle(r.Job.CurrentState),
			LifecycleState: r.Job.CurrentState,
			CreatedAt:      r.Job.CreatedAt,
			UpdatedAt:      r.Job.UpdatedAt,
			Metadata: map[string]interface{}{
				"recovered": true,
			},
		}

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

		s.jobs[job.ID] = job
		recoveredJobs++
	}

	metrics.RecordSchedulerRecoveryRun("succeeded")
	metrics.RecordSchedulerRecoveredJobs(recoveredJobs)
	metrics.RecordSchedulerRecoveryDuration(time.Since(start))
	logger.Info("scheduler startup recovery completed", map[string]interface{}{
		"recovered_jobs": recoveredJobs,
	})

	return nil
}

func runtimeJobStatusFromLifecycle(state JobLifecycleState) JobStatus {
	switch state {
	case JobStateRunning:
		return JobRunning
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
