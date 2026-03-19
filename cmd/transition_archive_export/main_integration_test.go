//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/scheduler"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func withArchiveExportPostgres(t *testing.T, fn func(ctx context.Context, pool *pgxpool.Pool, store *scheduler.PostgresTransitionStore)) {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("dagens_test"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("postgres container unavailable: %v", err)
	}
	defer testcontainers.TerminateContainer(pgContainer) //nolint:errcheck

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	store, err := scheduler.NewPostgresTransitionStore(ctx, pool)
	require.NoError(t, err)

	fn(ctx, pool, store)
}

func TestListEligibleJobsRespectsPrefixAndAge(t *testing.T) {
	withArchiveExportPostgres(t, func(ctx context.Context, pool *pgxpool.Pool, store *scheduler.PostgresTransitionStore) {
		seedTerminalArchiveJob(t, ctx, pool, store, "archive-prefix-a-001", 2, 21, scheduler.JobStateSucceeded, scheduler.TaskStateSucceeded)
		seedTerminalArchiveJob(t, ctx, pool, store, "archive-prefix-a-002", 2, 21, scheduler.JobStateFailed, scheduler.TaskStateFailed)
		seedTerminalArchiveJob(t, ctx, pool, store, "archive-prefix-b-001", 2, 21, scheduler.JobStateSucceeded, scheduler.TaskStateSucceeded)
		seedTerminalArchiveJob(t, ctx, pool, store, "archive-prefix-a-fresh", 2, 2, scheduler.JobStateSucceeded, scheduler.TaskStateSucceeded)

		jobs, err := listEligibleJobs(ctx, pool, 14, 10, "archive-prefix-a-")
		require.NoError(t, err)
		require.Len(t, jobs, 2)
		require.Equal(t, "archive-prefix-a-001", jobs[0].JobID)
		require.Equal(t, "archive-prefix-a-002", jobs[1].JobID)
	})
}

func TestExportJobBundleWritesBundleAndChecksum(t *testing.T) {
	withArchiveExportPostgres(t, func(ctx context.Context, pool *pgxpool.Pool, store *scheduler.PostgresTransitionStore) {
		jobID := "archive-export-job-001"
		seedTerminalArchiveJob(t, ctx, pool, store, jobID, 3, 21, scheduler.JobStateSucceeded, scheduler.TaskStateSucceeded)

		tmpDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "bundles"), 0o755))

		job := eligibleJob{
			JobID:        jobID,
			CurrentState: string(scheduler.JobStateSucceeded),
			UpdatedAt:    time.Now().UTC().Add(-21 * 24 * time.Hour),
		}
		bundle, exported, err := exportJobBundle(ctx, pool, time.Now().UTC(), 14, tmpDir, job)
		require.NoError(t, err)
		require.Equal(t, archiveSchemaVersion, bundle.SchemaVersion)
		require.Equal(t, 3, exported.TaskRows)
		require.Greater(t, exported.TransitionRows, 0)
		require.NotEmpty(t, exported.SHA256)

		data, err := os.ReadFile(filepath.Join(tmpDir, exported.RelativePath))
		require.NoError(t, err)

		var decoded archiveBundle
		require.NoError(t, json.Unmarshal(data, &decoded))
		require.Equal(t, jobID, decoded.Job.JobID)
		require.Equal(t, 3, len(decoded.Tasks))
	})
}

