# Control Plane HA Design

This document defines the intended high-availability model for the Dagens control plane.

The goal is to move from a single active scheduler model to a leader-controlled, failure-aware control plane without introducing double-dispatch or unclear ownership.

## Status

Current state in v0.1.x:

- the control plane is effectively single-active
- scheduling decisions are made by one API server instance
- queue state is in-memory
- there is no complete HA scheduler ownership model

Current implementation progress in v0.2:

- scheduler now has a `LeadershipProvider` seam
- dispatch is gated by leadership authority checks in the run loop
- follower instances defer dispatch and requeue work instead of executing it
- default provider keeps single-node behavior (`is_leader=true`) for compatibility
- etcd-backed leadership provider is available via API server configuration
- dispatch claims are fenced via state-guarded CAS before worker RPC
- scoped authority helper supports validated scope annotation (`IsValidScope`)
- integration coverage exists for etcd authority and scoped annotation paths
- failover drill now includes durable integrity checks for non-terminal canary tasks
- drill SQL checks are scoped to current run (`DRILL_ID` + start timestamp)
- planned shutdown path performs best-effort explicit leader resignation before exit
- scheduler supports optional critical webhook alerts for recovery/leadership startup failures

Target state for v0.2+:

- one active scheduler leader at a time
- standby control-plane instances allowed
- explicit leader election
- dispatch ownership guarded by lease/fencing semantics
- restart and failover behavior documented

## Design Goals

The HA model should provide:

1. A single active dispatcher at any point in time
2. Clear ownership of scheduling decisions
3. Split-brain resistance
4. Predictable failover behavior
5. Minimal ambiguity during leader transitions

## Non-Goals

This design does not attempt to provide:

- exactly-once execution
- zero-gap failover with no in-flight ambiguity
- multi-leader scheduling
- geo-distributed consensus
- full workflow replay in v0.2

The first goal is safe leadership and explicit dispatch authority.

## Core HA Model

The Dagens control plane should use:

- multiple API server instances may exist
- exactly one instance may act as the active scheduler leader
- non-leader instances may:
  - serve read APIs
  - accept submissions if the architecture allows it
  - forward or persist work for the leader
  - remain passive for dispatch

Only the active leader may:

- dequeue schedulable work
- make dispatch decisions
- issue execution RPCs to workers

This is the core invariant:

**There must be only one active dispatcher.**

## Leadership Model

### Recommended Approach

Use leader election backed by etcd leases.

The leader should hold:

- a lease-backed leadership key
- a periodically renewed session

If the lease expires:

- leadership is lost
- that instance must stop dispatching

### Why etcd

This is the most coherent choice with the current architecture because:

- etcd is already the natural home for distributed coordination
- leadership and lease semantics fit control-plane ownership
- it avoids inventing a separate coordination substrate

## Leadership State

At minimum, the leadership record should identify:

- leader instance ID
- lease/session identity
- leadership epoch or fencing token
- acquisition timestamp

This makes leadership observable and helps downstream systems reject stale work.

### Authority Token (v0.2+)

Use a formal authority token for dispatch-critical paths:

- `leader_id` (instance identity)
- `epoch` (monotonic fencing token)

Boolean `is_leader` checks are necessary but not sufficient for stale-leader safety.
The authority token must be attached to dispatch ownership decisions and durable
transition writes where possible.

## Fencing

Leader election alone is not enough.

You also need a fencing mechanism so that a stale leader cannot continue dispatching after losing authority.

### Required Rule

Every active leadership period must have a monotonically increasing token or epoch.

That token must be associated with dispatch authority.

### Purpose

Fencing protects against:

- delayed or partitioned former leaders
- stale processes that still believe they are primary
- duplicate dispatch caused by late network recovery

### Practical v0.2 Requirement

For v0.2, the minimum acceptable fencing model is:

- leader acquires a lease-backed leadership record
- leadership includes a unique epoch/token
- dispatch logic checks leadership before issuing work
- if leadership is lost, dispatch stops immediately

And additionally:

- leadership loss should cancel dispatch-critical contexts (DB/RPC) promptly
- stale dispatch attempts must fail via state-aware CAS/idempotency checks

Later versions may propagate the fencing token deeper into persisted task records or worker acceptance logic.

## Dispatch Ownership

Only the current leader owns dispatch.

This means:

