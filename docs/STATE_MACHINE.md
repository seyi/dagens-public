# State Machine

This document defines the first durable control-plane state model for Dagens.

The goal is to make job and task lifecycle transitions:

- explicit
- replayable
- durable enough to recover the control-plane view after restart

This is intentionally a narrow first step. It does not claim full workflow durability, exactly-once execution, or automatic task redispatch.

## Why This Exists

Dagens already has a clearer scheduling model, backpressure semantics, and worker-capacity handling.

The next control-plane maturity step is to answer a more serious operational question:

- what survives when the API server restarts?

The first answer does not need to be "everything resumes automatically."

It does need to be:

- job metadata survives
- task lifecycle transitions survive
- the control plane can rebuild its view of unfinished work

That is the minimum viable durable control-plane spine.

## Scope

This document defines:

- explicit job states
- explicit task states
- legal transitions
- append-only transition events
- replay rules on API restart

This document does not define:

- full durable scheduler queue semantics
- automatic redispatch of uncertain in-flight work
- exactly-once execution
- HA scheduling/fencing
- a final storage backend choice

## Design Goals

The first state machine implementation should provide:

1. A small, explicit set of lifecycle states
2. Append-only transition recording
3. Replayable reconstruction of current control-plane state
4. Clear operator visibility into what happened
5. A stable basis for later durability and HA work

## Non-Goals

The first implementation should not attempt to provide:

- automatic recovery of all in-flight execution
- speculative reconciliation of worker-side reality after restart
- hidden retries during replay
- task replay without an explicit policy decision

If the control plane cannot safely infer execution state, it should recover visibility first and defer automatic action.

## Core Model

The durable model has two layers:

1. **Current state**
   - the latest known job/task status exposed by the control plane
2. **Transition log**
   - the append-only sequence of state changes used to reconstruct current state

The durable source of truth is the transition log.

Current state may be materialized in memory for fast scheduling and API reads, but it must be derivable from recorded transitions.

## Job State Machine

Jobs represent the control-plane lifecycle of a submitted unit of work.

### Job States

- `SUBMITTED`
  - request accepted by the API layer
- `QUEUED`
  - admitted into the scheduler queue
- `RUNNING`
  - at least one task in the job has started execution
- `AWAITING_HUMAN`
  - job is paused at a HITL boundary awaiting callback/resume
- `SUCCEEDED`
  - all required tasks completed successfully
- `FAILED`
  - the job reached a terminal failure condition
- `CANCELED`
  - the job was explicitly canceled before successful completion

### Legal Job Transitions

- `SUBMITTED -> QUEUED`
- `QUEUED -> RUNNING`
- `QUEUED -> FAILED`
- `QUEUED -> CANCELED`
- `RUNNING -> AWAITING_HUMAN`
- `AWAITING_HUMAN -> RUNNING`
- `AWAITING_HUMAN -> FAILED`
- `AWAITING_HUMAN -> CANCELED`
- `RUNNING -> SUCCEEDED`
- `RUNNING -> FAILED`
- `RUNNING -> CANCELED`

### Illegal Job Transitions

The control plane should reject or ignore invalid transitions such as:

- `SUCCEEDED -> RUNNING`
- `FAILED -> RUNNING`
- `CANCELED -> RUNNING`
- `SUBMITTED -> SUCCEEDED`

Terminal states are terminal:

- `SUCCEEDED`
- `FAILED`
- `CANCELED`

## Task State Machine

Tasks represent the execution lifecycle of individual schedulable work units inside a job.

### Task States

- `PENDING`
  - task exists but has not yet been dispatched
- `DISPATCHED`
  - control plane selected a worker and attempted remote execution
- `RUNNING`
  - worker accepted execution and the task is in progress
- `SUCCEEDED`
  - task completed successfully
- `FAILED`
  - task reached a terminal execution failure

Optional future states may include:

- `RETRYING`
- `TIMED_OUT`
- `CANCELED`

Those are intentionally deferred until the first durable model is in place.

### Legal Task Transitions