func TestWriteResultsIncludesSelectedJobIDsForDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	result := exportResult{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Config: exportConfig{
			DryRun:       true,
			EvidenceRoot: tmpDir,
		},
		Manifest: exportManifest{
			SchemaVersion: archiveSchemaVersion,
			SelectedJobs:  2,
			ExportedJobs:  0,
		},
		SelectedJobIDs: []string{"job-a", "job-b"},
	}
	require.NoError(t, writeResults(tmpDir, result))

	data, err := os.ReadFile(filepath.Join(tmpDir, "result.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"dry_run": true`)
	require.Contains(t, string(data), `"selected_job_ids": [`)
	require.Contains(t, string(data), `"job-a"`)
}

func seedTerminalArchiveJob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *scheduler.PostgresTransitionStore, jobID string, tasksPerJob int, ageDays int, finalJobState scheduler.JobLifecycleState, finalTaskState scheduler.TaskLifecycleState) {
	t.Helper()
	terminalAt := time.Now().UTC().Add(-time.Duration(ageDays) * 24 * time.Hour)
	createdAt := terminalAt.Add(-10 * time.Minute)
	sequence := uint64(1)

	appendTransition := func(record scheduler.TransitionRecord) {
		require.NoError(t, store.AppendTransition(ctx, record))
		sequence++
	}

	appendTransition(scheduler.TransitionRecord{
		SequenceID: sequence, EntityType: scheduler.TransitionEntityJob, Transition: scheduler.TransitionJobSubmitted,
		JobID: jobID, NewState: string(scheduler.JobStateSubmitted), OccurredAt: createdAt,
	})
	appendTransition(scheduler.TransitionRecord{
		SequenceID: sequence, EntityType: scheduler.TransitionEntityJob, Transition: scheduler.TransitionJobQueued,
		JobID: jobID, PreviousState: string(scheduler.JobStateSubmitted), NewState: string(scheduler.JobStateQueued), OccurredAt: createdAt.Add(time.Second),
	})
	appendTransition(scheduler.TransitionRecord{
		SequenceID: sequence, EntityType: scheduler.TransitionEntityJob, Transition: scheduler.TransitionJobRunning,
		JobID: jobID, PreviousState: string(scheduler.JobStateQueued), NewState: string(scheduler.JobStateRunning), OccurredAt: createdAt.Add(2 * time.Second),
	})

	for taskIndex := 0; taskIndex < tasksPerJob; taskIndex++ {
		taskID := fmt.Sprintf("%s-task-%03d", jobID, taskIndex)
		appendTransition(scheduler.TransitionRecord{
			SequenceID: sequence, EntityType: scheduler.TransitionEntityTask, Transition: scheduler.TransitionTaskCreated,
			JobID: jobID, TaskID: taskID, NewState: string(scheduler.TaskStatePending), OccurredAt: createdAt.Add(time.Duration(3+taskIndex) * time.Second),
		})
		finalTransition := scheduler.TransitionTaskSucceeded
		if finalTaskState == scheduler.TaskStateFailed {
			finalTransition = scheduler.TransitionTaskFailed
		}
		appendTransition(scheduler.TransitionRecord{
			SequenceID: sequence, EntityType: scheduler.TransitionEntityTask, Transition: finalTransition,
			JobID: jobID, TaskID: taskID, PreviousState: string(scheduler.TaskStatePending), NewState: string(finalTaskState), OccurredAt: createdAt.Add(time.Duration(3+tasksPerJob+taskIndex) * time.Second),
		})
		require.NoError(t, store.UpsertTask(ctx, scheduler.DurableTaskRecord{
			TaskID:       taskID,
			JobID:        jobID,
			StageID:      "stage-1",
			NodeID:       "node-1",
			AgentID:      "agent-1",
			AgentName:    "agent-1",
			InputJSON:    mustJSONForTest(agent.AgentInput{Instruction: "archive export test task"}),
			PartitionKey: "partition-1",
			CurrentState: finalTaskState,
			LastAttempt:  1,
			UpdatedAt:    terminalAt,
		}))
	}

	jobFinalTransition := scheduler.TransitionJobSucceeded
	if finalJobState == scheduler.JobStateFailed {
		jobFinalTransition = scheduler.TransitionJobFailed
	}
	appendTransition(scheduler.TransitionRecord{
		SequenceID: sequence, EntityType: scheduler.TransitionEntityJob, Transition: jobFinalTransition,
		JobID: jobID, PreviousState: string(scheduler.JobStateRunning), NewState: string(finalJobState), OccurredAt: terminalAt,
	})

	require.NoError(t, store.UpsertJob(ctx, scheduler.DurableJobRecord{
		JobID:          jobID,
		Name:           "archive-export-test",
		CurrentState:   finalJobState,
		LastSequenceID: sequence - 1,
		CreatedAt:      createdAt,
		UpdatedAt:      terminalAt,
	}))
	_, err := pool.Exec(ctx, `
		INSERT INTO scheduler_job_sequences (job_id, last_sequence_id, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (job_id) DO UPDATE SET
			last_sequence_id = EXCLUDED.last_sequence_id,
			updated_at = EXCLUDED.updated_at
	`, jobID, int64(sequence-1), terminalAt)
	require.NoError(t, err)
}

func mustJSONForTest(v interface{}) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
