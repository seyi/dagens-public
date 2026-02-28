# Research Swarm Demo

This demo now uses internal `pkg/agents` LLM agents coordinated by `DistributedBarrier`.

## Flow

1. `academic-agent`, `github-agent`, and `blog-agent` run research in parallel via internal `LlmAgent`.
2. Each agent registers with `barrier.Wait()` via `coordination.WaitForSwarm`.
3. When all participants join, the barrier trips and all agents synthesize shared findings.

## LLM Configuration

The demo supports real LLM calls using project providers:

- `OPENAI_API_KEY` with model `RESEARCH_SWARM_MODEL` (default `gpt-4o-mini`)
- `OPENROUTER_API_KEY` with model `RESEARCH_SWARM_MODEL` (default `qwen/qwen3.5-plus-02-15`)

If no key is set, demo uses a deterministic mock provider unless:

- `RESEARCH_SWARM_REQUIRE_LLM=true` (then startup fails without a key)

## Run Locally (Docker Compose)

From repo root:

```bash
docker compose -f examples/research_swarm/docker-compose.yml up --build
```

This compose file loads `../../.env` and passes `OPENROUTER_API_KEY` into the demo container.

## Observe Barrier Keys

In a second terminal:

```bash
# all keys for this demo
docker compose -f examples/research_swarm/docker-compose.yml exec etcd \
  etcdctl get /barriers/research-demo --prefix --keys-only

# generation key changes
docker compose -f examples/research_swarm/docker-compose.yml exec etcd \
  etcdctl watch /barriers/research-demo/generation

# participant registration and cleanup
docker compose -f examples/research_swarm/docker-compose.yml exec etcd \
  etcdctl watch /barriers/research-demo/participants --prefix

# release key creation (barrier tripped)
docker compose -f examples/research_swarm/docker-compose.yml exec etcd \
  etcdctl watch /barriers/research-demo/released --prefix
```

## Run Without Docker Compose

If etcd is already running locally:

```bash
ETCD_ENDPOINTS=localhost:2379 go run ./examples/research_swarm
```

## Config

- `ETCD_ENDPOINTS` (default `localhost:2379`)
- `BARRIER_KEY` (default `/barriers/research-demo`)
- `METRICS_ADDR` (default `:2112`, set empty to disable)
- `DEMO_QUERY` (default AI architecture query)
- `RESEARCH_SWARM_MODEL` (provider model name)
- `RESEARCH_SWARM_REQUIRE_LLM` (`true` or `false`)

You can also use flags:

```bash
go run ./examples/research_swarm --query "your query" --timeout 90s --barrier-key /barriers/custom
```
