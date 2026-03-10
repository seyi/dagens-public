package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartLeadershipProviderIfLifecycle_StaticProviderReturnsNoOpCleanup(t *testing.T) {
	cleanup, err := StartLeadershipProviderIfLifecycle(context.Background(), defaultLeadershipProvider())
	if err != nil {
		t.Fatalf("StartLeadershipProviderIfLifecycle unexpected error: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup callback")
	}
	// Should be safe no-op for non-lifecycle providers.
	cleanup()
}

func TestStartLeadershipProviderIfLifecycle_LifecycleProviderStartsAndStops(t *testing.T) {
	lp := &testLifecycleProvider{}
	cleanup, err := StartLeadershipProviderIfLifecycle(context.Background(), lp)
	if err != nil {
		t.Fatalf("StartLeadershipProviderIfLifecycle unexpected error: %v", err)
	}
	if !lp.started {
		t.Fatal("expected lifecycle provider Start to be called")
	}
	cleanup()
	if !lp.stopped {
		t.Fatal("expected lifecycle provider Stop to be called from cleanup")
	}
}

func TestStartLeadershipProviderIfLifecycle_PropagatesStartError(t *testing.T) {
	lp := &testLifecycleProvider{startErr: errors.New("boom")}
	cleanup, err := StartLeadershipProviderIfLifecycle(context.Background(), lp)
	if err == nil {
		t.Fatal("expected start error")
	}
	if cleanup != nil {
		t.Fatal("expected nil cleanup on start error")
	}
}

func TestDispatchAuthorityForScope_FallbackProviderAnnotatesScope(t *testing.T) {
	auth, err := DispatchAuthorityForScope(context.Background(), defaultLeadershipProvider(), "tenant:123")
	if err != nil {
		t.Fatalf("DispatchAuthorityForScope unexpected error: %v", err)
	}
	if auth.Scope != "tenant:123" {
		t.Fatalf("authority scope = %q, want %q", auth.Scope, "tenant:123")
	}
	if !auth.IsLeader {
		t.Fatal("expected default provider authority to remain leader")
	}
}

func TestDispatchAuthorityForScope_ScopedProviderUsesScopedResult(t *testing.T) {
	provider := &testScopedProvider{}
	auth, err := DispatchAuthorityForScope(context.Background(), provider, "region:eu")
	if err != nil {
		t.Fatalf("DispatchAuthorityForScope unexpected error: %v", err)
	}
	if auth.Scope != "region:eu" {
		t.Fatalf("authority scope = %q, want %q", auth.Scope, "region:eu")
	}
	if provider.lastScope != "region:eu" {
		t.Fatalf("scoped provider lastScope = %q, want %q", provider.lastScope, "region:eu")
	}
}

func TestDispatchAuthorityForScope_ProviderScopeTakesPrecedence(t *testing.T) {
	provider := &overrideScopedProvider{}
	auth, err := DispatchAuthorityForScope(context.Background(), provider, "tenant:123")
	if err != nil {
		t.Fatalf("DispatchAuthorityForScope unexpected error: %v", err)
	}
	if auth.Scope != "tenant:456" {
		t.Fatalf("authority scope = %q, want %q", auth.Scope, "tenant:456")
	}
}

func TestDispatchAuthorityForScope_EmptyScopeIsGlobal(t *testing.T) {
	auth, err := DispatchAuthorityForScope(context.Background(), defaultLeadershipProvider(), "")
	if err != nil {
		t.Fatalf("DispatchAuthorityForScope unexpected error: %v", err)
	}
	if auth.Scope != "" {
		t.Fatalf("authority scope = %q, want empty global scope", auth.Scope)
	}
	if !auth.IsLeader {
		t.Fatal("expected default provider to remain leader")
	}
}

func TestDispatchAuthorityForScope_ConcurrentCalls(t *testing.T) {
	provider := &threadSafeScopedProvider{}
	scopes := []string{"tenant:1", "tenant:2", "region:us", "region:eu"}
	var wg sync.WaitGroup
	errCh := make(chan error, 200)

	for i := 0; i < 200; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			scope := scopes[i%len(scopes)]
			auth, err := DispatchAuthorityForScope(context.Background(), provider, scope)
			if err != nil {
				errCh <- err
				return
			}
			if auth.Scope != scope {
				errCh <- errors.New("scope mismatch")
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent authority resolution failed: %v", err)
	}
}

func TestDispatchAuthorityForScope_InvalidScopeRejected_FallbackProvider(t *testing.T) {
	_, err := DispatchAuthorityForScope(context.Background(), defaultLeadershipProvider(), "tenant: 123")
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("DispatchAuthorityForScope error = %v, want %v", err, ErrInvalidScope)
	}
}

func TestDispatchAuthorityForScope_InvalidScopeRejected_ScopedProvider(t *testing.T) {
	provider := &testScopedProvider{}
	_, err := DispatchAuthorityForScope(context.Background(), provider, "tenant$:123")
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("DispatchAuthorityForScope error = %v, want %v", err, ErrInvalidScope)
	}
}

