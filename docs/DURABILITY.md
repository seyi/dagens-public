# Durability

This document describes what Dagens persists today, where it is stored, and what durability guarantees are actually provided by the current implementation.

This is intentionally conservative. If a guarantee is not clearly implemented in code, it is not claimed here.

## TL;DR

- **Scheduler jobs and task results** are stored **in memory** on the API server.
- **Cluster membership** can be stored in **etcd**, but only for node discovery/health metadata.
- **HITL checkpoints** have a **durable PostgreSQL implementation available**, but default/simple implementations are in-memory.
- **HITL idempotency and resumption queue** have **durable Redis implementations available**, but simple implementations are in-memory.
- **Events** are in-memory unless explicitly wired to an event store, and the provided event store is also in-memory.
- The default runtime is **not yet a fully durable control plane**.

Here, "task results" means response payloads attached to in-memory task/job structs, not a durable artifact store.

## Defaults in Current Public Release

- Scheduler store: in-memory
- Event store: in-memory
- Registry: static unless `ETCD_ENDPOINTS` is set
- HITL checkpoint store: in-memory unless explicitly wired to PostgreSQL
- HITL idempotency store: in-memory unless explicitly wired to Redis
- HITL resumption queue: in-memory unless explicitly wired to Redis Streams

## Durability Matrix

| Component | Default store | Durable option | Enabled by default |
| --- | --- | --- | --- |
| Jobs | memory | (planned) | no |
| Registry | static/memory | etcd | only if configured |
| HITL checkpoints | memory | Postgres | no |
| HITL idempotency | memory | Redis | no |
| HITL queue | memory | Redis Streams | no |
| Events | memory | (planned) | no |

## What Is Persisted Today

### 1. Scheduler Jobs

Current behavior:

- Jobs are stored in the scheduler's in-memory `jobs` map.
- Pending jobs are buffered in an in-memory `jobQueue` channel.
- Task outputs are attached to in-memory task structs.

Code reference:

- [`pkg/scheduler/scheduler.go`](../pkg/scheduler/scheduler.go)

Implication:

- Jobs and results are lost if the API server process restarts.
- There is no built-in durable job store for the core scheduler path.

### 2. Cluster Membership and Node Metadata

When `ETCD_ENDPOINTS` is configured:

- worker and API node metadata are stored in etcd under leased keys
- node entries are ephemeral and tied to etcd sessions
- local caches are repopulated from watch events

Code references:

- [`pkg/registry/distributed_registry.go`](../pkg/registry/distributed_registry.go)
- [`cmd/api_server/main.go`](../cmd/api_server/main.go)
- [`cmd/worker/main.go`](../cmd/worker/main.go)

Implication:

- etcd durability applies to **registry metadata**, not scheduler jobs or task outputs.
- This is coordination durability, not workflow durability.

### 3. HITL Checkpoints

HITL checkpoints are the strongest durability primitive in the repository.

Checkpoint data includes:

- `graph_id`
- `graph_version`
- `node_id`
- serialized `state_data`
- `request_id`
- failure counters and timestamps

Code reference:

- [`pkg/hitl/models.go`](../pkg/hitl/models.go)

## Available Backends Not Enabled by Default

Available implementations:

#### PostgreSQL (Durable)

- `PostgresCheckpointStore` stores checkpoints in PostgreSQL tables
- tables are created automatically if missing
- a DLQ table is also maintained

Code reference:

- [`pkg/hitl/postgres_store.go`](../pkg/hitl/postgres_store.go)

Important note:

- This implementation exists, but is not wired into the default `cmd/api_server` / `cmd/worker` startup path today.
- The Compose file includes Postgres, but the core runtime binaries do not automatically use it for scheduler/job durability.

#### Simple “RedisCheckpointStore” (Not Actually Durable)

- `RedisCheckpointStore` in `pkg/hitl/implementation.go` is currently a Go map, not a real Redis-backed store
- Despite the name, this implementation is an **in-memory stub**

Code reference:

- [`pkg/hitl/implementation.go`](../pkg/hitl/implementation.go)

Implication:

- Despite its name, this default/simple implementation is in-memory and process-local.

### 4. HITL Idempotency Keys

Available durable implementation:

