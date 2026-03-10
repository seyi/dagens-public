package scheduler

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

var scopeSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+:[A-Za-z0-9._:/-]+$`)
var ErrInvalidScope = errors.New("invalid leadership scope")

// LeadershipAuthority describes whether the current scheduler instance may
// dispatch work, and the authority context used for fencing/auditing.
type LeadershipAuthority struct {
	// IsLeader is true when this instance is allowed to dispatch.
	IsLeader bool

	// Epoch is a monotonically increasing fencing token when available.
	Epoch string

	// LeaderID identifies the current elected leader when available.
	LeaderID string

	// Scope defines the authority boundary for this leadership decision.
	//
	// Scope format conventions:
	//   - Single dimension: "tenant:123", "region:us-west", "queue:payments"
	//   - Multi dimension: "tenant:123;region:eu;queue:priority"
	//   - Hash shards: "shard:7"
	//
	// Empty scope means global leadership (v0.2 default behavior).
	Scope string
}

// LeadershipProvider supplies dispatch authority for HA control planes.
//
// Implementations may use etcd leases, database leader locks, or static
// single-node authority for local/dev operation.
type LeadershipProvider interface {
	DispatchAuthority(ctx context.Context) (LeadershipAuthority, error)
}

// ScopedLeadershipProvider is an optional extension for providers that can
// return authority decisions scoped to a specific partition/tenant/region.
type ScopedLeadershipProvider interface {
	LeadershipProvider
	DispatchAuthorityForScope(ctx context.Context, scope string) (LeadershipAuthority, error)
}

// LeadershipProviderLifecycle is an optional extension for providers that need
// background startup/shutdown (for example etcd lease campaign loops).
type LeadershipProviderLifecycle interface {
	LeadershipProvider
	// Start begins background leadership maintenance and should return once the
	// provider is ready to serve DispatchAuthority calls. Implementations should
	// treat repeated Start calls as safe (idempotent no-op or explicit error).
	// The supplied ctx controls lifecycle goroutines and should trigger graceful
	// shutdown when canceled.
	Start(ctx context.Context) error
	// Stop terminates background leadership maintenance and should be safe to
	// call multiple times, including calls before Start.
	Stop()
}

// StartLeadershipProviderIfLifecycle starts provider lifecycle management when
// supported and returns a cleanup callback. For non-lifecycle providers this
// returns a no-op cleanup callback.
func StartLeadershipProviderIfLifecycle(ctx context.Context, provider LeadershipProvider) (cleanup func(), err error) {
	if lifecycle, ok := provider.(LeadershipProviderLifecycle); ok {
		if err := lifecycle.Start(ctx); err != nil {
			return nil, err
		}
		return lifecycle.Stop, nil
	}
	return func() {}, nil
}

// DispatchAuthorityForScope resolves authority for a scope when supported,
// falling back to global DispatchAuthority for providers that do not implement
// ScopedLeadershipProvider.
//
// The returned LeadershipAuthority is always annotated with the requested scope
// when no error occurs.
func DispatchAuthorityForScope(ctx context.Context, provider LeadershipProvider, scope string) (LeadershipAuthority, error) {
	if !IsValidScope(scope) {
		return LeadershipAuthority{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	if scoped, ok := provider.(ScopedLeadershipProvider); ok {
		auth, err := scoped.DispatchAuthorityForScope(ctx, scope)
		if err != nil {
			return LeadershipAuthority{}, err
		}
		if auth.Scope == "" {
			auth.Scope = scope
		}
		return auth, nil
	}

	auth, err := provider.DispatchAuthority(ctx)
	if err != nil {
		return LeadershipAuthority{}, err
	}
	auth.Scope = scope
	return auth, nil
}

// IsValidScope returns true when scope follows leadership naming conventions.
//
// Allowed forms:
//   - empty string: global scope
//   - single segment: key:value
//   - multi-segment: key1:value1;key2:value2
//
// Allowed key chars: [A-Za-z0-9_-]
// Allowed value chars: [A-Za-z0-9._:/-]
//
// Whitespace and empty segments are rejected.
// Empty scope is valid and denotes global leadership.
func IsValidScope(scope string) bool {
	if scope == "" {
		return true
	}

	start := 0
	for i := 0; i <= len(scope); i++ {
		if i < len(scope) && scope[i] != ';' {
			continue
		}
		segment := scope[start:i]
		if segment == "" || !scopeSegmentPattern.MatchString(segment) {
			return false
		}
		start = i + 1
	}
	return true
}

type staticLeadershipProvider struct {
	authority LeadershipAuthority
}

func (p staticLeadershipProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return p.authority, nil
}

func defaultLeadershipProvider() LeadershipProvider {
	return staticLeadershipProvider{
		authority: LeadershipAuthority{
			IsLeader: true,
			Epoch:    "single-node",
			LeaderID: "local",
		},
	}
}
