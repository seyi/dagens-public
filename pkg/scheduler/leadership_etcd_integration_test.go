//go:build integration

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/testutil"
)

func TestEtcdLeadershipProvider_DispatchAuthorityForScope(t *testing.T) {
	_, endpoints := testutil.StartEmbeddedEtcd(t)

	provider, err := NewEtcdLeadershipProvider(EtcdLeadershipProviderConfig{
		Endpoints:   endpoints,
		ElectionKey: "/dagens/test/leadership",
		Identity:    "node-1",
		SessionTTL:  5,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewEtcdLeadershipProvider unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("provider.Start unexpected error: %v", err)
	}
	defer provider.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		auth, err := provider.DispatchAuthority(context.Background())
		if err == nil && auth.IsLeader && auth.LeaderID == "node-1" && auth.Epoch != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	auth, err := provider.DispatchAuthority(context.Background())
	if err != nil {
		t.Fatalf("DispatchAuthority final error: %v", err)
	}
	if !auth.IsLeader {
		t.Fatalf("expected leader authority, got %+v", auth)
	}
	if auth.LeaderID != "node-1" {
		t.Fatalf("expected leader_id=node-1, got %+v", auth)
	}
	if auth.Epoch == "" {
		t.Fatalf("expected non-empty epoch, got %+v", auth)
	}

	for time.Now().Before(deadline) {
		scoped, err := provider.DispatchAuthorityForScope(context.Background(), "tenant:123")
		if err == nil && scoped.IsLeader && scoped.Scope == "tenant:123" && scoped.LeaderID == "node-1" && scoped.Epoch != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	auth, err = provider.DispatchAuthorityForScope(context.Background(), "tenant:123")
	if err != nil {
		t.Fatalf("DispatchAuthorityForScope final error: %v", err)
	}
	t.Fatalf("expected leader authority with scope annotation, got %+v", auth)
}
