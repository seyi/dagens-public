# Discipline: What Dagens Will Not Become

## The Commitment

Dagens is not building features for adoption.

Dagens is building **correctness** for trust.

This document defines the boundaries we will not cross.

---

## The Core Principle

> **Control-plane purity over feature convenience.**

Every feature request is evaluated against one question:

> Does this strengthen control-plane / execution-plane separation?
> Or does it blur the boundary?

If it blurs the boundary → **no**.

---

## What the Control Plane Does

**Responsibilities:**

- Accept submitted graphs
- Compile to stages + tasks
- Push dispatch to healthy workers
- Track job lifecycle
- Record events for observability
- Enforce safety policies
- Manage HITL checkpoints

**Non-Responsibilities:**

- Execute agents
- Store agent state
- Manage agent dependencies
- Host business logic
- Become a platform

---

## Features We Will Reject

### 1. UI Workflow Editor

**Request:** "Can we add a drag-and-drop graph builder?"

**Decision:** No.

**Reasoning:**
Graphs are submitted programmatically. A UI editor implies graphs are configuration, not code. This blurs the line between control plane (orchestration) and client (graph definition).

**Alternative:**
Clients build graphs using SDKs (Python, Go, etc.). Control plane receives compiled graphs.

---

### 2. Webhooks as Triggers

**Request:** "Can we support webhook triggers for event-driven workflows?"

**Decision:** No.

