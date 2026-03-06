# Postgres Transition Store Quickstart

This guide enables durable scheduler transition storage using PostgreSQL.

It is focused on transition durability and startup replay input, not full durable queue semantics.

## 1. Run Postgres

Using Compose from the repo root:

```bash
docker compose up -d postgres
```

Default Compose credentials:
- user: `postgres`
- password: `postgres`
- db: `dagens`
- port: `5432`

## 2. Configure API Server

Set:

```bash
export SCHEDULER_TRANSITION_STORE=postgres
export SCHEDULER_TRANSITION_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/dagens?sslmode=disable'
```

Optional:

```bash
export SCHEDULER_RECOVERY_TIMEOUT=5m
export SCHEDULER_RESUME_RECOVERED_QUEUED_JOBS=false
```

`DATABASE_URL` can be used as DSN fallback if `SCHEDULER_TRANSITION_POSTGRES_DSN` is not set.

Code reference:
- [`cmd/api_server/main.go`](../cmd/api_server/main.go)

## 3. Start Control Plane and Worker

```bash
go run ./cmd/api_server
```

In another shell:

```bash
go run ./cmd/worker
```

Or run full stack:

```bash
docker compose up --build
```

Compose already sets:
- `SCHEDULER_TRANSITION_STORE=postgres`
- `SCHEDULER_TRANSITION_POSTGRES_DSN=postgres://postgres:postgres@postgres:5432/dagens?sslmode=disable`

## 4. Verify Transition Tables

Connect to Postgres and inspect:

```sql
\dt
SELECT COUNT(*) FROM job_transitions;
SELECT COUNT(*) FROM durable_jobs;
SELECT COUNT(*) FROM durable_tasks;
```

The scheduler initializes schema automatically on store startup.

Code reference:
- [`pkg/scheduler/transition_store_postgres.go`](../pkg/scheduler/transition_store_postgres.go)

## 5. Validate Recovery Path

1. Submit one or more jobs.
2. Stop API server.
3. Restart API server with the same Postgres transition-store config.
4. Confirm startup recovery runs and unfinished job visibility is reconstructed.

Notes:
- Recovery is visibility-first.
- Optional: set `SCHEDULER_RESUME_RECOVERED_QUEUED_JOBS=true` to re-enqueue recovered `QUEUED` jobs at startup.
- Replay is bounded by `SCHEDULER_RECOVERY_TIMEOUT`.
- Previously running tasks are not auto-redispatched by replay.

See:
- [`docs/STATE_MACHINE.md`](./STATE_MACHINE.md)
- [`docs/DURABILITY.md`](./DURABILITY.md)
- [`docs/RECOVERY_RUNBOOK.md`](./RECOVERY_RUNBOOK.md)

## 6. Troubleshooting

### `SCHEDULER_TRANSITION_STORE=postgres requires ... DSN`

Set one of:
- `SCHEDULER_TRANSITION_POSTGRES_DSN`
- `DATABASE_URL`

### API startup replay fails

Use:
- [`docs/RECOVERY_RUNBOOK.md`](./RECOVERY_RUNBOOK.md)

### Connection refused

Check Postgres is up and reachable:

```bash
docker compose ps postgres
```
