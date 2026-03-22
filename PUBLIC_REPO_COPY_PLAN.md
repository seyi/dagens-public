# Public Repo Copy Plan

This document provides a safe, non-destructive sequence for creating a clean public Dagens repository from the current internal repo.

It does not modify the current repo.

## Goal

Create a new public repository directory that contains only the curated public subset:

- runtime source
- selected docs
- tests
- examples
- deployment assets
- dashboard

while excluding:

- local tool state
- generated artifacts
- personal notes
- prompt scratch files
- stray binaries
- unrelated side-project code

## Assumptions

- Current internal repo: `/data/repos/dagens`
- New public repo target: `/data/repos/dagens-public`
- You want a filesystem copy first, then you can initialize/push a separate Git repo

If you want a different target path, replace `/data/repos/dagens-public` in the commands below.

## Recommended Method

Use `rsync` with explicit includes, then prune docs.

This is safer than trying to delete files after a broad copy.

## Step 1: Create the target directory

```bash
mkdir -p /data/repos/dagens-public
```

## Step 2: Copy the public top-level set

Run these commands from anywhere:

```bash
rsync -a /data/repos/dagens/README.md /data/repos/dagens-public/
rsync -a /data/repos/dagens/ARCHITECTURE.md /data/repos/dagens-public/
rsync -a /data/repos/dagens/go.mod /data/repos/dagens-public/
rsync -a /data/repos/dagens/go.sum /data/repos/dagens-public/
rsync -a /data/repos/dagens/Makefile /data/repos/dagens-public/
rsync -a /data/repos/dagens/Dockerfile /data/repos/dagens-public/
rsync -a /data/repos/dagens/Dockerfile.api /data/repos/dagens-public/
rsync -a /data/repos/dagens/Dockerfile.worker /data/repos/dagens-public/
rsync -a /data/repos/dagens/docker-compose.yml /data/repos/dagens-public/
rsync -a /data/repos/dagens/.env.example /data/repos/dagens-public/
rsync -a /data/repos/dagens/.gitignore /data/repos/dagens-public/
```

Copy directories:

```bash
rsync -a /data/repos/dagens/.github /data/repos/dagens-public/
rsync -a /data/repos/dagens/cmd /data/repos/dagens-public/
rsync -a /data/repos/dagens/pkg /data/repos/dagens-public/
rsync -a /data/repos/dagens/sdk /data/repos/dagens-public/
rsync -a /data/repos/dagens/tests /data/repos/dagens-public/
rsync -a /data/repos/dagens/deploy /data/repos/dagens-public/
rsync -a /data/repos/dagens/examples /data/repos/dagens-public/
rsync -a /data/repos/dagens/web /data/repos/dagens-public/
```

Optional:

```bash
rsync -a /data/repos/dagens/deployments /data/repos/dagens-public/
```

Only copy `.githooks` if you have reviewed it and it is safe for a public repo:

```bash
rsync -a /data/repos/dagens/.githooks /data/repos/dagens-public/
```

## Step 3: Create a minimal public docs directory

Create the docs directory:

```bash
mkdir -p /data/repos/dagens-public/docs
```

Copy only the approved public-facing docs:

```bash
rsync -a /data/repos/dagens/docs/REPO_TREE_AND_RUNTIME.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/SCALING_AND_DISPATCH_CLARIFICATION.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/DURABILITY.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/FAILURE_SEMANTICS.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/CONTROL_PLANE_HA.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/RECOVERY_RUNBOOK.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/INFRASTRUCTURE_THESIS.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/GETTING_STARTED.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/DEPLOYMENT_PATTERNS.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/JAEGER_INTEGRATION.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/LOAD_TESTING.md /data/repos/dagens-public/docs/
rsync -a /data/repos/dagens/docs/LAUNCH.md /data/repos/dagens-public/docs/
```

Do not copy the entire `docs/` directory wholesale.

That would reintroduce internal assessments and generated `docs/localhost:6060/`.

## Step 4: Tighten `.gitignore` in the public repo

Append a hygiene block to the public repo's `.gitignore` that excludes:

```gitignore
# hidden local tooling/session artifacts
.gocache/
docs/localhost:6060/
payload*.json
*_prompt.txt
review_context*.txt
state_fix*.txt
state_snapshot.xml
dagens-api
dagens-worker
main
```

## Step 5: Verify that known internal-only artifacts are absent

Run this check inside the new public repo:

```bash
cd /data/repos/dagens-public

test ! -e docs/localhost:6060
test ! -e quint-code-modified
test ! -e github.com
test ! -e payload.json
test ! -e compiler_fix_prompt.txt
test ! -e review_context.txt
test ! -e state_snapshot.xml
test ! -e grok_agent.sh
```

If these commands produce no output and no failing shell exit on the first missing check, the exclusion set is working.

## Step 6: Validate the copied repo shape

Run:

```bash
cd /data/repos/dagens-public
find . -maxdepth 2 -type d | sort
find . -maxdepth 1 -type f | sort
```

Expected top-level shape:

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
- `.githooks/`

## Step 7: Run a basic sanity check

Inside the public repo:

```bash
cd /data/repos/dagens-public
go test ./... 
```

If the full test suite is too heavy, at minimum run:

```bash
cd /data/repos/dagens-public
go test ./pkg/... ./cmd/...
```

Then verify the documented demo path still works:

```bash
cd /data/repos/dagens-public
docker compose config
```

## Step 8: Initialize the public Git repo

Once the directory looks correct:

```bash
cd /data/repos/dagens-public
git init
git add .
git status --short
```

Review the staged file list carefully before the first commit.

## Optional: Single-command copy with explicit excludes

If you prefer a single broad directory copy and trust the exclude list, use:

```bash
rsync -a \
  --exclude '.git/' \
  --exclude '.gocache/' \
  --exclude 'vendor/' \
  --exclude 'docs/localhost:6060/' \
  --exclude 'quint-code-modified/' \
  --exclude 'github.com/' \
  --exclude 'payload*.json' \
  --exclude '*_prompt.txt' \
  --exclude 'review_context*.txt' \
  --exclude 'state_fix*.txt' \
  --exclude 'state_snapshot.xml' \
  --exclude 'grok_agent.sh' \
  --exclude 'extract-oss-core.sh' \
  --exclude 'migrate_to_dagens.sh' \
  --exclude 'test_client.py' \
  --exclude 'test_client_direct.py' \
  --exclude 'test_client_only.py' \
  --exclude 'test_pyspark_client.py' \
  --exclude 'Backpressure' \
  --exclude 'Dagens' \
  --exclude 'This' \
  --exclude 'When' \
  --exclude 'Workers' \
  --exclude 'main' \
  --exclude 'dagens-api' \
  --exclude 'dagens-worker' \
  --exclude 'hello_dagens' \
  --exclude 'hitl_demo' \
  --exclude 'multi_agent_demo' \
  --exclude 'loadtest' \
  --exclude 'durable_demo' \
  --exclude 'research_agent' \
  --exclude 'advanced_demo' \
  --exclude 'actor_comms_demo' \
  --exclude 'state-watch-demo' \
  --exclude 'state_demo' \
  --exclude 'resilience_patterns_example' \
  --exclude 'sandbox_test_app_bin' \
  /data/repos/dagens/ /data/repos/dagens-public/
```

If you use this broad-copy method, still manually trim `docs/` afterward.

## Bottom Line

The safest extraction path is:

1. Copy an explicit allowlist of directories.
2. Copy only a small approved set of docs.
3. Tighten `.gitignore`.
4. Run a quick verification before the first public commit.