- `PENDING -> DISPATCHED`
- `PENDING -> FAILED`
- `DISPATCHED -> RUNNING`
- `DISPATCHED -> FAILED`
- `RUNNING -> SUCCEEDED`
- `RUNNING -> FAILED`

### Illegal Task Transitions

The control plane should reject or ignore invalid transitions such as:

- `SUCCEEDED -> RUNNING`
- `FAILED -> RUNNING`
- `PENDING -> SUCCEEDED`

Terminal task states are:

- `SUCCEEDED`
- `FAILED`

## Transition Log

The first durable model should persist transition events, not only a mutable "current status" row.

Each transition should be appended as an immutable record.

### Why Append-Only

Append-only transitions provide:

- a replay source for rebuilding state
- an audit trail for operators
- debugging context for "what happened?"
- a cleaner foundation for reconciliation later

### Minimum Transition Records

At minimum, record:

- `job_id`
- `task_id` (for task events)
- `sequence_id`
- transition type
- prior state
- new state
- timestamp

For non-initial transitions, `previous_state` must be present:

- JOB initial transition may omit `previous_state` only when `new_state=SUBMITTED`
- TASK initial transition may omit `previous_state` only when `new_state=PENDING`

`sequence_id` must be monotonic within the replay scope.

For the first implementation, Dagens should choose one of:

- a global monotonic sequence
- a per-job monotonic sequence

Either is acceptable, but replay order must be based on `sequence_id`, not wall-clock time.

Useful additional fields:

- `node_id`
- dispatch attempt number
- error summary
- graph or stage ID

### Example Transition Events

Examples:

- `JobSubmitted`
- `JobQueued`
- `JobRunning`
- `JobAwaitingHuman`
- `JobResumed`
- `JobSucceeded`
- `JobFailed`
- `JobCanceled`
- `TaskCreated`
- `TaskDispatched`
- `TaskRunning`
- `TaskSucceeded`
- `TaskFailed`

The first implementation does not need an elaborate event hierarchy. A small, explicit set is better than a large flexible schema that is hard to validate.

## Replay Ordering

Replay must be deterministic.

That means:

- timestamps are metadata
- `sequence_id` is the ordering source of truth

On restart, transitions should be replayed in `sequence_id` order within the chosen replay scope.

The first implementation should not depend on timestamp ordering because:

- clocks can drift
- events can arrive with transport delay
- wall-clock ordering is not a safe control-plane invariant

## Replay on API Restart

On API server startup, Dagens should:

1. Load transition events for unfinished jobs
2. Reconstruct the latest known job state
3. Reconstruct the latest known task state
4. Rebuild in-memory control-plane views used for API reads and operator visibility

### First Recovery Goal

The first recovery goal is:

- restore the control-plane view

This means:

- jobs remain visible after restart
- tasks remain visible after restart
- the system can tell which work was unfinished

### What Replay Should Not Do Yet

The first replay implementation should not automatically:

- redispatch tasks that were in `DISPATCHED`
- redispatch tasks that were in `RUNNING`
- assume worker-side work was lost
- assume worker-side work completed

Those states are ambiguous after a control-plane restart.

The safe first behavior is:

- recover them as visible unfinished or uncertain states in the control-plane view
- require an explicit later policy for reconciliation or manual/operator action

### Current Restart Behavior (v0.2 implementation)

Current scheduler startup replay behavior is intentionally visibility-first:

- reconstruct in-memory job/task visibility state from transition history
- by default, do not re-enqueue recovered jobs into the active scheduler queue
- do not automatically redispatch recovered tasks

This preserves safety under ambiguity and avoids accidental double execution.

Optional startup resume slice (config-gated):

- when `EnableResumeRecoveredQueuedJobs=true`, recovered jobs in `QUEUED` lifecycle state are re-enqueued
- recovered `QUEUED` jobs are resumed only when recovered task lifecycle state remains pending/unknown
- this does not alter ambiguity handling for `DISPATCHED`/`RUNNING` tasks; those remain visibility-only

Recovery execution model notes:

- replay is currently fail-fast for startup safety; a single invalid unfinished job transition stream fails the recovery run
- replay failures are wrapped with job context (`job_id`) to aid operator diagnosis
- scheduler startup recovery is time-bounded by `RecoveryTimeout` (default `5m`)
- recovery observability uses explicit statuses (`succeeded`, `failed`, `canceled`)