- only the leader can transition tasks from schedulable to dispatching
- only the leader can issue `ExecuteOnNode` or equivalent remote execution calls

Standby instances must never race to dispatch the same work.

### Dispatch Invariant

Before dispatch, the control plane must have a clear answer to:

- who owns this task?
- under which leadership epoch?

If that answer is ambiguous, the system is not safe to dispatch.

### Dispatch Write Contract

Before worker RPC, persist a dispatch transition that includes:

- task identity
- expected previous state
- attempt
- leader token (`leader_id`, `epoch`)

Dispatch persistence should use atomic compare-and-swap semantics guarded by:

- expected prior state (for example `PENDING`)
- expected attempt/version
- fencing token validity

Idempotency keys (or equivalent uniqueness constraints) should be used so retry
writes cannot create duplicate dispatch ownership.

## Shared State Requirements

HA requires some state to survive process loss and be visible across instances.

### Minimum Shared State for v0.2

To support safe leadership and useful failover, the system should eventually persist:

- job metadata
- task-state transitions
- leadership ownership context for scheduling decisions

Without this, a new leader can acquire authority but still lack enough state to reason safely about previously queued or partially dispatched work.

### Important Boundary

This does not require full durable workflow replay in v0.2.

It does require enough durable state to answer:

- what jobs exist
- what state each task last reached
- which transitions were already recorded

### Transition Store as Source of Truth

For v0.2 HA, the transition store is the authoritative lifecycle source for:

- append-only job/task transitions
- materialized current job/task lifecycle views
- replay input for leader failover recovery

After failover, the new leader must rebuild dispatch visibility from durable
transition state before issuing new dispatches.

## Queue Ownership

The current in-memory queue model is incompatible with robust multi-instance HA if left unchanged.

### Current Risk

If the leader holds the only queue in memory:

- leader crash can lose queued work
- standby instances cannot take over with full visibility

### v0.2 Direction

The HA design should assume:

- queueing and dispatch ownership must eventually rely on shared durable state
- in-memory queues may still exist as local execution structures
- but they cannot be the only source of truth in a multi-control-plane future

This is why durability planning must accompany HA planning.

## Submission Behavior Under HA

There are two acceptable models.

### Model A: Leader-Only Submission

- only the leader accepts write submissions
- followers reject or redirect writes

Pros:

- simpler ownership model

Cons:

- less flexible ingress behavior

### Model B: Multi-Writer Submission, Single Dispatcher

- multiple API instances accept submissions
- submissions are persisted to shared state
- only the leader dispatches

Pros:

- better ingress availability

Cons:

- requires stronger shared-state discipline

### Recommended Direction

For long-term product viability, prefer:

- **multi-writer submission, single dispatcher**

But if v0.2 implementation complexity is too high, it is acceptable to start with:

- leader-only dispatch
- leader-preferred write path

as long as the behavior is explicit.

### v0.2 Decision (Current Runtime)

Current scheduler architecture uses local in-memory queue ownership per control-plane
instance. Under this model:

- follower write acceptance can strand work on follower-local queues
- multi-writer submission is not safe unless schedulable work is sourced from shared durable state

Therefore for v0.2 baseline:

- keep leader-aligned write ownership (leader-only or strict forward-to-leader)
- move to multi-writer only after shared queue/poller ownership is implemented

## Failure Scenarios

### Leader Crash

If the leader crashes:

- lease renewal stops
- lease expires
- leadership is lost
- a standby may acquire leadership

What remains ambiguous:

- tasks that were selected but not durably recorded
- tasks in flight during the crash

This ambiguity must be acknowledged in semantics docs.

### Network Partition

If the old leader is partitioned:

- it may temporarily believe it is still active
- lease/fencing must cause it to stop dispatching once authority is lost

This is the core reason fencing is required.

### Slow or Delayed Former Leader

If a former leader regains connectivity late:

- it must not resume dispatching under stale authority
- leadership checks must fail
- any stale local queue state must be treated as non-authoritative

### Graceful Leadership Handoff

On controlled shutdown (for example rolling deploy):

1. stop accepting new dispatch work
2. finish or persist in-flight dispatch ownership transitions
3. release leadership lease
4. confirm follower takeover window
5. exit dispatch loop

This reduces ambiguity versus crash-based takeover.

Current v0.2 implementation note:

- scheduler leader election stop path attempts explicit etcd resign
- resign timeout is configurable/derived and still degrades safely to lease-expiry fallback if resign fails

