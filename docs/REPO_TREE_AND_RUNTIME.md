# Dagens Repo Tree and Runtime Snapshot

This document summarizes:

1. Top-level repository folders
2. How the project is run today (single binary vs multiple services)
3. Environment variables currently used by runtime binaries

## Top-Level Folders

Top-level directories in this repo:

- `.github` - CI/CD workflows and repo automation
- `bin` - built binaries and CLI wrappers
- `cmd` - executable entrypoints (`api_server`, `worker`, `agent-server`, demos/loadtest)
- `deploy` - deployment assets (helm, kubernetes, agent env)
- `deployments` - additional deployment stacks/configs (e.g., loadtest)
- `docs` - architecture, implementation, and operational docs
- `examples` - runnable examples and demos
- `pkg` - core framework packages (graph, agent, a2a, hitl, scheduler, etc.)
- `python` - Python package and examples
- `sdk` - SDKs (including Python SDK variants)
- `tests` - end-to-end and load tests
- `vendor` - vendored Go dependencies
- `web` - dashboard/web UI assets
- hidden local tooling/state directories may also exist in private working copies, but they are not part of the public runtime layout
- `github.com`, `quint-code-modified`, `~` - imported/experimental or auxiliary trees in this workspace

## Execution Model: How It Runs Today

There is no single canonical production binary for everything. Current operation is primarily **multi-service**:

1. `cmd/api_server/main.go` - control-plane HTTP API + scheduler
2. `cmd/worker/main.go` - worker gRPC execution service (scale horizontally)
3. Optional infra services - Jaeger, Postgres, etcd (depending on mode)

Also available: a **single-binary demo server**:

1. `cmd/agent-server/main.go` - standalone HTTP agent server with built-in sample agents (`echo`, `summarizer`, `classifier`, `sentiment`)

## Run Modes

### Mode A: Multi-Service via Docker Compose (current easiest full stack)

Uses `docker-compose.yml`:

- `api-server` (port `8080`)
- `worker-1` and `worker-2` (gRPC workers)
- `jaeger` (OTEL backend)
- `postgres`

Command:

```bash
docker compose up --build
```

Notes:

- In this compose setup, `api_server` uses its built-in mock registry entries (`worker-1`, `worker-2`) on the Docker network.
- Workers call OpenRouter only if `OPENROUTER_API_KEY` is provided; otherwise they simulate execution.

### Mode B: Standalone Demo Server (single process)

Command:

```bash
go run ./cmd/agent-server --addr :8080
```

Then call:

- `GET /health`
- `GET /api/v1/agents`
- `POST /api/v1/agents/execute`

This mode is best for quick local API smoke tests.

### Mode C: Manual Multi-Process (advanced)

Start workers and API separately:

```bash
# terminal 1
NODE_ID=worker-1 PORT=50051 go run ./cmd/worker

# terminal 2
NODE_ID=worker-2 PORT=50052 go run ./cmd/worker

# terminal 3
go run ./cmd/api_server
```

Important:

- Without etcd, `api_server` expects `worker-1`/`worker-2` hostnames (compose-friendly); manual host networking may require additional setup.
- With etcd enabled, workers/API register/discover dynamically (requires `ETCD_ENDPOINTS` and correct node addressing via `POD_IP` or equivalent).

## Environment Variables

### Common Observability

- `OTEL_EXPORTER_TYPE` - `console|otlp|noop`
- `OTEL_EXPORTER_OTLP_ENDPOINT` - OTLP endpoint (enables OTEL tracer path)
- `OTEL_SERVICE_NAME` - service label (used in compose env)

### API Server (`cmd/api_server`)

- `ETCD_ENDPOINTS` - optional; enables distributed registry mode
- `POD_IP` - node address when using distributed registry
- `JWT_SECRET` - auth signing key (falls back to dev default if absent)
- `VAULT_ADDR` - optional Vault endpoint
- `VAULT_TOKEN` - optional Vault token

### Worker (`cmd/worker`)

- `NODE_ID` - worker identity (`worker-<timestamp>` default)
- `PORT` - gRPC port (`50051` default)
- `ETCD_ENDPOINTS` - optional; distributed registration/discovery
- `POD_IP` - worker address for registry in distributed mode
- `OPENROUTER_API_KEY` - optional; if missing, worker simulates output
- `VAULT_ADDR` - optional Vault endpoint
- `VAULT_TOKEN` - optional Vault token

### Agent Server (`cmd/agent-server`)

- No required env vars for basic use
- Uses `--addr` CLI flag (default `:8080`)

## Practical Recommendation

For a real quickstart today:

1. Start with Mode B for first API/demo success.
2. Move to Mode A for distributed behavior and tracing.
3. Use Mode C only when you need custom process-level debugging.
