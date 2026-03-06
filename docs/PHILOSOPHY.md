# Dagens Philosophy

## Why This Project Exists

Dagens is not a workflow engine with AI adapters.

It is not an agent framework with distributed execution.

It is **infrastructure for orchestrating agents and services across explicit control and execution planes**.

This document explains why that distinction matters.

---

## Core Beliefs

### 1. AI Orchestration Is a Distributed Systems Problem

Most agent frameworks treat orchestration as an in-process concern:

```python
# In-process graph execution
graph = StateGraph(...)
result = graph.run(input)
```

This works for single-machine, single-language workflows.

It breaks when you need:

- Horizontal scaling
- Multi-language interoperability
- Human-in-the-loop as a primitive
- Failure-aware dispatch
- Observability across service boundaries

Dagens starts from a different assumption:

> **Orchestration requires explicit control-plane / execution-plane separation.**

This is not a workflow engine pattern.
This is **distributed systems** pattern.

---

### 2. Humans Are Not Signals — They Are Graph Nodes

In workflow engines, human approval is typically:

```python
# Temporal-style: human as external signal
await workflow.sleep_until_approval()
@workflow.signal
def approve():
    ...
```

The human is an external event. A signal. A callback.

In Dagens, the human is a **first-class graph primitive**:

```
StartNode → HumanNode → ResumeNode → EndNode
```

The human is not an afterthought.
The human is part of the graph topology.

This matters because:

- Graph version is validated on resumption
- Callback signature is verified (idempotent, secure)
- Checkpoint state is explicit, not implicit
- Resumption is deterministic, not event-driven

**Philosophy:** Humans aren't interrupts. They're participants.

---

### 3. Agents Are Not Activities — They Are Routable Services

In workflow engines, activities are:

- Functions you implement
- Registered with the engine
- Polled by workers
- Stateless (state is in the workflow)

In Dagens, agents are:

- Services with capabilities
- Self-registering via etcd (lease-based)
- Dispatched to via push scheduling
- Stateful (agent state can be checkpointed)

**Philosophy:** Agents aren't functions to call. They're services to route to.

---

### 4. Graphs Are Submitted Jobs — Not Durable Functions

In workflow-as-code (Temporal, etc.):

```python
@workflow.defn
class MyWorkflow:
    @workflow.run
    async def run(self, input: str) -> str:
        ...
```

The workflow **is** code. You maintain it. You deploy it.

In Dagens:

```python
g = Graph("Haiku Generator")
g.add_node(poet)
job_id = client.submit(g, instruction="Generate poem")
```

The graph is a **submitted job**. It's compiled, scheduled, and executed.

**Philosophy:** Orchestration isn't writing durable functions. It's submitting graphs to a control plane.

---

### 5. Dispatch Is Push — Not Poll

In workflow engines:

```
Worker → polls → Task Queue → executes → Activity
```

Workers poll for work.

In Dagens:

```
Control Plane → selects → Healthy Worker → pushes → Agent
```

The control plane selects and dispatches.

**Why push?**