**Reasoning:**
Webhooks imply event-driven orchestration (Temporal's model). Dagens uses explicit graph submission. Event-driven triggers blur the control plane boundary.

**Alternative:**
External services submit graphs via API. Use CI/CD, cron jobs, or external event processors to trigger submissions.

---

### 3. Cron Scheduling

**Request:** "Can we add built-in cron scheduling for recurring jobs?"

**Decision:** No.

**Reasoning:**
Scheduling is external to the control plane. Adding cron makes the control plane a scheduler, not an orchestrator.

**Alternative:**
Use system cron, Kubernetes CronJobs, or external schedulers to submit graphs via API.

---

### 4. Dynamic Graph Mutation

**Request:** "Can we modify graphs mid-execution?"

**Decision:** No.

**Reasoning:**
Graphs are compiled before submission. Mutation breaks determinism and checkpoint/resume guarantees. This violates the graph-as-job model.

**Alternative:**
Submit new graphs for new logic. Use HITL checkpoints for human-guided branching.

---

### 5. Built-in Vector Store for RAG

**Request:** "Can we add a vector database for agent RAG?"

**Decision:** No.

**Reasoning:**
Vector storage is an agent concern, not an orchestration concern. Adding it makes the control plane stateful.

**Alternative:**
Agents manage their own vector stores. Control plane orchestrates; agents execute.

---

### 6. Built-in Agent SDK

**Request:** "Can we provide a first-party agent framework?"

**Decision:** No.

**Reasoning:**
Agents are external. They're brought via A2A adapters or the Go `agent.Agent` interface. A built-in SDK implies the control plane owns agent semantics.

**Alternative:**
Use LangGraph, CrewAI, or custom Go agents. Control plane orchestrates; agents are interchangeable.

---

### 7. Workflow Versioning in Control Plane

**Request:** "Can the control plane manage graph versions?"

**Decision:** No.

**Reasoning:**
Versioning is a client concern. The control plane receives compiled graphs. Managing versions blurs the boundary.

**Alternative:**
Clients version graphs. Control plane validates graph version on HITL resumption (to prevent drift).

---

### 8. Agent Marketplace

**Request:** "Can we build a marketplace for discovering agents?"

**Decision:** No.

**Reasoning:**
Agents register themselves via etcd or A2A. A marketplace implies the control plane owns agent discovery.

**Alternative:**
Agents advertise capabilities at registration. Clients discover via registry API.

---

### 9. Visual Graph Debugger

**Request:** "Can we add a visual debugger for stepping through graphs?"

**Decision:** No.

**Reasoning:**
Debugging is a client concern. The control plane exposes traces via OTEL; debuggers consume them.

**Alternative:**
Build external debuggers using OTEL traces. Control plane provides observability; clients debug.

---

### 10. Business Logic Extensions

**Request:** "Can we add custom middleware for X business logic?"

**Decision:** No.

**Reasoning:**
Business logic belongs in agents, not the control plane. Middleware that executes logic blurs the boundary.

**Alternative:**
Implement business logic in agents. Control plane orchestrates; agents execute.

---

## Features We Will Accept

### Backpressure-Aware Dispatch

**Why Yes:**
Strengthens control plane intelligence. Prevents worker overload. Maintains separation (control plane decides when to dispatch, not what to execute).

---

### Least-Loaded Scheduling

**Why Yes:**
Strengthens dispatch semantics. Uses worker metrics (CPU, memory, queue depth) to make smarter routing decisions.

---

### Kubernetes Operator

**Why Yes:**
Extends control plane to K8s without blurring boundaries. Operator manages control plane deployment; control plane manages orchestration.

---

### Policy Engine Integration (OPA)

**Why Yes:**
Strengthens governance without executing logic. Policy engine validates graphs before submission; control plane enforces.

---

### Distributed Event Store Backend

**Why Yes:**
Strengthens observability. Event store is a backend concern, not a logic concern. Control plane records events; event store persists them.

---

### Multi-Tenant Isolation

**Why Yes:**
Strengthens security model. Session isolation, JWT authentication, and RBAC are control plane concerns (access control, not execution).

---

## The Test for Every Feature

Before accepting a feature, ask:

1. **Does this strengthen control-plane / execution-plane separation?**
   - Yes → Accept
   - No → Reject

2. **Does this make the control plane stateful?**
   - Yes → Reject (except for job tracking + HITL checkpoints)
   - No → Continue

3. **Does this embed business logic in the control plane?**
   - Yes → Reject
   - No → Continue

4. **Does this blur the boundary between orchestration and execution?**
   - Yes → Reject
   - No → Accept

---

## Why This Discipline Matters

### Adoption-Driven Projects

- Simplify too early
- Overpromise capabilities
- Cut correctness corners
- Become "good enough" toolkits

### Correctness-Driven Infrastructure

- Build trust slowly
- Attract serious users
- Compound over time
- Become **infrastructure**

---

## The Tradeoff

**Saying "no" means:**

- Slower initial adoption
- Fewer "quick win" users
- More scrutiny from serious engineers

**Saying "yes" to everything means:**

- Faster initial adoption
- More users (initially)
- Architectural drift
- Becoming "another orchestration toolkit"

---

## The Goal

Dagens is not building for:
- Quick adoption
- Hype-driven users
- Feature checklist buyers

Dagens is building for:
- Serious infrastructure teams
- Distributed systems engineers
- Long-term trust

---

## The Promise

We will not compromise control-plane purity for:
- Feature requests
- Adoption pressure
- Competitive FOMO
- Investor expectations

We will say **no** to features that:
- Blur control/execution boundaries
- Embed business logic
- Make the control plane stateful (beyond job tracking)
- Violate the graph-as-job model

---

## Acknowledgments

This discipline is inspired by:

- **Kubernetes** — Does one thing (container orchestration) and does it well. Doesn't become a PaaS.
- **etcd** — Doesn't add features. Maintains correctness guarantees.
- **Apache Spark** — DAG scheduler for data, not business logic execution.
- **Temporal** — Workflow-as-code, but doesn't embed business logic in the engine.

---

## Further Reading

- [Philosophy](PHILOSOPHY.md) — Architectural beliefs
- [Scaling and Dispatch Clarification](SCALING_AND_DISPATCH_CLARIFICATION.md) — Control plane model
- [Architecture Overview](../ARCHITECTURE.md) — System design

---

## Conclusion

Dagens is **infrastructure**.

Infrastructure doesn't compromise for adoption.
Infrastructure earns trust through correctness.

We will say **no**.

And that's the point.
