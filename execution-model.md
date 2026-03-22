# Execution Model

This document defines Dagens control-plane execution semantics for dispatch
ownership, durable transitions, replay, and failover handling.

## Scope

This is the current v0.2 operational model for scheduler behavior.

It covers:

- dispatch ownership boundary
- durable transition interpretation
- replay/recovery behavior
- leader-loss/failover reconciliation rules

It does not claim:

- exactly-once task execution
- globally ordered cross-job execution
- zero-failure business outcomes under failover

## Core Principle

The durable transition log is the authoritative ledger for control-plane
decisions.

Control-plane actions must be explainable from durable transitions:

- decision to queue
- decision to dispatch
- running/terminal outcomes

## Dispatch Ownership Boundary

In v0.2, Dagens reuses `TASK_DISPATCHED` as the durable claim event.

Implications:

- no durable claim event means no dispatch attempt is valid
- dispatch ownership is established only after durable claim append succeeds
- leader/fencing checks occur before and during claim acquisition

Current practical invariant:

- no claim, no dispatch

## Claim Metadata

Claim-relevant metadata is persisted in transition/task materialized state:

- task ID
- job ID
- attempt number
- node ID
- transition sequence ID
- transition timestamp (`occurred_at`)
- (provider-side) leader/epoch gating semantics

Future-ready extension:

- explicit persisted leader ID and epoch/fencing token per claim event

## Lifecycle Interpretation

Key task lifecycle for ownership:

- `PENDING` -> `DISPATCHED` -> `RUNNING` -> terminal

Key job lifecycle:

- `SUBMITTED` -> `QUEUED` -> `RUNNING` -> terminal
- `AWAITING_HUMAN` -> `RUNNING` is represented as `JOB_RESUMED`

Terminal states:

- job: `SUCCEEDED` / `FAILED` / `CANCELED`
- task: `SUCCEEDED` / `FAILED`

## Replay Semantics

Replay is deterministic and sequence-driven.

Rules:

- replay order is transition `sequence_id` order
- transition log is authoritative over timestamps
- malformed transition streams fail replay for safety

Visibility-first behavior:

- recovered state is reconstructed from durable transitions
- ambiguous in-flight work is not blindly redispatched

## Failover Semantics

Leader loss handling is safety-first:

- follower blocks dispatch while not leader
- new leader reconciles durable unfinished jobs
- stale in-flight uncertainty can be terminal-quarantined

Current reconciliation protections include:

- malformed replay quarantine
- orphan running detection (running job without in-flight task progress)
- stale in-flight timeout handling for `DISPATCHED`/`RUNNING` task states

## HA Drill Acceptance

Failover drill is considered successful when:

- all canaries reach terminal states before timeout
- duplicate dispatch claim check is clean
  (`TASK_DISPATCHED` uniqueness by `(task_id, attempt)`)

Terminal `FAILED` canary outcomes are acceptable for this gate if fencing and
continuity semantics hold.

## Deferred Formalization

Planned post-repeatability hardening:

- optional explicit `TASK_DISPATCH_CLAIMED` transition
- claim timeout/expiry transition semantics
- explicit reclaim transitions by current leader epoch
- stronger claim metadata normalization across stores
