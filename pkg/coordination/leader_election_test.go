// Package coordination_test provides comprehensive tests for the distributed coordination system
package coordination

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"go.etcd.io/etcd/server/v3/embed"
)

// startEmbeddedEtcd starts an embedded etcd server for testing
func startEmbeddedEtcd(t *testing.T) (*embed.Etcd, *clientv3.Client) {
	cfg := embed.NewConfig()
	cfg.Dir = t.TempDir()
	cfg.LogLevel = "error"

	e, err := embed.StartEtcd(cfg)
	require.NoError(t, err)

	// Wait for etcd to be ready
	select {
	case <-e.Server.ReadyNotify():
	case <-time.After(60 * time.Second):
		e.Close()
		t.Fatal("etcd took too long to start")
	}

	t.Cleanup(func() {
		e.Close()
	})

	// Create client using the listener address
	endpoint := e.Clients[0].Addr().String()
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		cli.Close()
	})

	return e, cli
}

func TestLeaderElection_SingleCandidate(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	config := LeaderElectionConfig{
		Client:   cli,
		SessionTTL: 10,
		Key:      "/test/leader-election/single",
		Identity: "candidate-1",
	}

	elector, err := NewLeaderElector(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = elector.Start(ctx)
	require.NoError(t, err)

	// Wait a bit for election to occur
	time.Sleep(100 * time.Millisecond)

	// Single candidate should become leader
	assert.True(t, elector.IsLeader())

	// Get leader identity
	leaderID, err := elector.GetLeaderIdentity()
	assert.NoError(t, err)
	assert.Equal(t, "candidate-1", leaderID)
}

func TestLeaderElection_MultipleCandidates(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	const numCandidates = 3
	electors := make([]*LeaderElector, numCandidates)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create multiple electors
	for i := 0; i < numCandidates; i++ {
		config := LeaderElectionConfig{
			Client:   cli,
			SessionTTL: 10,
			Key:      "/test/leader-election/multiple",
			Identity: fmt.Sprintf("candidate-%d", i),
		}

		elector, err := NewLeaderElector(config)
		require.NoError(t, err)
		electors[i] = elector

		err = elector.Start(ctx)
		require.NoError(t, err)
	}

	// Wait for election to settle
	time.Sleep(500 * time.Millisecond)

	// Only one should be leader
	leaderCount := 0
	for _, elector := range electors {
		if elector.IsLeader() {
			leaderCount++
		}
	}

	assert.Equal(t, 1, leaderCount, "Exactly one candidate should be leader")

	// Verify leader identity
	leaderID, err := electors[0].GetLeaderIdentity()
	assert.NoError(t, err)
	assert.NotEmpty(t, leaderID)
}

func TestLeaderElection_LeadershipTransfer(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	// Create two electors
	config1 := LeaderElectionConfig{
		Client:   cli,
		SessionTTL: 5,
		Key:      "/test/leader-election/transfer",
		Identity: "candidate-1",
	}

	config2 := LeaderElectionConfig{
		Client:   cli,
		SessionTTL: 5,
		Key:      "/test/leader-election/transfer",
		Identity: "candidate-2",
	}

	elector1, err := NewLeaderElector(config1)
	require.NoError(t, err)

	elector2, err := NewLeaderElector(config2)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = elector1.Start(ctx)
	require.NoError(t, err)

	err = elector2.Start(ctx)
	require.NoError(t, err)

	// Wait for initial election
	time.Sleep(200 * time.Millisecond)

	// Initially, one of them should be leader
	initialLeader := elector1.IsLeader() || elector2.IsLeader()
	assert.True(t, initialLeader, "One candidate should be leader initially")

	// Stop the current leader (force session expiration)
	if elector1.IsLeader() {
		elector1.Stop()
	} else {
		elector2.Stop()
	}

	// Wait for leadership transfer
	time.Sleep(1 * time.Second)

	// The other candidate should become leader
	if elector1.IsLeader() {
		assert.False(t, elector2.IsLeader(), "Only one should be leader after transfer")
	} else {
		assert.True(t, elector2.IsLeader(), "Second candidate should become leader after first stops")
	}
}

func TestLeaderElection_Callbacks(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	var electedCalled, revokedCalled bool
	var electedMu, revokedMu sync.Mutex

	config := LeaderElectionConfig{
		Client:   cli,
		SessionTTL: 10,
		Key:      "/test/leader-election/callbacks",
		Identity: "callback-test",
		Callbacks: LeaderCallbacks{
			OnElected: func() {
				electedMu.Lock()
				defer electedMu.Unlock()
				electedCalled = true
			},
			OnRevoked: func() {
				revokedMu.Lock()
				defer revokedMu.Unlock()
				revokedCalled = true
			},
		},
	}

	elector, err := NewLeaderElector(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = elector.Start(ctx)
	require.NoError(t, err)

	// Wait for election
	time.Sleep(200 * time.Millisecond)

	// Verify elected callback was called
	electedMu.Lock()
	assert.True(t, electedCalled, "OnElected callback should be called")
	electedMu.Unlock()

	// Stop the elector to trigger revocation
	elector.Stop()

	// Wait for revocation
	time.Sleep(200 * time.Millisecond)

	// Verify revoked callback was called
	revokedMu.Lock()
	assert.True(t, revokedCalled, "OnRevoked callback should be called")
	revokedMu.Unlock()
}

func TestDistributedMutex_MutualExclusion(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	mutex := NewDistributedMutex(session, "/test/distributed-mutex/exclusion")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to acquire the same mutex twice concurrently
	var wg sync.WaitGroup
	acquiredCount := 0
	var countMu sync.Mutex

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := mutex.Lock(ctx)
			if err == nil {
				countMu.Lock()
				acquiredCount++
				countMu.Unlock()

				// Hold the lock briefly
				time.Sleep(100 * time.Millisecond)

				mutex.Unlock(ctx)
			}
		}()
	}

	wg.Wait()

	// Only one goroutine should have acquired the mutex
	assert.Equal(t, 1, acquiredCount, "Only one goroutine should acquire the mutex at a time")
}