### HITL Pause/Resume Under HA

HITL safety requirements during failover:

- pause transitions (`AWAITING_HUMAN`) remain durable in transition store
- callback ingress remains leader-agnostic
- resume dispatch remains leader-gated
- resumed execution still enforces graph version pinning

After failover, the new leader must recognize paused workflows from durable
state and safely continue resume dispatch when callbacks arrive.

### Worker Reconnection During Leader Transition

Worker behavior expectations:

- workers retry control-plane connectivity with backoff
- workers refresh leader endpoint/authority before new dispatch coordination
- stale authority should not be accepted for new dispatch ownership
- capacity heartbeats should resume against the active leader

## Readiness and Health

In an HA system, readiness must reflect role.

### Suggested Semantics

- healthy: process is alive and internally functioning
- ready for reads: process can serve non-dispatch APIs
- ready for dispatch: process is current leader and has valid leadership state

This distinction matters because a follower may be healthy without being allowed to schedule.

## Observability

HA state transitions must be visible.

### Required Events / Logs

- leadership acquired
- leadership renewed
- leadership lost
- leader change detected
- dispatch blocked because instance is not leader
- alert webhook delivery failures (when configured)

### Required Metrics

- current leadership state
- leader changes total
- leadership loss events
- dispatch attempts blocked by non-leader state
- leadership handoff duration
- failover replay/rebuild duration

## Rollout Plan

### Phase 1: Design

- define leadership and fencing model
- define source of truth boundaries
- define readiness semantics

### Phase 2: Leadership Primitive

- implement leader election
- expose leadership status
- prevent non-leaders from dispatching
- add leadership-scoped cancellation for dispatch contexts on lease loss

### Phase 3: Durable Scheduling Alignment

- align job/task state persistence with failover needs
- ensure new leader can reason about schedulable work
- enforce dispatch CAS with state guards + fencing token usage

### Phase 3.5: Topology Expansion (Optional)

- keep leader-aligned writes as default in v0.2 baseline
- only enable multi-writer submission after shared durable schedulable-work
  ownership is implemented end-to-end

### Phase 4: Operational Hardening

- add metrics/logging
- validate failover behavior
- test partition and stale-leader scenarios

## Release Criteria

The control plane should only be described as HA-aware when:

1. There is a single active dispatcher by design
2. Leadership is explicitly elected
3. Stale leaders are fenced from dispatch
4. Role-aware readiness is defined
5. Failover semantics are documented

## Open Decisions

These decisions must be finalized before implementation:

1. Is v0.2 leader-only for writes, or only for dispatch?
2. Where is the authoritative schedulable task record stored?
3. What exact record is written before a task is dispatched?
4. How is a stale dispatch attempt detected and rejected?
5. What leader epoch or fencing token format is used?

## v0.2 Resolved Decisions

For the current implementation slice:

1. **Write topology**: leader-aligned writes until shared durable schedulable-work source exists.
2. **Authoritative lifecycle state**: Postgres transition store and durable materialized views.
3. **Pre-dispatch record**: persist dispatch transition (with attempt + leader token) before worker RPC.
4. **Stale dispatch rejection**: state-guarded CAS + idempotency constraints.
5. **Token format**: monotonic epoch with leader identity.
6. **Planned handoff behavior**: best-effort explicit resign on scheduler stop before lease-expiry fallback.
7. **Drill integrity scope**: run-scoped SQL checks (`DRILL_ID`, start timestamp) plus non-terminal durable task validation.

## Backpressure Under HA

Backpressure remains required under failover conditions:

- admission control still applies during leader transitions
- temporary failover windows may increase `429` responses
- clients should treat retries as normal during leadership change
- queue depth visibility may reset across leaders; durable transitions remain
  authoritative for lifecycle correctness

## Relationship To Other Docs

Read this alongside:

- `docs/BACKPRESSURE.md`
- `docs/SCHEDULING_POLICY.md`
- `docs/DURABILITY.md`
- `docs/FAILURE_SEMANTICS.md`
- `docs/RECOVERY_RUNBOOK.md`

Operational alerting and drill verification details are defined in
`docs/RECOVERY_RUNBOOK.md`.

Backpressure limits overload.
Scheduling policy defines task placement.
Durability defines what survives restart.
HA defines who is allowed to dispatch at all.
Recovery runbook defines operational drill and remediation steps.
