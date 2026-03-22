# Failure Semantics

This document describes Dagens' current failure behavior and execution semantics.

This is a statement of the code as it exists today, not an aspirational design.

## TL;DR

- Core scheduler dispatch is **push-based**.
- Core task execution has **no built-in scheduler retry**.
- Scheduler stage semantics are currently **fail-fast**: one task error fails the stage.
- Core scheduler job/task semantics are **not durably recoverable** across API restart.
- HITL resume can achieve **at-least-once queue delivery** when using Redis Streams, with duplicate suppression handled by idempotency keys.
- “Healthy” workers currently means “present in registry with `Healthy=true`,” not deep runtime admission control.
- Dagens does **not** provide exactly-once semantics today.

## Defaults in v0.1.0

- Scheduler dispatch: single-attempt
- Scheduler retries: none
- Worker health: registry-visible and `Healthy=true`
- HITL duplicate suppression: only durable if backed by Redis idempotency storage
- Queue durability: only durable if using Redis Streams

## Dispatch Semantics

The scheduler:

1. accepts a job
2. stores it in an in-memory queue
3. selects a node
4. pushes execution via `ExecuteOnNode`

Code reference:

- [`pkg/scheduler/scheduler.go`](../pkg/scheduler/scheduler.go)

This is not a worker-pull system.

## At-Least-Once vs. At-Most-Once

### Core Scheduler Task Dispatch

Current effective behavior:

- The scheduler sends a task once.
- There is no built-in task retry loop in the scheduler.
- If dispatch or remote execution returns an error, the task fails and the stage fails.

Code reference:

- [`pkg/scheduler/scheduler.go`](../pkg/scheduler/scheduler.go)

Practical interpretation:

- From the scheduler's perspective, this is closest to **single-attempt dispatch**.
- It is **not** a true at-least-once scheduler.
- It is also **not** a strong at-most-once guarantee from a side-effect perspective, because a transport failure can happen after the remote worker has already performed side effects but before a successful response is observed.

Ambiguity can occur when:

- a worker performs side effects successfully
- but the RPC response is lost, delayed, or the connection fails before the scheduler receives a successful response

The honest statement is:

> Core dispatch is single-attempt, with possible ambiguity on transport failure.

### HITL Resumption Queue

If using `RedisResumptionQueue`:

- delivery is **at-least-once**
- jobs must be acknowledged after successful processing
- stale pending jobs can be claimed and retried by another consumer

Code reference:

- [`pkg/hitl/redis_store.go`](../pkg/hitl/redis_store.go)

If using `SimpleInMemoryQueue`:

- semantics are process-local and non-durable
- jobs disappear on process crash/restart

## Idempotency Expectations

### Scheduler / Normal Task Execution

There is no global idempotency framework for normal scheduler task execution today.

Implication:

- If your node logic has side effects, the node implementation itself should be idempotent or compensate externally.
- Dagens does not currently provide a durable exactly-once execution boundary for general tasks.

### HITL Resume Path

HITL has explicit duplicate suppression via idempotency keys:

- callback handler checks final idempotency key (`callback-done:<requestID>`)
- callback handler also uses short-lived processing locks
- resumption worker sets final idempotency marker before resume execution

Code references:

- [`pkg/hitl/callback_handler.go`](../pkg/hitl/callback_handler.go)
- [`pkg/hitl/resumption_worker.go`](../pkg/hitl/resumption_worker.go)
- [`pkg/hitl/orchestrator.go`](../pkg/hitl/orchestrator.go)

Practical interpretation:

- Duplicate callbacks are intentionally tolerated.
- Resume processing is designed to be idempotent at the callback/request boundary when durable idempotency storage is used.

## Retry Policy Boundaries

### Scheduler Layer

Current behavior:

- no automatic retry of failed tasks in the scheduler
- any task error fails the containing stage

Code reference:

- [`pkg/scheduler/scheduler.go`](../pkg/scheduler/scheduler.go)

### Agentic / Execution Wrapper Layer

Retries exist at the wrapper level, not the scheduler level:

- `pkg/agentic/SelfHealing` provides retry-with-backoff
- optional circuit breaker support is integrated there

Code reference:

- [`pkg/agentic/healing.go`](../pkg/agentic/healing.go)

Practical interpretation:

- retries are currently an execution concern around the invoked unit of work
- retries are not a control-plane scheduling concern yet

### HITL Resume Layer

HITL resumption worker has bounded retry-like behavior:

- errors matching simple retryable patterns (`timeout`, `connection refused`, `database is locked`) can cause reprocessing
- `FailureCount` is incremented
- retries stop at `MaxResumptionAttempts`
- permanent failures or exhausted retries move checkpoint to DLQ

Code reference:

- [`pkg/hitl/resumption_worker.go`](../pkg/hitl/resumption_worker.go)

