package coordination

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

func TestDistributedBarrier_MultiSessionSynchronization(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	s1, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer s1.Close()

	s2, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer s2.Close()

	s3, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer s3.Close()

	baseKey := "/dagens/test-barrier/multi-session"
	b1 := NewDistributedBarrier(cli, s1, baseKey, 3)
	b2 := NewDistributedBarrier(cli, s2, baseKey, 3)
	b3 := NewDistributedBarrier(cli, s3, baseKey, 3)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for _, b := range []*DistributedBarrier{b1, b2, b3} {
		wg.Add(1)
		go func(barrier *DistributedBarrier) {
			defer wg.Done()
			errs <- barrier.Wait(ctx)
		}(b)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}

func TestDistributedBarrier_ReleaseKeyHasNoLease(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	s1, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer s1.Close()

	s2, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer s2.Close()

	baseKey := "/dagens/test-barrier/release-no-lease"
	b1 := NewDistributedBarrier(cli, s1, baseKey, 2)
	b2 := NewDistributedBarrier(cli, s2, baseKey, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 2)
	go func() { errCh <- b1.Wait(ctx) }()
	go func() { errCh <- b2.Wait(ctx) }()

	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)

	genResp, err := cli.Get(ctx, baseKey+"/generation")
	require.NoError(t, err)
	require.Len(t, genResp.Kvs, 1)

	generation := string(genResp.Kvs[0].Value)
	releaseKey := fmt.Sprintf("%s/released/%s", baseKey, generation)
	releaseResp, err := cli.Get(ctx, releaseKey)
	require.NoError(t, err)
	require.Len(t, releaseResp.Kvs, 1)
	assert.Equal(t, int64(0), releaseResp.Kvs[0].Lease, "release key must not be lease-bound")
}

func TestDistributedBarrier_ResetStartsNewGeneration(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	baseKey := "/dagens/test-barrier/reset-generation"
	barrier := NewDistributedBarrier(cli, session, baseKey, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, barrier.Wait(ctx))

	gen1Resp, err := cli.Get(ctx, baseKey+"/generation")
	require.NoError(t, err)
	require.Len(t, gen1Resp.Kvs, 1)
	gen1 := string(gen1Resp.Kvs[0].Value)

	require.NoError(t, barrier.Reset(ctx))

	afterReset, err := cli.Get(ctx, baseKey, clientv3.WithPrefix())
	require.NoError(t, err)
	require.Len(t, afterReset.Kvs, 0, "reset should clear barrier state")

	time.Sleep(1 * time.Millisecond)

	require.NoError(t, barrier.Wait(ctx))

	gen2Resp, err := cli.Get(ctx, baseKey+"/generation")
	require.NoError(t, err)
	require.Len(t, gen2Resp.Kvs, 1)
	gen2 := string(gen2Resp.Kvs[0].Value)

	assert.NotEqual(t, gen1, gen2, "reset should produce a new barrier generation")
}

func TestWaitForAgents_RequiresDeadline(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	barrier := NewDistributedBarrier(cli, session, "/dagens/test-waitforagents/deadline", 1)

	err = WaitForAgents(context.Background(), barrier, func(context.Context) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deadline")
}

func TestWaitForAgents_Success(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	barrier := NewDistributedBarrier(cli, session, "/dagens/test-waitforagents/success", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = WaitForAgents(ctx, barrier, func(context.Context) error { return nil })
	require.NoError(t, err)
}

func TestWaitForAgents_PropagatesAgentError(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	barrier := NewDistributedBarrier(cli, session, "/dagens/test-waitforagents/agenterr", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	agentErr := fmt.Errorf("agent failed")
	err = WaitForAgents(ctx, barrier, func(context.Context) error { return agentErr })
	require.Error(t, err)
	assert.Equal(t, agentErr, err)
}

func TestWaitForSwarm_Success(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(10))
	require.NoError(t, err)
	defer session.Close()

	barrier := NewDistributedBarrier(cli, session, "/dagens/test-waitforswarm/success", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = WaitForSwarm(ctx, barrier, func(context.Context) error { return nil })
	require.NoError(t, err)
}
