# Getting Started with Dagens

This guide walks through the current recommended ways to run Dagens.

Dagens is a Go-based control-plane runtime for distributed agent and service orchestration.

## Prerequisites

- Go 1.21 or later
- Docker and Docker Compose
- Git

## Recommended: Full Stack via Docker Compose

This is the primary entry mode for evaluating the runtime.

```bash
git clone https://github.com/seyi/dagens
cd dagens

docker compose up --build
```

This starts:

- `api-server` on `:8080`
- `worker-1`
- `worker-2`
- `postgres`
- `jaeger`

Verify the control plane:

```bash
curl http://localhost:8080/health
```

## 5-Minute Local Smoke Test

For a fast local API smoke test:

```bash
go run ./cmd/agent-server --addr :8080
```

Then verify:

```bash
curl http://localhost:8080/health
curl http://localhost:8080/api/v1/agents
```

## First API Demo

With the Compose stack running, submit a simple job:

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

Then inspect jobs:

```bash
curl http://localhost:8080/v1/jobs
```

## Scale Modes

### Static Mode

Without `ETCD_ENDPOINTS`, Dagens runs with a fixed worker topology.

This is useful for:

- local development
- stable test clusters
- fixed production deployments

### Cluster-Aware Mode

With `ETCD_ENDPOINTS` configured, Dagens uses an etcd-backed distributed registry.

This enables:

- dynamic worker registration
- health-filtered node views
- better handling of node churn

## Important Notes

- The scheduler is push-based. Workers do not poll for jobs.
- Durability is limited in v0.1.x and should be reviewed before production use.
- Metrics currently use the legacy `spark_agent_*` namespace and will be renamed in a future release.

For deeper details, read:

- `docs/DURABILITY.md`
- `docs/FAILURE_SEMANTICS.md`
- `docs/SCALING_AND_DISPATCH_CLARIFICATION.md`
- `ARCHITECTURE.md`
