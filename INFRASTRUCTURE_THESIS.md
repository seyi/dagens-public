# Infrastructure Thesis: Would Dagens Still Matter Without LLMs?

This document captures a critical architectural test:

> If Dagens removed LLMs entirely and only orchestrated deterministic services plus human checkpoints, would it still be valuable?

## Conclusion

Yes, with an important qualifier.

Dagens would still provide real value because a significant portion of the codebase is execution infrastructure rather than model-specific logic. The parts that survive LLM removal are enough to justify Dagens as an orchestration runtime, not just an AI toy.

The qualifier is that the infrastructure is meaningful today, but still uneven in maturity across layers.

## What Still Has Value Without LLMs

### 1. Graph and Workflow Runtime

The graph layer is not inherently LLM-dependent. It defines:

- graph topology
- node interfaces
- function nodes
- traversal
- state passing
- compile-to-job conversion

Code references:

- [pkg/graph/graph.go](/data/repos/dagens/pkg/graph/graph.go)
- [pkg/graph/node.go](/data/repos/dagens/pkg/graph/node.go)
- [pkg/graph/compiler.go](/data/repos/dagens/pkg/graph/compiler.go)

This means Dagens can orchestrate deterministic services and functions, not only model calls.

### 2. Scheduler and Dispatch Fabric

The scheduler is independent of LLM logic. It:

- accepts jobs
- maintains an in-process job queue
- selects healthy nodes
- dispatches to workers using `ExecuteOnNode`

Code references:

- [pkg/scheduler/scheduler.go](/data/repos/dagens/pkg/scheduler/scheduler.go)
- [pkg/remote/remote_execution.go](/data/repos/dagens/pkg/remote/remote_execution.go)
- [pkg/remote/grpc_remote_execution.go](/data/repos/dagens/pkg/remote/grpc_remote_execution.go)

This is core control-plane infrastructure.

### 3. Registry and Cluster Coordination

Cluster membership and health routing are not tied to LLMs.

- static registry mode works today
- etcd-backed registry enables dynamic node registration, health filtering, and watch/reconnect behavior

Code references:

- [cmd/api_server/main.go](/data/repos/dagens/cmd/api_server/main.go)
- [pkg/registry/distributed_registry.go](/data/repos/dagens/pkg/registry/distributed_registry.go)

This is infrastructure whether the executed workload is AI or deterministic services.

### 4. HITL Checkpoint and Resume

HITL is one of the strongest non-LLM primitives in the repository.

It supports:

- checkpoint creation
- callback verification
- idempotent processing
- async resumption workers
- graph version validation before resume

Code references:

- [pkg/hitl/human_node.go](/data/repos/dagens/pkg/hitl/human_node.go)
- [pkg/hitl/callback_handler.go](/data/repos/dagens/pkg/hitl/callback_handler.go)
- [pkg/hitl/resumption_worker.go](/data/repos/dagens/pkg/hitl/resumption_worker.go)
- [pkg/hitl/system.go](/data/repos/dagens/pkg/hitl/system.go)

This remains valuable even if every workload is deterministic.

### 5. Events, Observability, and Resilience

These systems are generic runtime concerns, not AI-specific concerns:

- event bus
- event store/recorder
- telemetry
- retry/circuit-breaker wrappers
- execution monitoring

Code references:

- [pkg/events/events.go](/data/repos/dagens/pkg/events/events.go)
- [pkg/agentic/monitoring.go](/data/repos/dagens/pkg/agentic/monitoring.go)
- [pkg/agentic/healing.go](/data/repos/dagens/pkg/agentic/healing.go)

That is classic infrastructure behavior.

## What Becomes Weaker Without LLMs

Removing LLMs would expose where Dagens is still less mature than a fully hardened distributed runtime.

### 1. Scheduler Maturity Is Still Basic

Today the scheduling story is real, but simple:

- health-filtered node selection
- round-robin base selection
- sticky affinity support

It is not yet a sophisticated production scheduler with deep load-aware/backpressure-aware decisioning.

Primary code reference:

- [pkg/scheduler/scheduler.go](/data/repos/dagens/pkg/scheduler/scheduler.go)

### 2. Static Mode Is Prominent

The API server can run indefinitely in static registry mode, but the default fallback is still a `MockRegistry` with fixed workers.

Code reference:

- [cmd/api_server/main.go](/data/repos/dagens/cmd/api_server/main.go)

That is useful, but it means some “cluster” behavior is operationally manual unless etcd is enabled.

### 3. Some API Paths Use Placeholder Execution Nodes

The API submission path currently turns `"function"` and `"agent"` request nodes into placeholder `FunctionNode`s that no-op.

Code reference:

- [pkg/api/server.go](/data/repos/dagens/pkg/api/server.go)

This means part of the external control plane is more of an orchestration shell than a fully expressive execution model.

### 4. Repo Surface Is Still LLM-Heavy

There is substantial LLM-specific code across:

- `pkg/models`
- LLM agents
- LLM runtime flows
- policy rules using LLM evaluation

That does not negate the infrastructure value, but it means the non-LLM thesis exists more strongly in architecture than in current messaging and package balance.

## Final Positioning

The correct statement is:

> If LLMs disappeared, Dagens would still be a useful orchestration and control-plane runtime.

That means:

- it is not just an AI toy
- it is not only a wrapper around model APIs
- it has real infrastructure primitives

The more precise framing is:

> Dagens is an emerging orchestration/control-plane runtime with AI-specific adapters layered on top.

That is the strongest defensible claim today.
