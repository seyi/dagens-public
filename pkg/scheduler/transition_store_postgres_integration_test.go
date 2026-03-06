//go:build integration

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func withSchedulerPostgres(t *testing.T, fn func(ctx context.Context, pool *pgxpool.Pool)) {
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

func TestPostgresTransitionStoreRoundTrip(t *testing.T) {
	withSchedulerPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresTransitionStore(ctx, pool)
		require.NoError(t, err)

		now := time.Now().UTC()
		require.NoError(t, store.AppendTransition(ctx, TransitionRecord{
			SequenceID:    1,
			EntityType:    TransitionEntityJob,
			Transition:    TransitionJobSubmitted,
			JobID:         "job-1",
			PreviousState: "",
			NewState:      string(JobStateSubmitted),
			OccurredAt:    now,
		}))
		require.NoError(t, store.UpsertJob(ctx, DurableJobRecord{
			JobID:        "job-1",
			Name:         "Job One",
			CurrentState: JobStateQueued,
			CreatedAt:    now,
			UpdatedAt:    now,
		}))

		require.NoError(t, store.AppendTransition(ctx, TransitionRecord{
			SequenceID:    2,
			EntityType:    TransitionEntityTask,
			Transition:    TransitionTaskCreated,
			JobID:         "job-1",
			TaskID:        "task-1",
			PreviousState: "",
			NewState:      string(TaskStatePending),
			OccurredAt:    now.Add(time.Second),
		}))
		require.NoError(t, store.UpsertTask(ctx, DurableTaskRecord{
			TaskID:       "task-1",
			JobID:        "job-1",
			StageID:      "stage-1",
			NodeID:       "worker-1",
			CurrentState: TaskStatePending,
			LastAttempt:  0,
			UpdatedAt:    now.Add(time.Second),
		}))

		jobs, err := store.ListUnfinishedJobs(ctx)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		require.Equal(t, "job-1", jobs[0].JobID)

		transitions, err := store.ListTransitionsByJob(ctx, "job-1")
		require.NoError(t, err)
		require.Len(t, transitions, 2)
		require.EqualValues(t, 1, transitions[0].SequenceID)
		require.EqualValues(t, 2, transitions[1].SequenceID)
	})
}

func TestPostgresTransitionStoreWithTxRollsBackOnError(t *testing.T) {
	withSchedulerPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresTransitionStore(ctx, pool)
		require.NoError(t, err)

		now := time.Now().UTC()
		expectedErr := errors.New("force rollback")
		err = store.WithTx(ctx, func(tx TransitionStoreTx) error {
			if err := tx.AppendTransition(ctx, TransitionRecord{
				SequenceID: 1,
				EntityType: TransitionEntityJob,
				Transition: TransitionJobSubmitted,
				JobID:      "job-rollback",
				NewState:   string(JobStateSubmitted),
				OccurredAt: now,
			}); err != nil {
				return err
			}
			if err := tx.UpsertJob(ctx, DurableJobRecord{
				JobID:        "job-rollback",
				Name:         "rollback",
				CurrentState: JobStateSubmitted,
				CreatedAt:    now,
				UpdatedAt:    now,
			}); err != nil {
				return err
			}
			return expectedErr
		})
		require.ErrorIs(t, err, expectedErr)

		transitions, err := store.ListTransitionsByJob(ctx, "job-rollback")
		require.NoError(t, err)
		require.Len(t, transitions, 0)

		jobs, err := store.ListUnfinishedJobs(ctx)
		require.NoError(t, err)
		for _, job := range jobs {
			require.NotEqual(t, "job-rollback", job.JobID)
		}
	})
}

func TestRecoverFromTransitionsSeedsSequenceForPostgresStore(t *testing.T) {
	withSchedulerPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresTransitionStore(ctx, pool)
		require.NoError(t, err)

		now := time.Now().UTC()
		require.NoError(t, store.AppendTransition(ctx, TransitionRecord{
			SequenceID: 4,
			EntityType: TransitionEntityJob,
			Transition: TransitionJobSubmitted,
			JobID:      "job-seq-pg",
			NewState:   string(JobStateSubmitted),
			OccurredAt: now,
		}))
		require.NoError(t, store.AppendTransition(ctx, TransitionRecord{
			SequenceID:    5,
			EntityType:    TransitionEntityJob,
			Transition:    TransitionJobQueued,
			JobID:         "job-seq-pg",
			PreviousState: string(JobStateSubmitted),
			NewState:      string(JobStateQueued),
			OccurredAt:    now.Add(time.Second),
		}))
		require.NoError(t, store.UpsertJob(ctx, DurableJobRecord{
			JobID:        "job-seq-pg",
			Name:         "job-seq-pg",
			CurrentState: JobStateQueued,
			CreatedAt:    now,
			UpdatedAt:    now.Add(time.Second),
		}))

		s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
		require.NoError(t, s.SetTransitionStore(store))
		require.NoError(t, s.RecoverFromTransitions(ctx))

		require.EqualValues(t, 6, s.nextSequenceID("job-seq-pg"))
	})
}
