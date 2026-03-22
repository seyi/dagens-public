# Dagens vs Kubernetes

Dagens and Kubernetes solve different layers of the problem.

Kubernetes is an infrastructure control plane for placing and operating processes.
Dagens is an execution control plane for durable distributed work.

They are complementary, not substitutes.

## Short Version

Kubernetes answers:
- where should this process run?
- is the process healthy?
- how should it be rolled out or restarted?
- how do I expose it on the network?

Dagens answers:
- what work exists?
- what state is that work in?
- who is allowed to dispatch it?
- what survives leader loss?
- how is state replayed and recovered?
- how do HITL pause/resume semantics behave across failover?
- how do we prevent duplicate execution across control-plane transitions?

## What Dagens Is Actually About

Dagens is a durable execution control plane for distributed and human-in-the-loop workloads.

Its core thesis is that execution semantics should be explicit and durable:
- jobs and tasks move through an explicit state machine
- control-plane decisions are recorded in a durable transition log
- recovery is based on replay and reconciliation rather than restart-and-hope
- leader failover is part of the runtime model, not an afterthought
- workers participate in fencing and stale-authority rejection
- HITL checkpoint and resume semantics remain valid through failover

This is why Dagens is not just a job API in front of worker processes.
It is trying to make distributed execution behavior correct, inspectable, and recoverable.

## What Kubernetes Already Does Well

Kubernetes already provides:
- pod scheduling onto nodes
- service discovery and networking
- liveness/readiness/startup probing
- deployment rollout and rollback
- scaling primitives
- storage primitives
- leader-election building blocks
- strong cluster operational tooling

Dagens should use these capabilities where they help.

Running Dagens on Kubernetes is a good deployment model.
But Kubernetes by itself does not provide Dagens' execution semantics.

## What Kubernetes Does Not Give You By Default

Kubernetes does not natively give you:
- durable workflow/task lifecycle state machines
- execution-level replay and reconciliation semantics
- dispatch ownership and fencing across control-plane failover
- workload-specific duplicate dispatch prevention
- graph execution pause/resume semantics
- HITL checkpoint/resume behavior
- durable transition-log-based recovery for scheduler decisions

You can build those things on top of Kubernetes.
That is exactly the layer Dagens is targeting.

## The Clean Boundary

### Kubernetes layer

Use Kubernetes for:
- process placement
- pod lifecycle
- networking and service exposure
- deployment management
- autoscaling integration
- storage and secret integration

### Dagens layer

Use Dagens for:
- job/task state management
- dispatch policy and ownership
- transition logging and replay
- failover-safe recovery of durable work
- worker-facing fencing validation
- HITL pause/resume semantics
- operational drillability of execution behavior

## Example: Leader Failover

Kubernetes can restart an API pod after failure.
That is useful but insufficient.

The harder questions are:
- did the old leader already claim dispatch?
- can a new leader safely continue queued durable work?
- how do workers reject stale authority?
- how do we replay execution state without double-dispatch?
- what happens to a job paused in `AWAITING_HUMAN` during takeover?

Those are Dagens concerns.

## Example: Human-in-the-Loop Workflows

Kubernetes can keep the callback service alive.
It does not define:
- how a human checkpoint is persisted
- how callback idempotency is enforced
- how resume work is enqueued
- how duplicate callbacks are suppressed
- how resume behaves under control-plane failover

That is execution runtime behavior, not container orchestration.

## Recommended Mental Model

The right mental model is:
- Kubernetes is the infrastructure substrate
- Dagens is the durable execution substrate running on top

In a production deployment, a common shape is:
- Dagens API servers run as pods
- Dagens workers run as pods
- Postgres/Redis/etcd run in-cluster or as managed services
- Kubernetes keeps the platform healthy
- Dagens keeps the work semantics correct

## Why This Distinction Matters

If this boundary is not explicit, Dagens can be misunderstood as:
- a thin job queue
- a container scheduler clone
- an unnecessary layer over Kubernetes

That is incorrect.

The technical value of Dagens is not that it replaces Kubernetes.
The value is that it gives durable, replayable, failover-aware execution semantics to distributed work.

## Bottom Line

Yes, Dagens can run on Kubernetes.

No, Kubernetes alone is not the same thing as Dagens.

Kubernetes manages compute placement and service lifecycle.
Dagens manages durable execution semantics, dispatch ownership, replay, recovery, and HITL correctness.
