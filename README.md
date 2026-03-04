# Dagens

**Programmable control-plane runtime for distributed agent and service orchestration.**

Dagens is a distributed control plane with a dedicated execution-plane worker fabric, built in Go. It separates scheduling from execution, supports horizontal worker scaling, and includes first-class human-in-the-loop (HITL) checkpointing and resumability.

Designed as enterprise infrastructure with an OSS core, Dagens runs in fixed-topology mode or becomes cluster-aware via etcd-backed registry for dynamic scaling and health-based routing.

## Why Dagens?

Most agent frameworks are in-process orchestration layers. Dagens is a runtime substrate:

- Central push scheduler (control plane)
- Horizontally scalable gRPC workers (execution plane)
- Optional dynamic cluster awareness via etcd
- Evented lifecycle model with observability (OTEL-native)
- Human-in-the-loop pause/resume as a first-class primitive
- Resilience wrappers (retry, monitoring, circuit-breaker)

This is infrastructure, not an in-process agent library. It treats orchestration as a distributed systems problem, not a local graph execution concern.

Control-plane saturation model (v0.2):
- [Backpressure Design](docs/BACKPRESSURE.md)

## Integrations (Bring Your Own Agent Framework)

Dagens supports A2A adapters that allow external agent frameworks to participate in distributed orchestration.

Currently supported:

- **LangGraph** - Wrap a `StateGraph` or `CompiledGraph` as an A2A agent
- **CrewAI** - Expose a `Crew` as a distributed agent
- More adapters planned

Example (LangGraph):

```python
from langgraph.graph import StateGraph, END
from dagens_a2a import A2AServer
from dagens_a2a.adapters import LangGraphAdapter

graph = StateGraph(...)
graph.set_entry_point("process")

adapter = LangGraphAdapter(
    graph=graph,
    agent_id="processor",
    name="Text Processor"
)

server = A2AServer(adapter, port=8082)
server.run()
```

This allows Dagens to orchestrate Go-native agents and Python agents in the same workflow.

## Architecture Overview

Dagens uses a layered model.

```text
Client -> API Server (Control Plane)
             |
             v
      Push Scheduler
             |
             v
      gRPC Workers
             |
             v
   Event Bus + OTEL
             |
             v
   Optional HITL Checkpoint
```

### Control Plane

- API server
- Graph compiler
- Scheduler (push-based dispatch)
- Registry (static or etcd-backed)
- Auth plus optional Vault integration

### Execution Plane

- gRPC workers
- Horizontal scaling
- Transfer/autoflow semantics
- Optional agent-to-agent (A2A) streaming

### System Services

- Event bus plus event recorder
- OTEL tracing integration
- Postgres (Compose mode)
- Jaeger for tracing

### HITL Control Plane

- HumanNode checkpoint
- Callback validation (idempotent)
- Resumption worker
- Graph version validation

## Quickstart (Full Stack - Recommended)

Primary entry mode: Docker Compose cluster.

```bash
git clone https://github.com/seyi/dagens
cd dagens

# Copy environment template and add your API key (optional, for real AI execution)
cp .env.example .env
# Edit .env and add your OPENROUTER_API_KEY from https://openrouter.ai/

docker compose up --build
```

Services started:

- `api-server` (HTTP control plane on `:8080`)
- `worker-1` and `worker-2` (gRPC execution nodes)
- `postgres`
- `jaeger` (OTEL backend)

Verify:

```bash
curl http://localhost:8080/health
```

If `OPENROUTER_API_KEY` is not provided, workers simulate execution for development and testing.

You now have:

- Push scheduler
- Multi-node execution
- Tracing pipeline
- Fixed-topology cluster

### Development Mode (No Authentication)

For local development and testing, you can disable authentication:

```bash
DEV_MODE=true
```

In docker-compose, this is enabled by default. For production, set `DEV_MODE=false` or omit the variable.

## Python SDK (Run a Distributed Job in 2 Minutes)

Once the Compose stack is running:

```bash
pip install dagens
```

Create `haiku.py`:

```python
from dagens import Graph, Agent, DagensClient

client = DagensClient(endpoint="http://localhost:8080")

poet = Agent(
    name="deepseek-poet",
    instruction="Write a haiku about a distributed system that never fails.",
    model="deepseek/deepseek-chat",
)

g = Graph("Haiku Generator")
g.add_node(poet)
g.set_entry(poet)
g.add_finish(poet)

job_id = client.submit(g, instruction="Generate poem")
print("Job:", job_id)
```

Then poll:

```python
status = client.get_status(job_id)
print(status)
```

Note: If `OPENROUTER_API_KEY` is not set, workers simulate responses.

## First Demo (Control Plane Execution)

Submit a simple graph execution request to the API server:

```bash
curl -X POST http://localhost:8080/v1/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hello-graph",
    "nodes": [
      {"id": "start", "type": "function", "name": "Start"},
      {"id": "end", "type": "function", "name": "End"}
    ],
    "edges": [{"from": "start", "to": "end"}],
    "entry_node": "start",
    "finish_nodes": ["end"],
    "input": {"instruction": "Hello from Dagens"}
  }'
```

List all jobs:

```bash
curl http://localhost:8080/v1/jobs
```

Get specific job status:

```bash
curl http://localhost:8080/v1/jobs/<job-id>
```

## 5-Minute Local Smoke Test (Single Binary)

For quick validation:

```bash
go run ./cmd/agent-server --addr :8080
```

Then call:

