# Public Repo File Manifest

This document defines the recommended contents for a clean public Dagens repository.

The goal is to publish the product-facing runtime without exposing local tooling state, founder notes, review scratch files, generated artifacts, or unrelated side-project code.

## Recommendation

Preferred approach:

1. Create a new public repository from a curated subset of this repo.
2. Copy only the paths listed in `Keep`.
3. Exclude all paths listed in `Drop`.
4. Carry over only the docs listed in `Carry Over Docs`.

This is safer than trying to prune the current repo in place.

## Keep

These top-level paths are appropriate for the public repo.

### Core product paths

- `cmd/`
- `pkg/`
- `sdk/`
- `tests/`
- `deploy/`
- `examples/`
- `web/`

### Optional public infra/demo paths

- `deployments/`
  - Keep if you want public load-testing and infra examples.
  - If not, exclude it entirely.

### Root files to keep

- `README.md`
- `ARCHITECTURE.md`
- `go.mod`
- `go.sum`
- `Makefile`
- `Dockerfile`
- `Dockerfile.api`
- `Dockerfile.worker`
- `docker-compose.yml`
- `.env.example`
- `.gitignore`

### Git/project metadata to keep

- `.github/`
- `.githooks/`
  - Keep only if hooks are generic and repo-safe.
  - If hooks contain local assumptions, exclude this.

## Drop

These top-level paths or file classes should not be part of the public repo.

### Local tooling / assistant state

- `.claude/`
- `.cursor/`
- `.grok/`
- `.quint/`
- `.mcp.json`
- `.zen_context_export.md`
- `.zen_continuations.md`

### VCS and local cache

- `.git/`
- `.gocache/`
- `vendor/`
  - Keep only if you intentionally publish vendored dependencies.
  - Default recommendation: exclude and rely on `go.mod`.

### Generated docs / local snapshots

- `docs/localhost:6060/`

### Side projects / unrelated imported code

- `quint-code-modified/`
- `github.com/`

### Stray binaries and accidental top-level artifacts

- `Backpressure`
- `Dagens`
- `This`
- `When`
- `Workers`
- `main`
- `agent-server`
- `api_server`
- `dagens-api`
- `dagens-worker`
- `hello_dagens`
- `hitl_demo`
- `multi_agent_demo`
- `loadtest`
- `durable_demo`
- `research_agent`
- `advanced_demo`
- `actor_comms_demo`
- `state-watch-demo`
- `state_demo`
- `resilience_patterns_example`
- `sandbox_test_app_bin`

### Prompt payloads / scratch files

- `payload*.json`
- `*_prompt.txt`
- `review_context*.txt`
- `state_fix*.txt`
- `state_snapshot.xml`

### Internal helper scripts

- `grok_agent.sh`
- `extract-oss-core.sh`
- `migrate_to_dagens.sh`

### Local test clients and one-off scripts

- `test_client.py`
- `test_client_direct.py`
- `test_client_only.py`
- `test_pyspark_client.py`

### Internal planning / assessment docs in repo root

- `CRITICAL_ASSESSMENT.md`
- `HUMAN_IN_THE_LOOP_IMPLEMENTATION.md`
- `HUMAN_IN_THE_LOOP_IMPLEMENTATION_PLAN.md`
- `HUMAN_IN_THE_LOOP_IMPLEMENTATION_PLAN_IMPROVED.md`
- `IMPLEMENTATION_PLAN.md`
- `IMPROVEMENTS.md`
- `MIGRATION_COMPLETED.md`
- `REPO_SPLIT_ASSESSMENT.md`
- `REPO_SPLIT_PROPOSAL.md`
- `SECURITY_IMPLEMENTATION_PLAN.md`
- `SECURITY_RESPONSE.md`

## Carry Over Docs

These docs are strong candidates for the public repo because they support the product story and make the runtime easier to evaluate.

### Recommended public docs

- `docs/REPO_TREE_AND_RUNTIME.md`
- `docs/SCALING_AND_DISPATCH_CLARIFICATION.md`
- `docs/DURABILITY.md`
- `docs/FAILURE_SEMANTICS.md`
- `docs/INFRASTRUCTURE_THESIS.md`
- `docs/GETTING_STARTED.md`
- `docs/DEPLOYMENT_PATTERNS.md`
- `docs/JAEGER_INTEGRATION.md`
- `docs/LOAD_TESTING.md`
- `docs/LAUNCH.md`

### Keep only if they are edited for public tone

These may contain useful material, but they read like internal planning or assessment and should be reviewed before publishing.

- `docs/PRODUCTION_READINESS_CHECKLIST.md`
- `docs/PRODUCTION_READINESS_GAP_STATUS.md`
- `docs/RUNTIME_SUMMARY.md`
- `docs/DISTRIBUTED_GRAPH_EXECUTION.md`
- `docs/DISTRIBUTED_SERVICES_DESIGN.md`
- `docs/DISTRIBUTED_AGENT_ORCHESTRATION.md`
- `docs/DISTRIBUTED_COORDINATION_SYSTEM.md`
- `docs/STATE_MANAGEMENT_DESIGN.md`
- `docs/STICKY_SCHEDULING_V0_SPEC.md`

### Keep private unless intentionally publishing design history

These are better in a private operating repo or internal docs repo.

- `docs/INVESTMENT_MEMO.md`
- `docs/SHOWCASE_PUBLISH_PLAN.md`
- `docs/SECURITY_REVIEW_FINDINGS.md`
- `docs/SECURITY_INCIDENT_REPORT.md`
- `docs/GRAPH_OBSERVABILITY_ASSESSMENT.md`
- `docs/FRAMEWORK_ASSESSMENT.md`
- `docs/PRODUCTION_READINESS_ASSESSMENT.md`
- `docs/SCALE_READINESS_ASSESSMENT.md`
- `docs/HITL_IMPLEMENTATION_ASSESSMENT.md`
- `docs/GEMINI3_CRITICAL_ASSESSMENT.md`
- `docs/ARCHITECTURAL_CONSENSUS.md`
- `docs/CONSENSUS_CODE_REVIEW.md`
- `docs/REPOSITORY_SPLIT_RECOMMENDATION.md`

## Suggested Public Repo Shape

If you create a fresh public repo, the initial top-level tree should look like:

```text
.
|-- .env.example
|-- .github/
|-- .gitignore
|-- ARCHITECTURE.md
|-- Dockerfile
|-- Dockerfile.api
|-- Dockerfile.worker
|-- Makefile
|-- README.md
|-- cmd/
|-- deploy/
|-- docker-compose.yml
|-- docs/
|-- examples/
|-- go.mod
|-- go.sum
|-- pkg/
|-- sdk/
|-- tests/
`-- web/
```

Optional:

- `deployments/`

## Follow-Up Cleanup

If you publish from this repo instead of a new one, update `.gitignore` to prevent reintroducing local clutter.

At minimum, add:

```gitignore
.claude/
.cursor/
.grok/
.quint/
.mcp.json
.zen_context_export.md
.zen_continuations.md
.gocache/
docs/localhost:6060/
payload*.json
*_prompt.txt
review_context*.txt
state_fix*.txt
state_snapshot.xml
agent-server
api_server
dagens-api
dagens-worker
main
```

## Bottom Line

The public repo should present Dagens as:

1. A clean runtime codebase.
2. A small, deliberate documentation set.
3. A disciplined infrastructure project.

Do not make the public repo carry internal operating history, prompt scratch files, or local tool state.
