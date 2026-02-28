package coordination

import (
	"context"
	"fmt"
)

// WaitForAgents runs agentFunc and then waits on the given barrier.
// Callers must pass a context with deadline to avoid unbounded waits.
func WaitForAgents(ctx context.Context, barrier *DistributedBarrier, agentFunc func(context.Context) error) error {
	if barrier == nil {
		return fmt.Errorf("barrier is required")
	}
	if agentFunc == nil {
		return fmt.Errorf("agentFunc is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("context deadline is required for WaitForAgents")
	}

	if err := agentFunc(ctx); err != nil {
		return err
	}
	return barrier.Wait(ctx)
}

// WaitForSwarm is a higher-level alias for swarm-style agent rendezvous.
// It preserves the same deadline and error semantics as WaitForAgents.
func WaitForSwarm(ctx context.Context, barrier *DistributedBarrier, agentFunc func(context.Context) error) error {
	return WaitForAgents(ctx, barrier, agentFunc)
}
