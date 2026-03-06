# Backpressure Design

This document defines the control-plane saturation model for Dagens v0.2.

The goal is to make the control plane safe under load by turning overload into an explicit, observable condition instead of allowing unbounded queue growth or silent degradation.

## Current Implementation Status

Implemented in the current v0.2 work:

- bounded in-memory scheduler queue
- explicit admission rejection when the queue is full
- `429 Too Many Requests` plus `Retry-After: 5`
- worker capacity heartbeats with:
  - `node_id`
  - `in_flight`
  - `max_concurrency`
  - `report_timestamp`
- scheduler-side dual capacity tracking:
  - local `ReservedInFlight`
  - worker-reported `ReportedInFlight`
- worker-reported `max_concurrency` capped by scheduler config
- freshness-aware capacity selection using `capacity_ttl`
- degraded-mode fallback to stale capacity when no fresh-capacity worker is available
- dispatch cooldown after capacity-conflict-style rejections
- bounded capacity-conflict retry using `max_dispatch_attempts`
- backpressure metrics for:
  - queue current depth
  - queue configured max
  - queue observed high-water mark
  - all-workers-full
  - degraded mode
  - dispatch cooldown activations
  - dispatch rejections by reason
  - heartbeat ingestion pipeline
  - task dispatch retries
  - tasks failed after exhausting max dispatch attempts

Still intentionally not implemented:

- true deferred/requeue scheduler semantics when a stage cannot place work
- durable overflow queueing
- durable recovery of waiting work across scheduler restart
- stronger heartbeat identity binding beyond shared-token auth
- per-worker capacity gauges (deferred to avoid premature label-cardinality growth)

## Historical Context

Current state in v0.1.x:

- scheduler queueing was in-memory only
- submission could outpace execution
- overload behavior was not explicit
- worker capacity was not a first-class scheduler signal

Target state for v0.2:

- bounded scheduler queue
- explicit admission control
- worker capacity reporting
- load-aware scheduling
- observable saturation metrics

## Design Goals

The backpressure model should provide:

1. Predictable overload behavior
2. Simple operator understanding
3. Bounded memory growth in the control plane
4. Clear client-facing failure semantics
5. Scheduler decisions based on real capacity, not health-only routing

## Non-Goals

This v0.2 design does not attempt to provide:

- exactly-once execution
- durable overflow queues
- global fair scheduling across tenants
- advanced priority queues
- automatic multi-cluster spillover

Those may come later, but they are not required for the first serious backpressure implementation.

## Core Model

Dagens remains a push-based control plane:

1. Client submits work to the API server
2. API server validates and attempts admission
3. Admitted work enters the scheduler queue
4. Scheduler selects a worker based on health and capacity
5. Work is pushed to a worker over gRPC

Backpressure is introduced at two points:

1. Admission time
2. Dispatch time

## Admission Control

### Queue Bound

The scheduler queue must be bounded.

Required configuration:

- `JobQueueSize`

Behavior:

- If queue depth is below `max_queue_depth`, a new job may be admitted.
- If queue depth is at or above `max_queue_depth`, the API server rejects the job immediately.

This prevents the control plane from becoming an unbounded in-memory sink.

### Saturation Response

When the queue is full:

- the API server returns an explicit overload response
- the response should map to `429 Too Many Requests` or equivalent overload semantics
- the response should clearly indicate that the system is saturated, not that the request is invalid
- the response must include a `Retry-After` header

The caller must be able to distinguish:

- malformed request
- authorization failure
- system saturation

### Retry-After Requirement

To prevent client retry storms, the API server must provide retry guidance on saturation.

For v0.2:

- `429` responses must include `Retry-After`
- current implementation uses a static value of `5` seconds
- later implementations may derive it from current queue drain behavior

Clients and SDKs must respect this value to maintain system stability.

### First v0.2 Policy

The first v0.2 admission policy should be:

- bounded queue
- immediate reject on saturation

