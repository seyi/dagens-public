package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchedulerReadiness(t *testing.T) {
	s := NewScheduler(nil, nil)

	readiness := s.Readiness()
	assert.False(t, readiness.IsRecovering)
	assert.False(t, readiness.IsLeader)
	assert.True(t, readiness.LastReconcileTime.IsZero())

	now := time.Now().UTC()

	s.mu.Lock()
	s.started = true
	s.recovering = true
	s.lastReconcileTime = now
	s.mu.Unlock()

	readiness = s.Readiness()
	assert.True(t, readiness.IsRecovering)
	assert.False(t, readiness.IsLeader)
	assert.Equal(t, now, readiness.LastReconcileTime)

	s.mu.Lock()
	s.recovering = false
	s.leadership = staticLeadershipProvider{
		authority: LeadershipAuthority{IsLeader: true, LeaderID: "node-a", Epoch: "1"},
	}
	s.mu.Unlock()

	readiness = s.Readiness()
	assert.False(t, readiness.IsRecovering)
	assert.True(t, readiness.IsLeader)
	assert.Equal(t, now, readiness.LastReconcileTime)
}

func TestReconcileDurableQueuedJobsOnce_FollowerWarmReplayUpdatesReadiness(t *testing.T) {
	store := NewInMemoryTransitionStore()
	seedDurableQueuedJobForReconcileTest(t, store, "job-follower-warm")

	cfg := DefaultSchedulerConfig()
	cfg.EnableLeaderDurableRequeueLoop = false
	cfg.EnableFollowerDurableRequeueLoop = true

	s := NewSchedulerWithConfig(nil, nil, cfg)
	require.NoError(t, s.SetTransitionStore(store))
	require.NoError(t, s.SetLeadershipProvider(staticLeadershipProvider{
		authority: LeadershipAuthority{IsLeader: false, LeaderID: "node-a", Epoch: "1"},
	}))

	before := time.Now()
	require.NoError(t, s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeFollower))

	job, err := s.GetJob("job-follower-warm")
	require.NoError(t, err)
	assert.Equal(t, JobStateQueued, job.LifecycleState)

	s.mu.RLock()
	_, inQueue := s.queuedJobs["job-follower-warm"]
	s.mu.RUnlock()
	assert.False(t, inQueue, "follower warm replay must not enqueue jobs")

	readiness := s.Readiness()
	assert.False(t, readiness.LastReconcileTime.IsZero())
	assert.False(t, readiness.LastReconcileTime.Before(before))
	assert.False(t, readiness.IsLeader)
}

func TestReconcileContext_UsesConfiguredTimeout(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.ReconcileTimeout = 150 * time.Millisecond

	s := NewSchedulerWithConfig(nil, nil, cfg)
	base, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	s.mu.Lock()
	s.executionCtx = base
	s.mu.Unlock()

	ctx, cancel := s.reconcileContext()
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(cfg.ReconcileTimeout), deadline, 75*time.Millisecond)
}

func TestReconcileDurableQueuedJobsOnce_RespectsReconcileTimeout(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.ReconcileTimeout = 100 * time.Millisecond

	blockStore := &blockingTransitionStore{
		TransitionStore: NewInMemoryTransitionStore(),
		started:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}

	s := NewSchedulerWithConfig(nil, nil, cfg)
	require.NoError(t, s.SetTransitionStore(blockStore))

	ctx, cancel := s.reconcileContext()
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.reconcileDurableQueuedJobsOnce(ctx, ReconcileModeFollower)
	}()

	select {
	case <-blockStore.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconcile to start")
	}

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
	case <-time.After(500 * time.Millisecond):
		t.Fatal("reconcile did not respect timeout")
	}
}

func TestFailDurableQueuedJobForReconcile_BlockedWithoutLeadership(t *testing.T) {
	now := time.Now().UTC()
	store := NewInMemoryTransitionStore()
	s := NewSchedulerWithConfig(nil, nil, DefaultSchedulerConfig())
	require.NoError(t, s.SetTransitionStore(store))

	provider := &toggleLeadershipProvider{}
	provider.setLeader(false)
	require.NoError(t, s.SetLeadershipProvider(provider))

	job := &Job{
		ID:             "job-follower-blocked",
		Name:           "job-follower-blocked",
		Status:         JobPending,
		LifecycleState: JobStateQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()

	store.jobs[job.ID] = DurableJobRecord{
		JobID:          job.ID,
		Name:           job.Name,
		CurrentState:   JobStateQueued,
		LastSequenceID: 3,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	s.failDurableQueuedJobForReconcile(context.Background(), store.jobs[job.ID], "should-not-mutate")

	reloaded, err := s.GetJob(job.ID)
	require.NoError(t, err)
	assert.Equal(t, JobStateQueued, reloaded.LifecycleState)

	transitions, err := store.ListTransitionsByJob(context.Background(), job.ID)
	require.NoError(t, err)
	assert.Len(t, transitions, 0)
	assert.Equal(t, JobStateQueued, store.jobs[job.ID].CurrentState)
}

func TestFailDurableQueuedJobForReconcile_MutatesWhenLeader(t *testing.T) {
	now := time.Now().UTC()
	store := NewInMemoryTransitionStore()
	s := NewSchedulerWithConfig(nil, nil, DefaultSchedulerConfig())
	require.NoError(t, s.SetTransitionStore(store))

	provider := &toggleLeadershipProvider{}
	provider.setLeader(true)
	require.NoError(t, s.SetLeadershipProvider(provider))

	job := &Job{
		ID:             "job-leader-fail",
		Name:           "job-leader-fail",
		Status:         JobPending,
		LifecycleState: JobStateQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()

	store.jobs[job.ID] = DurableJobRecord{
		JobID:          job.ID,
		Name:           job.Name,
		CurrentState:   JobStateQueued,
		LastSequenceID: 3,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	s.failDurableQueuedJobForReconcile(context.Background(), store.jobs[job.ID], "leader-mutation")

	reloaded, err := s.GetJob(job.ID)
	require.NoError(t, err)
	assert.Equal(t, JobStateFailed, reloaded.LifecycleState)

	transitions, err := store.ListTransitionsByJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.Len(t, transitions, 1)
	assert.Equal(t, TransitionJobFailed, transitions[0].Transition)
}