- `RedisIdempotencyStore` stores callback/idempotency keys in Redis

Code reference:

- [`pkg/hitl/redis_store.go`](../pkg/hitl/redis_store.go)

Implication:

- If wired correctly, duplicate callback suppression can survive process restarts.
- If using a simple in-memory substitute, it does not survive restarts.

### 5. HITL Resumption Queue

Available durable implementation:

- `RedisResumptionQueue` uses Redis Streams with consumer groups
- supports ack-based processing and stale-claim behavior

Code reference:

- [`pkg/hitl/redis_store.go`](../pkg/hitl/redis_store.go)

Non-durable/simple implementation:

- `SimpleInMemoryQueue` uses a Go channel

Code reference:

- [`pkg/hitl/implementation.go`](../pkg/hitl/implementation.go)

Implication:

- Redis-backed mode provides queue durability and recovery semantics.
- In-memory queue mode loses queued resumption work on process restart.

### 6. Events

Current event persistence implementation:

- `MemoryEventStore`

Code reference:

- [`pkg/events/events.go`](../pkg/events/events.go)

Implication:

- Event persistence is currently in-memory only.
- There is no built-in durable event store backend in the core path.

## What Is Best-Effort Only

These behaviors should be treated as best-effort today:

- scheduler job retention
- task result retention
- event retention
- API-visible job history after API restart
- in-memory HITL queue or checkpoint flows

## Failure Behavior

### API Server Restart

What is lost:

- in-memory scheduler jobs
- queued jobs
- task outputs attached to in-memory job structs

Restart behavior today:

- scheduler startup recovery is visibility-only when transition history is available
- recovered jobs/tasks are rebuilt into in-memory views
- recovered work is not automatically re-enqueued or redispatched
- with the current default in-memory transition store, process restart still drops transition history unless a durable transition backend is configured
- startup recovery is bounded by `RecoveryTimeout` (default `5m`) to avoid indefinite startup hangs
- recovery observability distinguishes `status=failed` from `status=canceled` for operator triage

What may survive:

- etcd node registry state (if etcd is in use and peers are still registered)
- HITL checkpoints, if using `PostgresCheckpointStore`
- HITL idempotency and queue state, if using Redis-backed implementations

### Worker Restart

What happens:

- worker process stops serving gRPC
- if using etcd-backed registry, its leased node key eventually disappears
- in-flight execution state inside the worker is not durably recovered by the core worker runtime

Implication:

- currently, worker restart is not a general-purpose task recovery mechanism
- recovery depends on higher-level retry/re-submit behavior, not durable worker execution logs

### etcd Loss

What is affected:

- dynamic registry updates
- leased node membership
- watch-based cluster changes

What is not directly stored there:

- scheduler jobs
- task results
- HITL checkpoints

Implication:

- etcd loss affects node discovery/health coordination
- it does not restore or preserve job execution state

### PostgreSQL Loss

If using PostgreSQL checkpoint store:

- durable HITL checkpoints become unavailable
- resume operations that require those checkpoints cannot proceed

If not using PostgreSQL checkpoint store:

- the default core scheduler path is already non-durable, so Postgres loss does not change scheduler durability

### Redis Loss

If using Redis-backed HITL:

- idempotency checks fail open or fail hard depending on error handling path
- resumption queue becomes unavailable
- duplicate suppression and queued resume behavior degrade

If using only in-memory implementations:

- Redis is not part of the active durability story

## Current Guarantee Target vs. Future Target

### Current Reality

Today, Dagens provides:

- a durable **cluster registry** when etcd is enabled
- a potentially durable **HITL subsystem** if PostgreSQL/Redis-backed stores are explicitly wired in
- a mostly **in-memory scheduler control plane**

### Intended Future Target

The likely mature durability target is:

- durable job metadata
- durable results/state transitions
- explicit replay/recovery semantics after control-plane restart
- leader-aware scheduling for HA control planes
- durable event store
- clear, documented dispatch and retry guarantees

## Honest Summary

Dagens already contains meaningful durability building blocks, especially around HITL and cluster coordination.

But the default runtime should currently be described as:

> A control-plane runtime with selective durable subsystems, not yet a fully durable workflow orchestrator.