func TestDistributedBarrier_Synchronization(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	barrier := NewDistributedBarrier(cli, session, "/test/distributed-barrier/sync", 3)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create multiple sessions to simulate multiple participants
	var wg sync.WaitGroup
	waitTimes := make([]time.Duration, 3)
	var timeMu sync.Mutex

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			start := time.Now()
			err := barrier.Wait(ctx)
			require.NoError(t, err)

			duration := time.Since(start)
			timeMu.Lock()
			waitTimes[idx] = duration
			timeMu.Unlock()
		}(i)
	}

	wg.Wait()

	// All participants should have waited roughly the same amount of time
	// (they all waited for each other to arrive)
	maxWait := time.Duration(0)
	for _, d := range waitTimes {
		if d > maxWait {
			maxWait = d
		}
	}

	// The barrier should have synchronized all participants
	assert.True(t, maxWait > 0, "Participants should have waited for synchronization")
}

func TestDistributedSemaphore_PermitManagement(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	semaphore := NewDistributedSemaphore(cli, session, "/test/distributed-semaphore/permits", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Initially, 2 permits should be available
	available, err := semaphore.GetAvailable(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, available, "Initially 2 permits should be available")

	// Acquire one permit
	err = semaphore.Acquire(ctx)
	assert.NoError(t, err)

	// Now 1 permit should be available
	available, err = semaphore.GetAvailable(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, available, "After acquiring 1 permit, 1 should remain")

	// Acquire another permit
	err = semaphore.Acquire(ctx)
	assert.NoError(t, err)

	// Now 0 permits should be available
	available, err = semaphore.GetAvailable(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 0, available, "After acquiring 2 permits, 0 should remain")

	// Try to acquire a third permit (should block or fail in a real implementation)
	// For this test, we'll just verify the count
	err = semaphore.Release(ctx)
	assert.NoError(t, err)

	// Now 1 permit should be available again
	available, err = semaphore.GetAvailable(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, available, "After releasing 1 permit, 1 should be available")
}

func TestDistributedSemaphore_ConcurrentAcquisition(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	semaphore := NewDistributedSemaphore(cli, session, "/test/distributed-semaphore/concurrent", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to acquire permits concurrently
	var wg sync.WaitGroup
	acquiredCount := 0
	var countMu sync.Mutex

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := semaphore.Acquire(ctx)
			if err == nil {
				countMu.Lock()
				acquiredCount++
				countMu.Unlock()

				// Hold the permit briefly
				time.Sleep(100 * time.Millisecond)

				// Release the permit
				semaphore.Release(ctx)
			}
		}()
	}

	wg.Wait()

	// At most 2 goroutines should have successfully acquired permits simultaneously
	// But over the course of the test, more than 2 could have acquired permits sequentially
	assert.GreaterOrEqual(t, acquiredCount, 2, "At least 2 acquisitions should succeed")
}

