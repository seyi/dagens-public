//go:build integration

package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/seyi/dagens/pkg/retention"
	"github.com/seyi/dagens/pkg/scheduler"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func withRetentionPostgres(t *testing.T, fn func(ctx context.Context, pool *pgxpool.Pool)) {
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

	require.NoError(t, pool.Ping(ctx))
	fn(ctx, pool)
}

func TestQueryVisibilitySnapshotRejectsInvalidRetentionDays(t *testing.T) {
	_, err := retention.QueryVisibilitySnapshot(context.Background(), nil, 0)
	require.Error(t, err)
}

func TestQueryVisibilitySnapshotPopulatesBucketsAndEligibleRows(t *testing.T) {
	withRetentionPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := scheduler.NewPostgresTransitionStore(ctx, pool)
		require.NoError(t, err)

		now := time.Now().UTC()
		insertJob := func(jobID string, state scheduler.JobLifecycleState, updatedAt time.Time, transitionCount int) {
			require.NoError(t, store.UpsertJob(ctx, scheduler.DurableJobRecord{
				JobID:          jobID,
				Name:           jobID,
				CurrentState:   state,
				LastSequenceID: uint64(transitionCount),
				CreatedAt:      now.Add(-24 * time.Hour),
				UpdatedAt:      now,
			}))
			for i := 1; i <= transitionCount; i++ {
				record := scheduler.TransitionRecord{
					SequenceID: uint64(i),
					EntityType: scheduler.TransitionEntityJob,
					JobID:      jobID,
					OccurredAt: now.Add(time.Duration(i) * time.Second),
				}
				switch i {
				case 1:
					record.Transition = scheduler.TransitionJobSubmitted
					record.NewState = string(scheduler.JobStateSubmitted)
				case 2:
					record.Transition = scheduler.TransitionJobQueued
					record.PreviousState = string(scheduler.JobStateSubmitted)
					record.NewState = string(scheduler.JobStateQueued)
				case 3:
					record.Transition = scheduler.TransitionJobRunning
					record.PreviousState = string(scheduler.JobStateQueued)
					record.NewState = string(scheduler.JobStateRunning)
				default:
					record.Transition = scheduler.TransitionJobSucceeded
					record.PreviousState = string(scheduler.JobStateRunning)
					record.NewState = string(scheduler.JobStateSucceeded)
				}
				require.NoError(t, store.AppendTransition(ctx, scheduler.TransitionRecord{
					SequenceID:    record.SequenceID,
					EntityType:    record.EntityType,
					Transition:    record.Transition,
					JobID:         record.JobID,
					PreviousState: record.PreviousState,
					NewState:      record.NewState,
					OccurredAt:    record.OccurredAt,
				}))
			}
			if state != scheduler.JobStateSubmitted && state != scheduler.JobStateQueued && state != scheduler.JobStateRunning && state != scheduler.JobStateAwaitingHuman {
				require.NoError(t, store.UpsertTask(ctx, scheduler.DurableTaskRecord{
					TaskID:       "task-" + jobID,
					JobID:        jobID,
					StageID:      "stage-1",
					NodeID:       "node-1",
					AgentID:      "agent-1",
					AgentName:    "agent",
					CurrentState: scheduler.TaskStateSucceeded,
					LastAttempt:  1,
					UpdatedAt:    updatedAt,
				}))
			}
			_, err := store.NextSequenceID(ctx, jobID)
			require.NoError(t, err)
			_, err = pool.Exec(ctx, `UPDATE scheduler_durable_jobs SET updated_at = $2 WHERE job_id = $1`, jobID, updatedAt)
			require.NoError(t, err)
		}

		insertJob("job-unfinished", scheduler.JobStateRunning, now.Add(-2*time.Hour), 1)
		insertJob("job-recent", scheduler.JobStateSucceeded, now.Add(-2*24*time.Hour), 1)
		insertJob("job-mid", scheduler.JobStateFailed, now.Add(-10*24*time.Hour), 1)
		insertJob("job-old", scheduler.JobStateSucceeded, now.Add(-20*24*time.Hour), 2)

		snapshot, err := retention.QueryVisibilitySnapshot(ctx, pool, 14)
		require.NoError(t, err)
		require.EqualValues(t, 1, snapshot.UnfinishedJobs)
		require.EqualValues(t, 1, bucketJobs(snapshot.TerminalBuckets, "lt_7d"))
		require.EqualValues(t, 1, bucketJobs(snapshot.TerminalBuckets, "7_to_14d"))
		require.EqualValues(t, 1, bucketJobs(snapshot.TerminalBuckets, "14_to_30d"))
		require.EqualValues(t, 1, snapshot.EligibleArchive.Jobs)
		require.EqualValues(t, 2, snapshot.EligibleArchive.TransitionRows)
		require.EqualValues(t, 1, snapshot.EligibleArchive.TaskRows)
		require.EqualValues(t, 1, snapshot.EligibleArchive.SequenceRows)
	})
}

func TestQueryVisibilitySnapshotPropagatesQueryFailure(t *testing.T) {
	withRetentionPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		pool.Close()
		_, err := retention.QueryVisibilitySnapshot(ctx, pool, 14)
		require.Error(t, err)
	})
}

func bucketJobs(buckets []retention.TerminalAgeBucket, label string) int64 {
	for _, bucket := range buckets {
		if bucket.Label == label {
			return bucket.Jobs
		}
	}
	return 0
}
