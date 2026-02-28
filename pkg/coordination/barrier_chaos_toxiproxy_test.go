//go:build chaos
// +build chaos

package coordination

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/testutil"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// TestChaos_DistributedBarrier_ToxiproxyNetworkCut validates barrier timeout behavior
// when etcd connectivity is disrupted through toxiproxy.
//
// Run manually:
// CHAOS_TEST=true go test -tags=chaos -run TestChaos_DistributedBarrier_ToxiproxyNetworkCut ./pkg/coordination -v
func TestChaos_DistributedBarrier_ToxiproxyNetworkCut(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	ctx := context.Background()

	networkName := "dagens-chaos-barrier-net"
	networkReq := testcontainers.NetworkRequest{
		Name:       networkName,
		Driver:     "bridge",
		Attachable: true,
	}
	net, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: networkReq,
	})
	require.NoError(t, err)
	defer net.Remove(ctx)

	etcdC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "quay.io/coreos/etcd:v3.5.12",
			ExposedPorts: []string{"2379/tcp"},
			Networks:     []string{networkName},
			NetworkAliases: map[string][]string{
				networkName: {"etcd"},
			},
			Cmd: []string{
				"/usr/local/bin/etcd",
				"--name=etcd0",
				"--advertise-client-urls=http://etcd:2379",
				"--listen-client-urls=http://0.0.0.0:2379",
				"--listen-peer-urls=http://0.0.0.0:2380",
			},
			WaitingFor: wait.ForHTTP("/health").WithPort("2379/tcp"),
		},
		Started: true,
	})
	require.NoError(t, err)
	defer etcdC.Terminate(ctx)

	toxiproxyC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/shopify/toxiproxy:2.12.0",
			ExposedPorts: []string{"8474/tcp", "8666/tcp"},
			Networks:     []string{networkName},
			NetworkAliases: map[string][]string{
				networkName: {"toxiproxy"},
			},
			WaitingFor: wait.ForListeningPort("8474/tcp"),
		},
		Started: true,
	})
	require.NoError(t, err)
	defer toxiproxyC.Terminate(ctx)

	adminEndpoint, err := toxiproxyC.Endpoint(ctx, "8474/tcp")
	require.NoError(t, err)

	err = createToxiproxyProxy(adminEndpoint, "etcd", "0.0.0.0:8666", "etcd:2379")
	require.NoError(t, err)

	proxyEndpoint, err := toxiproxyC.Endpoint(ctx, "8666/tcp")
	require.NoError(t, err)

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{proxyEndpoint},
		DialTimeout: 2 * time.Second,
	})
	require.NoError(t, err)
	defer cli.Close()

	s1, err := concurrency.NewSession(cli, concurrency.WithTTL(5))
	require.NoError(t, err)
	defer s1.Close()
	s2, err := concurrency.NewSession(cli, concurrency.WithTTL(5))
	require.NoError(t, err)
	defer s2.Close()

	barrierKey := "/dagens/chaos/barrier/toxiproxy"
	b1 := NewDistributedBarrier(cli, s1, barrierKey, 2)
	b2 := NewDistributedBarrier(cli, s2, barrierKey, 2)

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer waitCancel()

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- b1.Wait(waitCtx)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		_ = addToxiproxyTimeout(adminEndpoint, "etcd", 5000)
		errCh <- b2.Wait(waitCtx)
	}()

	wg.Wait()
	close(errCh)

	observedErr := false
	for err := range errCh {
		if err != nil {
			observedErr = true
		}
	}
	require.True(t, observedErr, "expected at least one barrier waiter to fail under network cut")
}

