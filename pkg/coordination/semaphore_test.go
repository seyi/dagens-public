package coordination

import (
	"context"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

func TestDistributedSemaphore_AcquireRelease(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)
	// Note: StartEmbeddedEtcd registers t.Cleanup(func() { e.Close() })

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	session, err := concurrency.NewSession(cli)
	require.NoError(t, err)
	defer session.Close()

	sem := NewDistributedSemaphore(cli, session, "/dagens/test-sem", 2)

	// Acquire 1
	err = sem.Acquire(context.Background())
	require.NoError(t, err)

	// Acquire 2
	err = sem.Acquire(context.Background())
	require.NoError(t, err)

	// Verify availability
	avail, err := sem.GetAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, avail)

	// Release 1
	err = sem.Release(context.Background())
	require.NoError(t, err)

	avail, err = sem.GetAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, avail)
}

func TestDistributedSemaphore_Fairness(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)
	// Note: StartEmbeddedEtcd registers t.Cleanup(func() { e.Close() })

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	// Create 3 sessions for 3 participants
	s1, _ := concurrency.NewSession(cli)
	defer s1.Close()
	s2, _ := concurrency.NewSession(cli)
	defer s2.Close()
	s3, _ := concurrency.NewSession(cli)
	defer s3.Close()

	// Semaphore size 1 (Mutex behavior for strict ordering test)
	sem1 := NewDistributedSemaphore(cli, s1, "/dagens/fair-sem", 1)
	sem2 := NewDistributedSemaphore(cli, s2, "/dagens/fair-sem", 1)
	sem3 := NewDistributedSemaphore(cli, s3, "/dagens/fair-sem", 1)

	// 1. Participant 1 acquires immediately
	err = sem1.Acquire(context.Background())
	require.NoError(t, err)

	// 2. Participant 2 tries to acquire (blocks)
	done2 := make(chan struct{})
	go func() {
		sem2.Acquire(context.Background())
		close(done2)
	}()

	// Ensure P2 has registered its intent (is in queue)
	time.Sleep(500 * time.Millisecond)

	// 3. Participant 3 tries to acquire (blocks)
	done3 := make(chan struct{})
	go func() {
		sem3.Acquire(context.Background())
		close(done3)
	}()

	// Ensure P3 is in queue
	time.Sleep(500 * time.Millisecond)

	// Verify queue order by inspecting holders
	holders, err := sem1.GetHolders(context.Background())
	require.NoError(t, err)
	require.Len(t, holders, 3)
	// P1 should be first (holding), then P2, then P3 based on our timing

	// Release P1 -> P2 should get it
	sem1.Release(context.Background())
	select {
	case <-done2:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Participant 2 did not acquire semaphore in time")
	}

	// Release P2 -> P3 should get it
	sem2.Release(context.Background())
	select {
	case <-done3:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Participant 3 did not acquire semaphore in time")
	}
}

func TestDistributedSemaphore_CrashSafety(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)
	// Note: StartEmbeddedEtcd registers t.Cleanup(func() { e.Close() })

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	// Session with short TTL to simulate crash quickly
	s1, err := concurrency.NewSession(cli, concurrency.WithTTL(1))
	require.NoError(t, err)
	
	sem1 := NewDistributedSemaphore(cli, s1, "/dagens/crash-sem", 1)

	// Acquire
	err = sem1.Acquire(context.Background())
	require.NoError(t, err)

	// Verify held
	avail, _ := sem1.GetAvailable(context.Background())
	assert.Equal(t, 0, avail)

	// SIMULATE CRASH: Close session (revokes lease)
	s1.Close()
	
	// Wait for TTL expiration/cleanup
	time.Sleep(2 * time.Second)

	// Verify released automatically
	// We need a new client/session to check since s1 is closed
	s2, _ := concurrency.NewSession(cli)
	defer s2.Close()
	sem2 := NewDistributedSemaphore(cli, s2, "/dagens/crash-sem", 1)

	avail, err = sem2.GetAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, avail, "Semaphore should be available after session crash")
}
