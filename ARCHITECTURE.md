# Dagens Architecture

Dagens is a Go-based control-plane runtime for orchestrating distributed agent and service execution.

It separates control-plane concerns from execution-plane concerns:

- The API server accepts requests, compiles workflow graphs, and owns scheduling decisions.
- The scheduler dispatches work push-style to workers over gRPC.
- Workers act as execution endpoints.
- Registry, events, observability, and HITL provide coordination, visibility, and resumability.

## Core Model

### Control Plane

The control plane is responsible for:

- request intake
- graph compilation
- job submission
- queueing
- scheduling
- node selection
- dispatch
- lifecycle event emission

In the current implementation, the control plane lives primarily in:

- `cmd/api_server`
- `pkg/api`
- `pkg/graph`
- `pkg/scheduler`
- `pkg/registry`
- `pkg/events`
- `pkg/hitl`

### Execution Plane

The execution plane consists of worker processes that expose gRPC execution endpoints.

Workers:

- receive execution requests from the scheduler
- run assigned tasks
- return results to the caller
- participate in static or etcd-backed registration

In the current implementation, the execution plane lives primarily in:

- `cmd/worker`
- `pkg/remote`
- `pkg/agent`
- `pkg/agentic`

## Dispatch Model

Dagens uses a central push scheduler.

1. A request is submitted to the API server.
2. The graph is compiled into executable work.
3. The scheduler enqueues the job in-process.
4. The scheduler selects a node from the registry.
5. Work is dispatched to a worker via gRPC.
6. The worker executes and returns a result.

Workers do not poll for jobs.

## Scale Modes

### Fixed Topology

Without etcd, Dagens can run in static registry mode.

This mode is suitable for:

- development
- staging
- fixed production clusters

It can run indefinitely, but cluster membership is static.

### Cluster-Aware

With `ETCD_ENDPOINTS` configured, Dagens uses an etcd-backed distributed registry.

This adds:

- dynamic node registration
- health-filtered node views
- watch/reconnect behavior
- better support for elastic clusters and node churn

etcd improves cluster coordination, but it is not the same as durable workflow state.

## Human-In-The-Loop (HITL)

Dagens includes a first-class HITL pause/resume flow:

1. A workflow reaches a human checkpoint.
2. Execution returns a pending state.
3. A callback is validated for authenticity and idempotency.
4. A resumption worker reloads checkpoint state.
5. Execution resumes from the checkpoint.

This makes approval-driven workflows a runtime primitive, not an afterthought.

## Durability and Failure Semantics

Dagens is intentionally explicit about current guarantees:

- scheduler queueing is in-memory by default
- job and task result state is not durably persisted by default
- etcd provides coordination durability, not full workflow durability
- exactly-once execution is not guaranteed in v0.1.x

For the current guarantees, read:

- `docs/DURABILITY.md`
- `docs/FAILURE_SEMANTICS.md`
- `docs/SCALING_AND_DISPATCH_CLARIFICATION.md`

## Positioning

Dagens should be understood as:

- a control-plane runtime
- with an execution-plane worker fabric
- for distributed agent and service orchestration

It is infrastructure first, with AI-specific integrations layered on top.