### Stuck and Uncertain Tasks

Tasks recovered after restart may be operationally uncertain when their last durable state was:

- `DISPATCHED`
- `RUNNING`

In the first implementation, Dagens should treat these as:

- visible
- unfinished
- not automatically redispatched

This means the first recovery model must support a manual resolution path.

Examples of acceptable first-step operator actions:

- force a task to `FAILED`
- cancel the parent job
- mark the task for later reconciliation tooling

Automatic reconciliation is deferred, but operationally stuck work must still be resolvable.

## Scheduling and Recovery Boundary

The first state-machine implementation is about durability of control-plane state, not full execution resumption.

That means:

- the scheduler may rebuild metadata after restart
- the scheduler does not yet need to automatically resume all unfinished work
- replayed state should be safe before it is "smart"

This keeps the first implementation honest and avoids overclaiming stronger guarantees than the runtime can currently provide.

## Minimum Durable Data For v0.2

The first implementation should durably persist:

1. Job metadata
   - job ID
   - creation timestamp
   - graph/workflow identifier if applicable
2. Job transition events
3. Task metadata
   - task ID
   - parent job ID
   - stage or node identity if applicable
4. Task transition events

This is enough to:

- recover lifecycle visibility
- answer operator questions after restart
- support future reconciliation logic

## What "Recovered" Means

For the first durability slice, "recovered" means:

- reconstructed in-memory state from durable transitions
- consistent API-visible state after restart
- unfinished work can be identified

It does not yet mean:

- work is automatically resumed
- in-flight work is automatically retried
- the scheduler can infer exactly what a worker completed while the control plane was down

## Job State Derivation

Job state is not independent of task state.

For the first implementation:

- task transitions establish the underlying execution facts
- job transitions reflect the control-plane aggregation of those facts

That means terminal job transitions such as:

- `JobSucceeded`
- `JobFailed`

should be recorded only after the relevant task transitions make that outcome true.

Example:

1. the final required `TaskSucceeded` is appended
2. the control plane derives that the job has now completed successfully
3. `JobSucceeded` is appended

This preserves causality in the transition log and makes replay deterministic.

## Operational Semantics

Once this lands, Dagens should be able to say:

- job and task lifecycle transitions are durably recorded
- the control plane can rebuild its view after restart
- ambiguous in-flight work is recovered for visibility first, not automatically re-executed

This is a materially stronger statement than the current in-memory-only scheduler path, and it is the right first durability step.

## Log Growth and Compaction

The first implementation may use straightforward append-only replay, but the model must acknowledge that the log will grow over time.

Future work should support:

- snapshots
- compaction
- replay from a checkpoint plus later transitions

This is not required for the first durability slice, but the transition format should not prevent it.

The first version should optimize for correctness and clarity before optimizing long-term replay performance.

## Relationship To Other Docs

- [`docs/DURABILITY.md`](DURABILITY.md)
  - describes what is durable today and what is not
- [`docs/FAILURE_SEMANTICS.md`](FAILURE_SEMANTICS.md)
  - describes current execution guarantees and ambiguity boundaries
- [`docs/BACKPRESSURE.md`](BACKPRESSURE.md)
  - describes saturation and scheduling behavior under load

This document is the bridge between:

- current best-effort scheduler state
- and a future durable control-plane model

## First Implementation Checklist

The first implementation should complete these steps:

1. Define job and task state enums in code
2. Validate legal transitions
3. Record append-only transition events
4. Persist transition events in a durable store
5. Define the `sequence_id` strategy used for deterministic replay
6. Replay unfinished jobs/tasks on API startup
7. Expose recovered state through existing API views
8. Provide a manual operator path to resolve stuck uncertain tasks

## Deferred Work

These are explicitly deferred until after the first state-machine implementation:

- automatic task reconciliation against worker reality
- durable waiting/requeue queues
- HA scheduler ownership built on the transition log
- exactly-once claims
- stronger execution recovery guarantees

Those are important later, but they should not be mixed into the first serious durability slice.
