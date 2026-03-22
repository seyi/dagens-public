# Testing

This document defines the current testing model for Dagens.

The repository contains a mix of:

- stable unit tests
- integration tests
- environment-dependent tests
- tests that currently need repair due to interface drift or changed runtime behavior

For public CI, do not treat `go test ./pkg/... ./cmd/...` as the default gate in v0.1.x.

## Default CI Command (Verified)

The following subset was verified to pass under the same containerized validation path used for the public repo:

```bash
go test \
  ./pkg/a2a \
  ./pkg/actor \
  ./pkg/agent \
  ./pkg/agentic \
  ./pkg/events \
  ./pkg/evaluation \
  ./pkg/registry \
  ./pkg/resilience \
  ./pkg/scheduler \
  ./pkg/secrets \
  ./pkg/state \
  ./pkg/telemetry
```

This is the recommended default CI gate for the OSS repo in v0.1.x.

## Full Recursive Test Command

The broad recursive command is:

```bash
go test ./pkg/... ./cmd/...
```

This currently does **not** pass in the containerized validation environment.

That does not mean the codebase is unusable. It means the full suite currently mixes stable unit coverage with tests that depend on additional environment, provider setup, or test refactors.

## Current Test Tiers

### Tier 1: Stable Unit / Core Runtime

Use this as the default CI gate.

Packages currently verified green:

- `pkg/a2a`
- `pkg/actor`
- `pkg/agent`
- `pkg/agentic`
- `pkg/events`
- `pkg/evaluation`
- `pkg/registry`
- `pkg/resilience`
- `pkg/scheduler`
- `pkg/secrets`
- `pkg/state`
- `pkg/telemetry`

### Tier 2: Integration / Environment-Dependent

These packages may require local services, Docker access, shells, or provider-specific setup:

- `pkg/hitl`
- `pkg/models/openai`
- `pkg/tools/mcp`
- leader-election-heavy parts of `pkg/coordination`

Examples of environment assumptions observed during containerized runs:

- Docker/testcontainers availability
- `bash` available in PATH
- provider credentials or proper stubs
- embedded etcd timing behavior

Barrier-specific coordination tests passed under the same containerized validation path when run in isolation:

```bash
go test ./pkg/coordination -run "TestDistributedBarrier|TestWaitForAgents|TestWaitForSwarm" -count=1
```

In practice, `pkg/coordination` should currently be treated as mixed:

- barrier paths: viable for targeted CI
- leader election paths: not suitable for the default gate yet

These should run in separate CI jobs with explicit prerequisites.

### Tier 3: Currently Failing / Needs Repair

These packages currently show test drift or failing assertions under the containerized full-suite run:

- `pkg/auth`
- `pkg/graph`
- `pkg/grpc`
- `pkg/integration`
- `pkg/remote`
- `pkg/sandbox`
- `pkg/agents`
- `pkg/runtime`
- `pkg/tools`
- leader-election-heavy parts of `pkg/coordination`

Common failure patterns observed:

- mocks no longer satisfy updated interfaces
- helper functions referenced in tests no longer exist
- runtime behavior changed but assertions were not updated
- tests assume external environment that is not always present
- some tests panic and need direct repair

## Suggested CI Structure

### Job 1: Default OSS Gate

Run only the verified stable subset:

```bash
go test \
  ./pkg/a2a \
  ./pkg/actor \
  ./pkg/agent \
  ./pkg/agentic \
  ./pkg/events \
  ./pkg/evaluation \
  ./pkg/registry \
  ./pkg/resilience \
  ./pkg/scheduler \
  ./pkg/secrets \
  ./pkg/state \
  ./pkg/telemetry
```

### Job 2: Optional Integration Tests

Run selected integration suites only when the environment is prepared.

Examples:

- Docker/testcontainers-enabled job
- provider-mocked or secret-injected job
- etcd-enabled job

### Job 3: Test Repair Backlog

Track failing packages separately and move them back into the default gate only after:

- interface mocks are updated
- brittle assertions are fixed
- environment prerequisites are made explicit
- panics are eliminated

## Release Guidance

For v0.1.x:

- use Tier 1 as the public CI gate
- document Tier 2 as environment-dependent
- treat Tier 3 as repair backlog, not release-blocking by default

This keeps the public repository honest and avoids implying that the entire recursive suite is currently portable across environments.