- Health-aware routing (don't dispatch to unhealthy nodes)
- Locality-aware scheduling (stick to nodes with cached state)
- Backpressure control (pause dispatch when workers are overloaded)
- Failure detection (lease expiry = automatic deregistration)

**Philosophy:** Polling is for stateless tasks. Push is for stateful services.

---

## What This Means in Practice

### Control Plane / Execution Plane Separation

```
┌─────────────────────────────────────┐
│         Control Plane               │
│  - API Server                       │
│  - Graph Compiler                   │
│  - Push Scheduler                   │
│  - Registry (etcd-backed)           │
└─────────────────────────────────────┘
                  │
                  │ gRPC (push dispatch)
                  ▼
┌─────────────────────────────────────┐
│       Execution Plane               │
│  - Worker 1 (agent executor)        │
│  - Worker 2 (agent executor)        │
│  - Worker N (horizontal scale)      │
└─────────────────────────────────────┘
```

This is not embedded orchestration.
This is **infrastructure**.

---

### Human-in-the-Loop as Primitive

```
Graph Execution:
  1. Execute StartNode
  2. Reach HumanNode → checkpoint
  3. Return control to caller (pending state)
  4. Human approves via callback (signature validated)
  5. Resumption worker reloads checkpoint
  6. Graph version validated (no drift)
  7. Execute ResumeNode
  8. Complete
```

The human is not a signal.
The human is a **checkpoint boundary**.

---

### Multi-Language Interoperability

```
┌──────────────────────────────────────────────┐
│              Dagens Control Plane            │
└──────────────────────────────────────────────┘
         │              │              │
         ▼              ▼              ▼
   ┌──────────┐  ┌──────────┐  ┌──────────┐
   │   Go     │  │  Python  │  │   Rust   │
   │  Agent   │  │ LangGraph│  │  Agent   │
   │ (native) │  │  A2A     │  │  A2A     │
   └──────────┘  └──────────┘  └──────────┘
```

Agents aren't bound to the orchestration language.
Agents are **routable services** with capability advertisement.

---

## Comparison to Existing Tools

### Temporal

**Temporal Philosophy:**
> Workflows are durable functions. State is automatically checkpointed. Workers poll for tasks. The engine manages everything.

**Dagens Philosophy:**
> Orchestration is explicit control-plane / execution-plane separation. Graphs are submitted jobs. Dispatch is push-based. Humans are first-class primitives.

**Different starting assumptions → different architectures.**

| Concern | Temporal | Dagens |
|---------|----------|--------|
| Model | Workflow-as-code | Graph-as-job |
| Dispatch | Workers poll | Control plane pushes |
| Control Plane | Embedded in engine | Explicit separation |
| HITL | Manual signals + sleep | First-class graph node |
| Registration | Activity types | etcd lease + capabilities |
| Interoperability | Language SDKs | A2A protocol adapters |

**When to use Temporal:**
- Business process workflows (order fulfillment, onboarding)
- Durable function execution
- Single-language ecosystems

**When to use Dagens:**
- Agent/service orchestration
- Multi-language interoperability
- Human-in-the-loop as primitive
- Explicit control/execution separation

---

### LangGraph

**LangGraph Philosophy:**
> Graphs are executed in-process. State is managed by the graph runtime.

**Dagens Philosophy:**
> Graphs are submitted to a control plane. Execution is distributed across workers.

**Different scope → different tool.**

LangGraph is an **in-process graph executor**.
Dagens is a **distributed orchestration runtime** that can execute LangGraph agents via A2A adapters.

---

### CrewAI

**CrewAI Philosophy:**
> Agents coordinate in a single process with defined roles and tasks.

**Dagens Philosophy:**
> Agents are routable services across a distributed worker fabric.

**Different scale → different tool.**

CrewAI is a **single-process agent framework**.
Dagens provides **distributed scheduling, health-aware dispatch, and HITL primitives** at the runtime layer.

---

## What Dagens Is Not

Dagens is not:

- ❌ A workflow engine (Temporal)
- ❌ An in-process graph executor (LangGraph)
- ❌ An agent framework (CrewAI)
- ❌ A task queue (Celery)
- ❌ A service mesh (Istio)

Dagens is:

- ✅ A **control-plane runtime** for agent/service orchestration
- ✅ An **execution-plane fabric** with horizontal scaling
- ✅ A **HITL primitive** with checkpoint/resume semantics
- ✅ An **interoperability layer** via A2A protocol adapters

---

## The Deeper Conviction

> **AI orchestration isn't a workflow problem. It's a distributed systems problem.**

This belief shapes everything:

- Control/execution separation (not embedded engine)
- Push dispatch (not worker polling)
- Humans as graph nodes (not external signals)
- Agents as routable services (not activities)
- Graphs as submitted jobs (not durable functions)

This is not incremental improvement.
This is **different category**.

---

## Why This Matters

The AI infrastructure landscape is full of:

- Workflow engines with AI adapters
- Agent frameworks with distributed execution
- Task queues with LLM integrations

These are all **workflow-first** or **agent-first** solutions.

Dagens is **infrastructure-first**.

We don't start from "How do we make agents distributed?"
We start from "How do we build orchestration infrastructure?"

That difference in starting point → difference in architecture.

---

## The Goal

Dagens exists to provide:

1. **Explicit control-plane / execution-plane separation**
2. **Health-aware, push-based dispatch**
3. **Human-in-the-loop as first-class primitive**
4. **Multi-language interoperability via A2A**
5. **Observability-first design (OTEL-native)**
6. **Enterprise-grade policy, audit, and security**

This is not "AI orchestration."
This is **distributed systems for AI workloads**.

---

## Acknowledgments

This philosophy was shaped by:

- Apache Spark's DAG scheduler model
- Kubernetes' control-plane / worker separation
- Etcd's lease-based health detection
- Actor model's message-passing concurrency
- Distributed systems literature (consensus, barriers, leader election)

Dagens stands on the shoulders of distributed systems giants.

We apply those patterns to AI orchestration.

---

## Further Reading

- [Scaling and Dispatch Clarification](SCALING_AND_DISPATCH_CLARIFICATION.md)
- [Repo Tree and Runtime Snapshot](REPO_TREE_AND_RUNTIME.md)
- [Architecture Overview](../ARCHITECTURE.md)