Do not start with:

- indefinite blocking
- hidden retries
- overflow to another queue

Immediate reject is easier to reason about and safer for operators.

## Worker Capacity Model

Workers must report capacity as explicit scheduler-visible state.

### Required Signals

Each worker should expose:

- `in_flight`
  - current number of executing tasks
- `max_concurrency`
  - maximum simultaneous tasks that worker is willing to execute

From these, the scheduler derives:

- `available_slots = max_concurrency - in_flight`

### Reported vs Reserved Capacity

The scheduler does not rely only on worker-reported state.

It tracks two values per worker:

- `ReservedInFlight`
  - local scheduler-side reservation count for work it has already selected for dispatch
- `ReportedInFlight`
  - worker-reported count from heartbeats

The effective in-use count is:

- `max(ReservedInFlight, ReportedInFlight)`

This is intentionally conservative. It avoids undercounting when:

- the scheduler has just reserved a slot but has not yet received a fresh heartbeat
- a worker heartbeat is slightly delayed

### Concurrency Cap

Workers may report their own `max_concurrency`, but the scheduler enforces:

- `MaxWorkerConcurrencyCap`

If a worker reports a larger value, the scheduler clamps it to the configured cap.

This protects the control plane from a misconfigured or overly optimistic worker attracting unsafe amounts of traffic.

### Optional Signals

These are useful later, but not required for the first implementation:

- recent execution latency
- recent error rate
- memory pressure
- CPU load

v0.2 should start with simple, reliable signals rather than complex heuristics.

## Capacity Reporting Transport

The scheduler needs a clear transport model for capacity updates.

### Recommended v0.2 Model

Use:

- worker-initiated periodic capacity heartbeats

Each worker should periodically publish a capacity snapshot to the control plane.

This is preferred over scheduler polling because:

- it keeps workers responsible for reporting their own local state
- it avoids turning the scheduler into an active poller of all workers
- it reduces coupling between placement logic and transport timing

### Capacity Snapshot Contents

Current implementation includes:

- worker ID
- `in_flight`
- `max_concurrency`
- report timestamp

The stronger session-identity binding described below remains a follow-up item.

This gives the scheduler enough information to make load-aware decisions and detect stale state.

Worker identity is mandatory in the payload.

Today, the heartbeat endpoint is:

- authenticated outside `DEV_MODE`
- protected by a shared worker token

This is sufficient for the current internal control-plane path, but stronger identity binding remains a future hardening step.

Longer term, worker identity should also be bound to a stronger authenticated session identity rather than only a shared token.

### Why Not Scheduler Polling

Do not make scheduler polling the primary v0.2 mechanism.

Polling:

- increases control-plane chatter
- makes scheduling depend on synchronous remote checks
- adds latency to placement decisions

The scheduler should instead make decisions from the latest known capacity snapshot.

## Freshness and Capacity TTL

Capacity data must have an explicit freshness model.

Without this, a crashed or partitioned worker can appear to hold slots forever, leading to capacity leakage or bad placement decisions.

### Required Rule

Every capacity snapshot must be considered valid only for a bounded time window.

Required configuration:

- `capacity_ttl`

Behavior:

- if the latest capacity snapshot is newer than `CapacityTTL`, it is considered fresh
- if the latest capacity snapshot is older than `CapacityTTL`, it is considered stale

### Stale Capacity Behavior

If capacity becomes stale:

- do not trust the worker's reported `in_flight`
- do not assume the worker has free capacity
- treat the worker as **capacity unknown**

For normal load-aware scheduling, stale-capacity workers should:

- be deprioritized below workers with fresh capacity
- remain eligible as a degraded fallback if no fresh-capacity worker with available slots exists

This is safer than forcing:

- `in_flight = 0`

because a partitioned or delayed worker may still be executing work even if the control plane has stopped hearing from it.

### Relationship to Health

Capacity freshness and worker health are related but not identical.