// TestChaos_DistributedBarrier_AgentSessionCrashDuringWait simulates an agent crash
// while waiting on barrier, then validates remaining agents can still complete once
// enough live participants arrive.
func TestChaos_DistributedBarrier_AgentSessionCrashDuringWait(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	_, endpoints := testutil.StartEmbeddedEtcd(t)
	cli, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)
	defer cli.Close()

	s1, err := concurrency.NewSession(cli, concurrency.WithTTL(1))
	require.NoError(t, err)
	defer s1.Close()
	s2, err := concurrency.NewSession(cli, concurrency.WithTTL(1))
	require.NoError(t, err)
	defer s2.Close()
	s3, err := concurrency.NewSession(cli, concurrency.WithTTL(1))
	require.NoError(t, err)
	defer s3.Close()

	key := "/dagens/chaos/barrier/agent-crash"
	b1 := NewDistributedBarrier(cli, s1, key, 2)
	b2 := NewDistributedBarrier(cli, s2, key, 2)
	b3 := NewDistributedBarrier(cli, s3, key, 2)

	crashCtx, crashCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer crashCancel()
	go func() {
		_ = b1.Wait(crashCtx)
	}()
	time.Sleep(100 * time.Millisecond)
	_ = s1.Close() // simulate crashed agent

	finalCtx, finalCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finalCancel()

	errCh := make(chan error, 2)
	go func() { errCh <- b2.Wait(finalCtx) }()
	go func() { errCh <- b3.Wait(finalCtx) }()

	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)
}

// TestChaos_DistributedBarrier_EtcdRestartDuringGenerationBootstrap simulates etcd
// restart while barrier generation is being established.
func TestChaos_DistributedBarrier_EtcdRestartDuringGenerationBootstrap(t *testing.T) {
	if os.Getenv("CHAOS_TEST") != "true" {
		t.Skip("Skipping chaos test. Set CHAOS_TEST=true to enable.")
	}

	ctx := context.Background()
	etcdC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "quay.io/coreos/etcd:v3.5.12",
			ExposedPorts: []string{"2379/tcp"},
			Cmd: []string{
				"/usr/local/bin/etcd",
				"--name=etcd0",
				"--advertise-client-urls=http://127.0.0.1:2379",
				"--listen-client-urls=http://0.0.0.0:2379",
				"--listen-peer-urls=http://0.0.0.0:2380",
			},
			WaitingFor: wait.ForHTTP("/health").WithPort("2379/tcp"),
		},
		Started: true,
	})
	require.NoError(t, err)
	defer etcdC.Terminate(ctx)

	endpoint, err := etcdC.Endpoint(ctx, "2379/tcp")
	require.NoError(t, err)
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 2 * time.Second,
	})
	require.NoError(t, err)
	defer cli.Close()

	session, err := concurrency.NewSession(cli, concurrency.WithTTL(5))
	require.NoError(t, err)
	defer session.Close()

	barrier := NewDistributedBarrier(cli, session, "/dagens/chaos/barrier/restart-bootstrap", 1)

	// Restart etcd right before wait to force generation bootstrap disruption.
	require.NoError(t, etcdC.Stop(ctx, nil))
	require.NoError(t, etcdC.Start(ctx))
	waitForEtcdReady(t, cli, 5*time.Second)

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = barrier.Wait(waitCtx)
	require.NoError(t, err)
}

func waitForEtcdReady(t *testing.T, cli *clientv3.Client, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := cli.Get(pingCtx, "health-check-key")
		cancel()
		if err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("etcd did not become ready within %s", timeout)
}

func createToxiproxyProxy(adminEndpoint, name, listen, upstream string) error {
	body := map[string]string{
		"name":     name,
		"listen":   listen,
		"upstream": upstream,
	}
	return postJSON(fmt.Sprintf("http://%s/proxies", adminEndpoint), body)
}

func addToxiproxyTimeout(adminEndpoint, proxyName string, timeoutMs int) error {
	body := map[string]any{
		"name":       "timeout",
		"type":       "timeout",
		"stream":     "downstream",
		"toxicity":   1.0,
		"attributes": map[string]int{"timeout": timeoutMs},
	}
	return postJSON(fmt.Sprintf("http://%s/proxies/%s/toxics", adminEndpoint, proxyName), body)
}

func postJSON(url string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("request to %s failed with status %d", url, resp.StatusCode)
	}
	return nil
}