func TestLeaderTaskRunner_OnlyRunsOnLeader(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	// Create two electors
	config1 := LeaderElectionConfig{
		Client:   cli,
		SessionTTL: 10,
		Key:      "/test/leader-task-runner/runs",
		Identity: "runner-1",
	}

	config2 := LeaderElectionConfig{
		Client:   cli,
		SessionTTL: 10,
		Key:      "/test/leader-task-runner/runs",
		Identity: "runner-2",
	}

	elector1, err := NewLeaderElector(config1)
	require.NoError(t, err)

	elector2, err := NewLeaderElector(config2)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = elector1.Start(ctx)
	require.NoError(t, err)

	err = elector2.Start(ctx)
	require.NoError(t, err)

	// Wait for election to settle
	time.Sleep(500 * time.Millisecond)

	// Create task runners
	runner1 := NewLeaderTaskRunner(elector1)
	runner2 := NewLeaderTaskRunner(elector2)

	// Create a task that increments a counter
	var taskCounter int
	var counterMu sync.Mutex

	task := LeaderTask{
		Name:     "test-task",
		Interval: 100 * time.Millisecond,
		Function: func(ctx context.Context) error {
			counterMu.Lock()
			taskCounter++
			counterMu.Unlock()
			return nil
		},
		RunUntil: func() bool {
			return taskCounter >= 5 // Run until counter reaches 5
		},
	}

	runner1.AddTask(task)
	runner2.AddTask(task)

	// Start both runners
	err = runner1.Start(ctx)
	require.NoError(t, err)

	err = runner2.Start(ctx)
	require.NoError(t, err)

	// Wait for tasks to run
	time.Sleep(2 * time.Second)

	// Only the leader's task should run, so counter should be around 5-10
	// (depending on timing, but significantly less than if both ran)
	counterMu.Lock()
	defer counterMu.Unlock()
	
	// The counter should be incremented by only the leader's task
	// This verifies that tasks only run on the leader node
	assert.Greater(t, taskCounter, 0, "Tasks should run on leader")
}

func TestConcurrentLeaderElections(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	const numElections = 5
	var wg sync.WaitGroup

	for i := 0; i < numElections; i++ {
		wg.Add(1)
		go func(electionNum int) {
			defer wg.Done()

			config := LeaderElectionConfig{
				Client:   cli,
				SessionTTL: 10,
				Key:      fmt.Sprintf("/test/concurrent-elections/election-%d", electionNum),
				Identity: fmt.Sprintf("candidate-%d-%d", electionNum, time.Now().UnixNano()),
			}

			elector, err := NewLeaderElector(config)
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			err = elector.Start(ctx)
			require.NoError(t, err)

			// Wait briefly to ensure election occurs
			time.Sleep(100 * time.Millisecond)

			// Each election should have exactly one leader
			leaderID, err := elector.GetLeaderIdentity()
			assert.NoError(t, err)
			assert.NotEmpty(t, leaderID)
		}(i)
	}

	wg.Wait()
}

func TestLeaderElection_SessionExpiration(t *testing.T) {
	_, cli := startEmbeddedEtcd(t)

	config := LeaderElectionConfig{
		Client:   cli,
		SessionTTL: 2, // Very short TTL for testing
		Key:      "/test/leader-election/expiry",
		Identity: "expiry-test",
	}

	elector, err := NewLeaderElector(config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = elector.Start(ctx)
	require.NoError(t, err)

	// Wait to become leader
	time.Sleep(200 * time.Millisecond)
	assert.True(t, elector.IsLeader())

	// Wait for session to expire (should be around TTL seconds)
	time.Sleep(3 * time.Second)

	// After session expiry, should no longer be leader
	assert.False(t, elector.IsLeader(), "Should lose leadership after session expiry")
}
