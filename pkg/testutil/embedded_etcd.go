package testutil

import (
	"net/url"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/embed"
)

// StartEmbeddedEtcd starts an embedded etcd server for integration tests.
// It returns the embedded server instance and the client endpoints.
// The server is automatically closed when the test finishes.
func StartEmbeddedEtcd(t *testing.T) (*embed.Etcd, []string) {
	t.Helper()

	cfg := embed.NewConfig()
	cfg.Dir = t.TempDir()
	cfg.LogLevel = "error" // Suppress noisy logs

	// Use random ports to avoid conflicts
	lpUrl, _ := url.Parse("http://localhost:0")
	lcUrl, _ := url.Parse("http://localhost:0")
	
	// Try alternative field names
	cfg.ListenPeerUrls = []url.URL{*lpUrl}
	cfg.ListenClientUrls = []url.URL{*lcUrl}

	e, err := embed.StartEtcd(cfg)
	if err != nil {
		t.Fatalf("failed to start embedded etcd: %v", err)
	}

	select {
	case <-e.Server.ReadyNotify():
	case <-time.After(60 * time.Second):
		e.Close()
		t.Fatal("embedded etcd took too long to start")
	}

	t.Cleanup(func() {
		e.Close()
	})

	endpoints := []string{e.Clients[0].Addr().String()}
	return e, endpoints
}