package registry

import (
	"context"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestDistributedAgentRegistry_Registration(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	config := RegistryConfig{
		EtcdEndpoints:    endpoints,
		NodeID:           "test-node-1",
		NodeName:         "Test Node 1",
		NodeAddress:      "localhost",
		NodePort:         8080,
		LeaseTTL:         2, // Short TTL for faster testing
		HeartbeatInterval: 1 * time.Second,
	}

	reg, err := NewDistributedAgentRegistry(config)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = reg.Start(ctx)
	require.NoError(t, err)
	defer reg.Stop()

	// Verify node is registered
	nodes := reg.GetNodes()
	assert.Len(t, nodes, 1)
	assert.Equal(t, "test-node-1", nodes[0].ID)

	// Verify in etcd directly
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	resp, err := cli.Get(ctx, "/dagens/nodes/test-node-1")
	require.NoError(t, err)
	assert.Len(t, resp.Kvs, 1)
}

func TestDistributedAgentRegistry_Reconnection(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	config := RegistryConfig{
		EtcdEndpoints:    endpoints,
		NodeID:           "test-node-recon",
		LeaseTTL:         2,
		HeartbeatInterval: 500 * time.Millisecond,
	}

	reg, err := NewDistributedAgentRegistry(config)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = reg.Start(ctx)
	require.NoError(t, err)
	defer reg.Stop()

	// Wait for initial registration
	time.Sleep(1 * time.Second)

	// SIMULATE SESSION LOSS: Manually revoke the lease
	reg.mu.RLock()
	leaseID := reg.leaseID
	reg.mu.RUnlock()

	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	_, err = cli.Revoke(ctx, leaseID)
	require.NoError(t, err)

	// Wait for reconnection loop to kick in (should happen quickly due to session.Done())
	// The session wrapper usually notices revocation via keepalive failure.
	// We give it a few seconds to detect and recover.
	time.Sleep(3 * time.Second)

	// Verify we have a NEW lease
	reg.mu.RLock()
	newLeaseID := reg.leaseID
	reg.mu.RUnlock()

	assert.NotEqual(t, leaseID, newLeaseID, "Lease ID should have changed after reconnection")
	assert.NotZero(t, newLeaseID, "New lease ID should not be zero")

	// Verify node is STILL registered in etcd (or re-registered)
	resp, err := cli.Get(ctx, "/dagens/nodes/test-node-recon")
	require.NoError(t, err)
	assert.Len(t, resp.Kvs, 1, "Node should be re-registered")
}