- a worker may still be considered healthy but have stale capacity data
- a worker may become fully unhealthy and be removed from placement entirely

The scheduler should distinguish:

- healthy with fresh capacity
- healthy with stale/unknown capacity
- unhealthy / not eligible

### Current v0.2 Selection Order

The current scheduler selection order is:

1. healthy workers with fresh capacity and available slots
2. healthy workers with stale capacity and available slots (degraded mode)
3. no selection

Workers in dispatch cooldown are excluded from both categories until:

- the cooldown expires
- or a later selection pass sees them as eligible again

## Update Frequency and Storm Control

Capacity reporting must not create avoidable control-plane storms.

### Baseline Policy

For v0.2, the default reporting mode is:

- periodic

Current default interval:

- every 2 seconds

This creates a stable cadence that is easy to reason about operationally.

## Retry Boundary

Retries are intentionally narrow.

The scheduler does not implement general-purpose hidden retries for:

- transport failures
- timeouts
- worker crashes
- generic dispatch errors

Those errors fail immediately.

The only built-in retry path is:

- bounded capacity-conflict redispatch

Current behavior:

- retries only on explicit capacity-conflict-style dispatch failures
- retry budget is controlled by `MaxDispatchAttempts`
- when capacity-conflict retry budget is exhausted, the task fails explicitly

This keeps retry semantics visible and constrained.

## Current No-Capacity Semantics

The current implementation is still intentionally conservative.

If a stage cannot select a worker for a task because no healthy worker has capacity:

- the scheduler records the all-workers-full metric
- the scheduler logs the saturation event
- the stage fails

This is an interim v0.2 semantic.

Dagens does **not** yet provide a true deferred/requeue scheduler model for waiting work once a stage is already executing.

That stronger waiting-state model is a later step because it should be introduced alongside stronger durability semantics.

## Metrics To Watch

Current backpressure-relevant metrics:

- `dagens_task_queue_length`
- `dagens_task_queue_config_max`
- `dagens_task_queue_observed_max`
- `dagens_scheduler_all_workers_full_total`
- `dagens_scheduler_degraded_mode_total`
- `dagens_scheduler_dispatch_cooldown_activations_total`
- `dagens_scheduler_dispatch_rejections_total`
- `dagens_worker_heartbeats_received_total`
- `dagens_worker_heartbeats_succeeded_total`
- `dagens_worker_heartbeat_auth_failed_total`
- `dagens_worker_heartbeat_invalid_payload_total`
- `dagens_worker_heartbeat_processing_duration_seconds`
- `dagens_task_dispatch_retries_total`
- `dagens_tasks_failed_max_dispatch_attempts_total`

These metrics are the primary operator view of:

- admission saturation
- stale-capacity fallback
- dispatch failure causes
- heartbeat health
- retry pressure

Note:

- periodic heartbeats introduce eventual consistency
- if task duration is significantly shorter than the heartbeat interval, capacity data will lag reality
- this may temporarily reduce utilization accuracy

That trade-off is acceptable for v0.2 because control-plane stability is more important than perfect utilization.

### Graceful Shutdown Signaling

During graceful shutdown, workers should publish a final draining capacity state.

Recommended behavior:

- set `max_concurrency = 0`
- continue reporting `in_flight` until active tasks drain or the worker exits

This allows the scheduler to stop assigning new work while existing work completes.

### Event-Driven Updates

Do not start with unthrottled event-driven updates on every task start/finish as the only mechanism.

That approach can create bursts when:

- many tasks start simultaneously
- many tasks complete simultaneously
- a large worker fleet changes state at once

If event-driven updates are later added, they should be:

- coalesced
- rate-limited
- secondary to the periodic heartbeat model

### Scheduler Consumption Model

The scheduler should read from:

- the latest cached capacity snapshot per worker

It should not require a fresh round-trip to every worker during placement.

## Capacity Failure Handling