- `GET /health` - Health check
- `GET /api/v1/agents` - List available agents
- `POST /api/v1/agents/execute` - Execute an agent

Example:
```bash
curl -X POST http://localhost:8080/api/v1/agents/execute \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "echo",
    "input": "Hello World"
  }'
```

This mode is ideal for:

- API exploration
- SDK testing
- CI smoke validation

## Scaling Modes

### Static Mode (No etcd)

- Fixed worker topology
- Manual/static registry
- Suitable for stable deployments
- Can run indefinitely

### Cluster-Aware Mode (etcd enabled)

- Dynamic worker registration
- Health-filtered node view
- Cluster watch plus session lifecycle
- Failure-aware dispatch
- Elastic scaling support

Enable by setting:

```bash
ETCD_ENDPOINTS=<your-endpoints>
```

### Agent Registration Semantics

How agents become routable in the cluster:

**Worker Self-Registration** (etcd mode):
- Workers register on startup via etcd session
- Lease-based health (TTL expiry = automatic deregistration)
- Capabilities advertised at registration time

**Static Registration** (no etcd):
- Predefined in `MockRegistry` at API server startup
- Suitable for fixed topologies and testing

**A2A Agent Registration**:
- External frameworks register via A2A server startup
- Server advertises agent capabilities to registry
- Supports LangGraph, CrewAI, and custom adapters

**Programmatic Registration** (Go SDK):
- Use `agent.ExecutorFunc` to define inline agents
- Register via `server.RegisterAgent()` in single-binary mode
- Agents become immediately routable

## Developer Experience

- Full distributed cluster (Docker Compose)
- Single binary local server
- Python SDK for graph submission
- A2A protocol for cross-language agent integration

Agents can be created in a few lines of Go using `agent.ExecutorFunc`, while distributed orchestration remains explicit and production-oriented.

## Scheduling Model

Dispatch is push-based.

1. Jobs are submitted to the scheduler queue.
2. Scheduler selects a healthy node.
3. Execution is dispatched via gRPC.
4. Worker executes and returns result.
5. Events are recorded via event bus.
6. Optional HITL pause/resume is supported mid-graph.

Workers do not poll for jobs.

## Human-in-the-Loop (HITL)

Dagens treats human approval as a stateful checkpoint:

1. Graph execution reaches `HumanNode`.
2. Runtime returns pending/checkpoint signal.
3. Callback handler validates signature plus idempotency.
4. Resumption worker reloads checkpoint.
5. Graph version is validated.
6. Execution resumes deterministically.

This enables enterprise approval workflows without losing execution state.

## Observability

Built-in support for:

- OTEL exporters (`console`, `otlp`, `noop`)
- Jaeger tracing (Compose mode)
- Event lifecycle recording
- Execution telemetry

Set:

```bash
OTEL_EXPORTER_TYPE=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=jaeger:4317
```

## Project Structure

- `cmd/` - runtime entrypoints (`api_server`, `worker`, `agent-server`)
- `pkg/` - core runtime (`graph`, `agent`, `a2a`, `hitl`, `scheduler`, `registry`)
- `sdk/` - SDKs (including Python)
- `examples/` - runnable demos
- `deploy/` - Helm/Kubernetes configs
- `tests/` - e2e and load tests
- `web/` - dashboard UI

## Enterprise Orientation

Dagens is built with production constraints in mind:

**Policy & Governance**:
- Safety policy enforcement with structured violation errors
- Callback signature validation for HITL
- Graph version validation on resumption

**Auditability**:
- Full OTEL tracing across job lifecycle
- Event recording for execution history
- Lineage tracking for dependency chains

**Multi-Tenancy**:
- Session-based isolation
- JWT authentication with configurable providers
- Vault integration for secrets management

**Security**:
- Lease-based health for automatic failure detection
- Session lifecycle management via etcd
- Health-filtered routing prevents dispatch to unhealthy nodes

The OSS core provides orchestration primitives. Enterprise layers can extend this with billing, advanced RBAC, and compliance reporting.

## Comparison

**Temporal**
Temporal is workflow-as-code: workflows are implemented as durable functions maintained in application code, and workers poll for tasks.
Dagens is graph-based orchestration with explicit control-plane / execution-plane separation and push-based dispatch.
Temporal focuses on durable business process workflows. Dagens focuses on distributed agent and service orchestration with multi-language interoperability and first-class checkpoint/resume semantics.

**LangGraph**
LangGraph is an in-process graph execution framework.
Dagens is a distributed orchestration runtime that can execute LangGraph-based agents via A2A adapters.

**CrewAI**
CrewAI is a single-process agent coordination framework.
Dagens provides distributed scheduling, health-aware dispatch, and human-in-the-loop primitives at the runtime layer.

See [Philosophy](docs/PHILOSOPHY.md) for a deeper discussion of architectural differences.

## Roadmap

- Advanced scheduling strategies (least-loaded, weighted routing)
- Backpressure-aware dispatch
- Pluggable persistence layer abstraction
- Kubernetes-native operator
- Multi-tenant isolation model
- Distributed event store backend

## Additional Docs

- [Philosophy](docs/PHILOSOPHY.md) - Architectural beliefs and comparisons
- [Discipline](docs/DISCIPLINE.md) - What Dagens will not become
- [Scaling and Dispatch Clarification](docs/SCALING_AND_DISPATCH_CLARIFICATION.md)
- [Repo Tree and Runtime Snapshot](docs/REPO_TREE_AND_RUNTIME.md)

## License

See the repository license file.
