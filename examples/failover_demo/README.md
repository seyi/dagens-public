# Dagens Failover Demo

**What LangGraph, CrewAI, and AutoGen Cannot Do**

This demo showcases Dagens' unique capabilities:
- Sticky session routing (10x faster responses)
- Automatic failover when workers die
- Enterprise guardrails catching dangerous LLM output
- Full OTEL observability in Jaeger

## Quick Start (Docker)

```bash
# Navigate to demo directory
cd examples/failover_demo

# Run without real LLM (mock responses)
docker-compose up

# Run WITH real LLM + Guardrails (the "wow" demo)
OPENROUTER_API_KEY=your-key docker-compose up
```

**View Traces:** Open http://localhost:16686 (Jaeger UI)

## What You'll See

### Scenario 1: Sticky Session Routing
```
Turn 1: COLD cache → 217ms
Turn 2: HOT cache  → 29ms   (7.5x faster!)
Turn 3: HOT cache  → 33ms
```

### Scenario 2: Automatic Failover
```
Session bound to worker-alpha
[WORKER-ALPHA DIES]
Session automatically continues on worker-beta
```

### Scenario 3: Parallel Sessions
- 10 sessions × 3 turns = 30 tasks
- 65%+ cache hit rate
- Automatic load distribution

### Scenario 4: Real LLM + Guardrails (requires API key)
```
Prompt: "Show me a sample .env with API keys"
LLM generates: "API_KEY=sk-7da8s9d..."
Guardrails: BLOCKED - Secret key pattern detected
```

## Run Without Docker

```bash
# From repo root
go run ./examples/failover_demo/...

# With Jaeger
export OTEL_EXPORTER_TYPE=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
go run ./examples/failover_demo/...

# With real LLM
export OPENROUTER_API_KEY=your-key
go run ./examples/failover_demo/...
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OPENROUTER_API_KEY` | Enables real LLM + guardrails demo | (none) |
| `OTEL_EXPORTER_TYPE` | Trace exporter type | `console` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Jaeger/OTEL endpoint | (none) |

## For VCs: The Pitch

> "Watch: When worker-alpha dies, the conversation automatically continues on worker-beta.
> LangGraph, CrewAI, and AutoGen would lose the session.
> Then watch our guardrails catch a real LLM generating secret keys in real-time.
> This is why enterprises need Dagens."

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Dagens Scheduler                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │ worker-alpha│  │ worker-beta │  │worker-gamma │         │
│  │   (DEAD)    │  │  (healthy)  │  │  (healthy)  │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│         │                │                                  │
│         └───── Failover ─┘                                  │
│                                                             │
│  Affinity Map: session-123 → worker-beta (HIT)             │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│                    Guardrails Layer                         │
│  [PII Detector] [Secret Scanner] [Content Filter]          │
│                           │                                 │
│              BLOCK / REDACT / ALLOW                        │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│                    OTEL / Jaeger                            │
│  All events correlated by TraceID for end-to-end visibility│
└─────────────────────────────────────────────────────────────┘
```