## Graph and Stage Failure Behavior

Current scheduler semantics:

- stages execute sequentially
- tasks inside a stage execute concurrently
- one task failure causes stage failure
- one stage failure causes job failure (or blocked if policy-related)

Code reference:

- [`pkg/scheduler/scheduler.go`](../pkg/scheduler/scheduler.go)

This is a simple fail-fast model.

## HITL Resume Under Duplicates

Current intent:

- duplicate callbacks should not cause repeated successful resume execution for the same request

Mechanism:

1. callback handler checks `callback-done:<requestID>`
2. acquires `processing:<requestID>` via `SetNX`
3. enqueue happens once (or duplicates are acknowledged as already processing)
4. resumption worker sets final idempotency key before resuming execution

Code references:

- [`pkg/hitl/callback_handler.go`](../pkg/hitl/callback_handler.go)
- [`pkg/hitl/resumption_worker.go`](../pkg/hitl/resumption_worker.go)

Important caveat:

- These guarantees are only as durable as the configured idempotency store.
- If idempotency is process-local or unavailable, duplicate protection degrades.

## HITL Runtime Contracts

These are the explicit contracts for HITL pause/resume in the current implementation.

### 1. Idempotency Scope: `request_id`

- The idempotency boundary is the HITL `request_id`.
- Duplicate callback delivery for the same `request_id` is expected and tolerated.
- Duplicate suppression uses:
  - `callback-done:<request_id>` as final marker
  - `processing:<request_id>` as short-lived in-flight lock
- Contract: resume requests are idempotent by `request_id` when durable idempotency storage is configured.

Code references:

- [`pkg/hitl/callback_handler.go`](../pkg/hitl/callback_handler.go)
- [`pkg/hitl/resumption_worker.go`](../pkg/hitl/resumption_worker.go)
- [`pkg/hitl/orchestrator.go`](../pkg/hitl/orchestrator.go)

### 2. Resume Delivery Semantics: At-Least-Once

- HITL resume processing is at-least-once with Redis Streams-backed queueing.
- A control-plane/worker crash during resume can cause replay after restart if ACK was not recorded.
- Contract: side-effecting node logic on resumed paths must be idempotent or externally compensated.
- Dagens does not claim exactly-once execution semantics for resumed graph tails today.

Code references:

- [`pkg/hitl/redis_store.go`](../pkg/hitl/redis_store.go)
- [`pkg/hitl/resumption_worker.go`](../pkg/hitl/resumption_worker.go)
- [`pkg/graph/graph.go`](../pkg/graph/graph.go)

### 3. Version Policy: Strict Graph Version Pinning

- Checkpoints persist `graph_version` and resume validates it against the currently registered graph definition.
- On mismatch, resume is rejected; worker path moves checkpoint to DLQ and acknowledges the queue message.
- No compatibility-mode resume is attempted automatically in the runtime today.

Code references:

- [`pkg/hitl/models.go`](../pkg/hitl/models.go)
- [`pkg/hitl/resumption_worker.go`](../pkg/hitl/resumption_worker.go)
- [`pkg/hitl/orchestrator.go`](../pkg/hitl/orchestrator.go)

## Worker Health Semantics

Today, “healthy” is relatively shallow.

In registry terms, a worker is healthy if:

- it is present in the registry cache
- its node record has `Healthy=true`

Code reference:

- [`pkg/registry/distributed_registry.go`](../pkg/registry/distributed_registry.go)

Current system does not yet implement:

- per-worker in-flight admission control
- scheduler-visible load shedding
- deep liveness/readiness checks tied to actual execution capacity

So the honest definition is:

> Health currently means registry-visible and marked healthy, not proven available capacity.

## What Happens on Common Failures

### Task RPC Error

Current behavior:

- task marked failed
- stage fails
- job fails (or blocks for policy violation)

There is no built-in automatic re-dispatch by the scheduler.

### Worker Disappears Mid-Execution

Current behavior:

- API side sees RPC/connection failure or timeout
- task fails from scheduler perspective
- etcd-backed registry may eventually remove worker membership

Any side effects completed before failure are not automatically reconciled.

### API Server Restarts

Current behavior:

- in-memory scheduler state is lost
- job history is lost
- queued work is lost

Only separately durable subsystems (for example, durable HITL stores) can survive that event.

### Duplicate Human Callback

Current behavior:

- duplicate callback is either rejected as already done or accepted as already processing
- intended to avoid duplicate resume execution

This is one of the stronger semantics in the codebase today.

## Honest Summary

The current failure model is best described as:

> Fail-fast, single-attempt scheduler dispatch with stronger duplicate control in the HITL resume path than in the general task execution path.

That is good enough for an early infrastructure release, as long as it is documented explicitly and not overstated.
