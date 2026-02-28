# Dagens v0.1.0 Launch Announcement

## Title

**Dagens v0.1.0 — A Control-Plane Runtime for Distributed Agent Orchestration**

---

## Post Body

Dagens is a distributed control plane with an execution-plane worker fabric, built in Go. It separates scheduling from execution and treats orchestration as a distributed systems problem.

**Key features:**

- **Control-plane / execution-plane separation** — API server orchestrates, workers execute
- **Push-based dispatch** — Workers don't poll; scheduler selects healthy nodes and pushes tasks
- **Human-in-the-loop as primitive** — Checkpoint/resume with graph version validation
- **Multi-language orchestration** — A2A adapters for LangGraph, CrewAI, and Go agents
- **etcd-backed cluster awareness** — Optional dynamic registration with lease-based health
- **OTEL-native observability** — Tracing, metrics, and event recording

**This is infrastructure, not an agent framework.**

AI frameworks are consumers of Dagens, not its identity.

---

## Documentation

- **README:** https://github.com/seyi/dagens
- **Philosophy:** https://github.com/seyi/dagens/blob/main/docs/PHILOSOPHY.md
- **Discipline:** https://github.com/seyi/dagens/blob/main/docs/DISCIPLINE.md
- **Architecture:** https://github.com/seyi/dagens/blob/main/ARCHITECTURE.md

---

## Quickstart

```bash
git clone https://github.com/seyi/dagens
cd dagens
cp .env.example .env  # Add OPENROUTER_API_KEY for real AI execution
docker compose up --build
curl http://localhost:8080/health
```

---

## What Dagens Is Not

- ❌ Not a workflow engine (Temporal)
- ❌ Not an in-process graph executor (LangGraph)
- ❌ Not an agent framework (CrewAI)
- ❌ Not a task queue (Celery)

**Dagens is a control-plane runtime for distributed agent and service orchestration.**

---

## Comparison

| Concern | Temporal | LangGraph | Dagens |
|---------|----------|-----------|--------|
| Model | Workflow-as-code | In-process graph | Graph-as-job |
| Dispatch | Workers poll | In-process | Push-based |
| Control Plane | Embedded | N/A | Explicit separation |
| HITL | Manual signals | N/A | First-class checkpoint |
| Interop | Language SDKs | Python only | A2A protocol adapters |

See `docs/PHILOSOPHY.md` for deeper architectural discussion.

---

## What's Next (v0.2.0 Roadmap)

- Backpressure-aware dispatch
- Advanced scheduling strategies (least-loaded, weighted routing)
- Kubernetes-native operator
- Policy engine integration (OPA-style)
- Distributed event store backend

---

## Acknowledgments

Dagens stands on the shoulders of:
- Apache Spark's DAG scheduler
- Kubernetes' control-plane / worker separation
- etcd's lease-based health detection
- Actor model's message-passing concurrency

---

## License

Apache 2.0

---

## Where to Post

1. **Hacker News** — https://news.ycombinator.com/submit
2. **Reddit r/distributedsystems** — https://www.reddit.com/r/distributedsystems/
3. **Reddit r/golang** — https://www.reddit.com/r/golang/
4. **Twitter/LinkedIn** — If you use these platforms
5. **Go Discord/Slack** — Gophers Slack, Discord channels

---

## How to Respond to Comments

**"Why not Temporal?"**
→ Different category. Temporal is workflow-as-code for business processes. Dagens is control-plane orchestration for agents/services. See `docs/PHILOSOPHY.md`.

**"This is just X but in Go"**
→ Dagens isn't X. It's explicit control-plane / execution-plane separation with push dispatch. Graphs are submitted jobs, not durable functions.

**"Can I use this in production?"**
→ v0.1.0. It's a starting point. The architecture is sound; battle-testing comes next.

**"How does backpressure work?"**
→ Roadmap item for v0.2.0. Current implementation is basic. PRs welcome.

**"Why the AI focus?"**
→ AI isn't the focus. AI is a consumer. Dagens is infrastructure that happens to orchestrate AI workloads well.

---

## Tone Guidelines

✅ Do:
- Answer questions calmly
- Link to Philosophy doc for depth
- Acknowledge limitations honestly
- Note feedback for v0.2.0

❌ Don't:
- Get defensive
- Oversell capabilities
- Attack competitors
- Promise features you won't build

---

## Success Metrics

Not:
- ❌ 1000 GitHub stars in a week
- ❌ Viral Twitter thread
- ❌ Hype-driven adopters

But:
- ✅ 5-10 serious engineers who understand the model
- ✅ Feedback from people building real distributed systems
- ✅ Trust compounded through correctness

---

**Ship it.**