func TestIsValidScope(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		want  bool
	}{
		{name: "empty global scope", scope: "", want: true},
		{name: "single tenant", scope: "tenant:123", want: true},
		{name: "single region", scope: "region:us-west", want: true},
		{name: "single queue", scope: "queue:payments", want: true},
		{name: "uppercase key allowed", scope: "Tenant:123", want: true},
		{name: "hash shard", scope: "shard:7", want: true},
		{name: "multi segment", scope: "tenant:123;region:eu;queue:priority", want: true},
		{name: "value with slash and dot", scope: "model:llama-3.1/70b", want: true},
		{name: "missing colon", scope: "tenant123", want: false},
		{name: "empty segment", scope: "tenant:123;;region:eu", want: false},
		{name: "empty key", scope: ":tenant", want: false},
		{name: "empty value", scope: "tenant:", want: false},
		{name: "invalid char in key", scope: "tenant$:123", want: false},
		{name: "whitespace", scope: "tenant: 123", want: false},
		{name: "trailing semicolon", scope: "tenant:123;", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := IsValidScope(tc.scope)
			if got != tc.want {
				t.Fatalf("IsValidScope(%q) = %v, want %v", tc.scope, got, tc.want)
			}
		})
	}
}

type testLifecycleProvider struct {
	started  bool
	stopped  bool
	startErr error
}

func (p *testLifecycleProvider) Start(context.Context) error {
	if p.startErr != nil {
		return p.startErr
	}
	p.started = true
	return nil
}

func (p *testLifecycleProvider) Stop() {
	p.stopped = true
}

func (p *testLifecycleProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return LeadershipAuthority{IsLeader: true, Epoch: "test", LeaderID: "test"}, nil
}

type testScopedProvider struct {
	lastScope string
}

func (p *testScopedProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return LeadershipAuthority{IsLeader: false, Epoch: "global", LeaderID: "global"}, nil
}

func (p *testScopedProvider) DispatchAuthorityForScope(_ context.Context, scope string) (LeadershipAuthority, error) {
	p.lastScope = scope
	return LeadershipAuthority{IsLeader: true, Epoch: "scoped", LeaderID: "scoped", Scope: scope}, nil
}

func TestStartLeadershipProviderIfLifecycle_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	lp := &cancelAwareLifecycleProvider{}
	cleanup, err := StartLeadershipProviderIfLifecycle(ctx, lp)
	if err != nil {
		t.Fatalf("StartLeadershipProviderIfLifecycle unexpected error: %v", err)
	}
	if !lp.started.Load() {
		t.Fatal("expected lifecycle provider to start")
	}

	cancel()
	select {
	case <-lp.cancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("expected provider goroutine to observe context cancellation")
	}
	cleanup()
}

func TestLifecycleProvider_IdempotentStartStop(t *testing.T) {
	lp := &idempotentLifecycleProvider{}
	cleanup1, err := StartLeadershipProviderIfLifecycle(context.Background(), lp)
	if err != nil {
		t.Fatalf("first StartLeadershipProviderIfLifecycle error: %v", err)
	}
	cleanup2, err := StartLeadershipProviderIfLifecycle(context.Background(), lp)
	if err != nil {
		t.Fatalf("second StartLeadershipProviderIfLifecycle error: %v", err)
	}

	// Both cleanup callbacks should be safe.
	cleanup1()
	cleanup2()

	if lp.startCalls.Load() < 2 {
		t.Fatalf("start calls = %d, want at least 2", lp.startCalls.Load())
	}
	if lp.stopCalls.Load() < 2 {
		t.Fatalf("stop calls = %d, want at least 2", lp.stopCalls.Load())
	}
}

type overrideScopedProvider struct{}

func (p *overrideScopedProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return LeadershipAuthority{IsLeader: true, Epoch: "global", LeaderID: "global"}, nil
}

func (p *overrideScopedProvider) DispatchAuthorityForScope(context.Context, string) (LeadershipAuthority, error) {
	return LeadershipAuthority{IsLeader: true, Epoch: "scoped", LeaderID: "scoped", Scope: "tenant:456"}, nil
}

type threadSafeScopedProvider struct {
	callCount atomic.Int64
}

func (p *threadSafeScopedProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return LeadershipAuthority{IsLeader: true, Epoch: "global", LeaderID: "global"}, nil
}

func (p *threadSafeScopedProvider) DispatchAuthorityForScope(_ context.Context, scope string) (LeadershipAuthority, error) {
	p.callCount.Add(1)
	return LeadershipAuthority{IsLeader: true, Epoch: "scoped", LeaderID: "scoped", Scope: scope}, nil
}

type cancelAwareLifecycleProvider struct {
	started        atomic.Bool
	cancelObserved chan struct{}
	stopOnce       sync.Once
}

func (p *cancelAwareLifecycleProvider) Start(ctx context.Context) error {
	p.started.Store(true)
	p.cancelObserved = make(chan struct{})
	go func() {
		<-ctx.Done()
		p.stopOnce.Do(func() {
			close(p.cancelObserved)
		})
	}()
	return nil
}

func (p *cancelAwareLifecycleProvider) Stop() {
	p.stopOnce.Do(func() {
		if p.cancelObserved != nil {
			close(p.cancelObserved)
		}
	})
}

func (p *cancelAwareLifecycleProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return LeadershipAuthority{IsLeader: true, Epoch: "test", LeaderID: "test"}, nil
}

type idempotentLifecycleProvider struct {
	startCalls atomic.Int64
	stopCalls  atomic.Int64
}

func (p *idempotentLifecycleProvider) Start(context.Context) error {
	p.startCalls.Add(1)
	return nil
}

func (p *idempotentLifecycleProvider) Stop() {
	p.stopCalls.Add(1)
}

func (p *idempotentLifecycleProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return LeadershipAuthority{IsLeader: true, Epoch: "idempotent", LeaderID: "idempotent"}, nil
}