The system must define what happens when capacity information or worker liveness is lost.

### If a Worker Stops Reporting Capacity

If heartbeats stop and capacity becomes stale:

- mark capacity as unknown
- stop treating that worker as a preferred load-aware target
- rely on health and registry logic to determine whether the worker remains eligible at all

### If Worker Health Is Lost

If the worker is no longer considered healthy:

- remove it from eligible placement

At that point, task recovery is a separate concern and must be handled by task-state semantics, not by guessing from stale capacity alone.

### Slot Leakage Prevention

To prevent long-lived capacity distortion:

- stale capacity must expire via TTL
- eligibility must be bounded by health/lease semantics
- in-flight task recovery must be handled by explicit task-state logic, not by assuming slot release

This keeps backpressure design honest and avoids hidden recovery behavior.

## Scheduling Under Backpressure

### Selection Rule

The target v0.2 selection policy is:

- choose from healthy workers only
- prefer the worker with the lowest `in_flight`

Equivalent framing:

- prefer the worker with the highest `available_slots`

### Full-Capacity Behavior

If all healthy workers report no available slots:

- the scheduler must not treat the cluster as unconstrained
- queued jobs remain queued until capacity becomes available
- if admission is attempted while the queue is already full, the API server rejects new work

This creates a simple two-stage model:

1. cluster has capacity: admit and dispatch
2. cluster is full: queue until bounded capacity is exhausted, then reject

### Fallback Behavior

If capacity signals are temporarily unavailable:

- fall back to health-only selection with degraded preference ordering
- prefer workers with fresh capacity over workers with stale/unknown capacity
- increment a metric indicating degraded scheduling mode

This should be a temporary fallback, not the normal operating mode.

## Dispatch Reject Cooldown

Cached capacity data can be slightly stale even when the heartbeat model is functioning as designed.

That creates a thrashing risk:

1. scheduler selects a worker using a recent cached snapshot
2. worker rejects the dispatch because it is actually full
3. scheduler immediately retries using the same stale snapshot

Without protection, this can create tight dispatch/reject loops.

### Required v0.2 Protection

If a worker rejects a dispatch due to capacity conflict:

- mark that worker as temporarily unavailable for scheduling
- hold that state for a short reject cooldown
- clear that state when:
  - the next fresh capacity heartbeat arrives, or
  - the cooldown expires

Suggested initial cooldown:

- 5 seconds

This prevents repeated dispatch attempts against obviously stale cached state.

Implementation note:

- process the fresh heartbeat first
- update capacity state
- then clear temporary-unavailable cooldown

This avoids clearing cooldown before new capacity data is actually applied.

## Dispatch-Time Backpressure

Admission control protects the queue.
Dispatch-time backpressure protects the workers.

At dispatch time:

- the scheduler should only dispatch to workers with available slots
- if a worker becomes saturated between selection and dispatch, the dispatch should fail cleanly
- the task should remain in a schedulable state rather than being silently lost
- the worker should enter reject cooldown if the failure is a capacity conflict

The exact retry or requeue semantics must be documented alongside scheduler retry policy.

### Requeue Ordering

If a task returns to the queue after dispatch rejection:

- requeue it at the tail of the queue

This prevents a repeatedly rejected task from monopolizing the front of the queue during saturation.

## Failure Semantics

The v0.2 backpressure model does not change the current core truth:

- Dagens does not provide exactly-once execution

But it must make overload semantics explicit.

### Admission Failure

If the queue is full at submit time:

- the job is not admitted
- no execution is attempted
- the caller receives an overload response

This is the cleanest failure mode in the system.

### Queued But Not Yet Dispatched

If a job has been admitted but not yet dispatched:

- it remains queued until capacity is available
- if the process restarts before durability improvements land, in-memory queue state may be lost

This is why backpressure and durability should be developed together.

### Dispatch Rejection

If a selected worker cannot accept work at dispatch time:

- the scheduler should treat this as a capacity conflict or transient dispatch failure
- the task must return to a schedulable state or fail explicitly
- the behavior must be observable in metrics and logs

Repeated dispatch rejections must not loop forever.

For v0.2:

- a task that fails dispatch due to capacity conflict 3 times should move to a failed state
- dispatch rejection should not requeue indefinitely

This prevents permanent queue stagnation when admitted load cannot be placed on available workers.

## Metrics

The following metrics should exist for v0.2.

### Queue Metrics

- `current_queue_depth`
  - current number of queued jobs/tasks
- `config_max_queue_depth`
  - configured hard queue limit
- `observed_max_queue_depth`
  - high-water mark over process lifetime
- total rejected submissions due to saturation

### Worker Capacity Metrics

- worker `in_flight`
- worker `max_concurrency`
- worker `available_slots`
- worker capacity freshness / staleness
- dispatch reject cooldown activations
- total cluster available slots

### Scheduler Metrics

- scheduling decisions by policy
- capacity-based deferrals
- degraded-mode selections (when capacity signals are missing)
- dispatch rejections by reason
- workers currently in temporary-unavailable cooldown

### Admission Metrics

- accepted submissions
- rejected submissions
- rejection reason counts

### Metric Labeling Note

Metrics labeled by `worker_id` can grow in cardinality in large fleets.

To keep dashboards and alerting usable:

- provide per-worker metrics for detailed diagnosis
- also provide aggregated cluster-level metrics for normal operational monitoring

## Logging

Overload and capacity events should be explicit in logs.

Required loggable events:

- job rejected due to queue saturation
- worker skipped due to zero available slots
- scheduler entering degraded mode
- dispatch rejected because worker became full
- worker entered temporary-unavailable cooldown

These events should be easy to correlate with job IDs and worker IDs.

## Configuration

The first implementation should expose a small configuration surface:

- `max_queue_depth`
- `default_worker_max_concurrency`
- `max_worker_concurrency_cap`
- `capacity_heartbeat_interval`
- `capacity_ttl`
- `dispatch_reject_cooldown`
- `retry_after_seconds`

Avoid a large matrix of queue tuning knobs in v0.2.
Start with a small, understandable model.

### Worker Concurrency Guardrail

Workers may advertise their own `max_concurrency`, but the scheduler should enforce a hard upper bound.

Recommended rule:

- scheduler uses the lower of:
  - worker-reported `max_concurrency`
  - configured `max_worker_concurrency_cap`

This prevents a misconfigured or malicious worker from attracting unsafe amounts of traffic.

### Suggested Initial Defaults

Reasonable v0.2 starting defaults:

- `capacity_heartbeat_interval = 2s`
- `capacity_ttl = 5s`
- `dispatch_reject_cooldown = 5s`
- `retry_after_seconds = 5s`
- `max_worker_concurrency_cap = 100`

These should be treated as operational starting points, not permanent universal values.

## Rollout Plan

### Phase 1

- add queue bound
- reject on full queue
- expose queue metrics

### Phase 2

- add worker `in_flight`
- add worker `max_concurrency`
- add least-in-flight selection

### Phase 3

- refine dispatch-time conflict handling
- add degraded-mode metrics and logs

## Release Criteria

Backpressure for v0.2 should only be considered complete when:

1. The scheduler queue is bounded
2. Overload produces explicit admission failure
3. Worker capacity is visible to the scheduler
4. Scheduling uses capacity, not just health
5. Saturation is visible in metrics and logs
6. Client retry behavior is defined through `Retry-After`
7. Client retry behavior is validated in integration tests

## Relationship To Other Docs

This document should be read alongside:

- `docs/DURABILITY.md`
- `docs/FAILURE_SEMANTICS.md`
- `docs/SCALING_AND_DISPATCH_CLARIFICATION.md`

Backpressure without durability still limits overload.
Durability without backpressure still risks unsafe overload.
v0.2 should improve both together.
