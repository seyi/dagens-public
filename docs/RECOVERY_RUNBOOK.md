# Recovery Runbook

This runbook covers operator actions when scheduler startup replay/recovery fails or times out.

Scope:
- API server startup recovery in the scheduler
- transition replay from configured transition store
- visibility-first recovery semantics
- HITL resume operational recovery for graph version mismatch and DLQ handling

Code references:
- [`pkg/scheduler/scheduler.go`](../pkg/scheduler/scheduler.go)
- [`pkg/scheduler/recovery.go`](../pkg/scheduler/recovery.go)
- [`pkg/scheduler/replay.go`](../pkg/scheduler/replay.go)
- [`pkg/scheduler/transition_store_postgres.go`](../pkg/scheduler/transition_store_postgres.go)
- [`cmd/api_server/main.go`](../cmd/api_server/main.go)
- [`pkg/hitl/resumption_worker.go`](../pkg/hitl/resumption_worker.go)
- [`pkg/hitl/orchestrator.go`](../pkg/hitl/orchestrator.go)
- [`pkg/hitl/postgres_store.go`](../pkg/hitl/postgres_store.go)

## Quick Facts

- Recovery runs before the scheduler loop starts.
- Recovery is bounded by `SCHEDULER_RECOVERY_TIMEOUT` (default `5m`).
- Replay is fail-fast for startup safety.
- Recovery is visibility-first; it does not auto-redispatch previously running tasks.
- Optional: `SCHEDULER_RESUME_RECOVERED_QUEUED_JOBS=true` re-enqueues recovered `QUEUED` jobs at startup.

## Common Failure Modes

1. Transition store not reachable (Postgres DSN/network/auth issue)
2. Replay timeout (`SCHEDULER_RECOVERY_TIMEOUT` too small for dataset)
3. Invalid transition stream for an unfinished job (state/ordering violation)
4. Context cancellation during startup shutdown

## Triage Steps

1. Confirm transition store mode and timeout:
```bash
echo "$SCHEDULER_TRANSITION_STORE"
echo "$SCHEDULER_TRANSITION_POSTGRES_DSN"
echo "$SCHEDULER_RECOVERY_TIMEOUT"
```

2. Check API startup logs for replay status and job context:
- look for recovery status (`succeeded`, `failed`, `canceled`)
- look for replay failure details (`job_id`, wrapped replay error)

3. If mode is `memory`:
- recovery input is process-local only
- restart recovery after process loss is limited by design

4. If mode is `postgres`:
- verify Postgres connectivity first
- verify transition tables exist and are readable

5. If a specific `job_id` is reported:
- inspect transition ordering and legality for that job
- confirm monotonic `sequence_id` progression

## Postgres Inspection Queries

Use these against the scheduler transition schema:

```sql
-- Unfinished jobs currently tracked
SELECT job_id, current_state, updated_at
FROM scheduler_durable_jobs
WHERE current_state NOT IN ('SUCCEEDED', 'FAILED', 'CANCELED')
ORDER BY updated_at DESC;
```

```sql
-- Transition stream for one job
SELECT sequence_id, entity_type, task_id, previous_state, new_state, transition, occurred_at
FROM scheduler_job_transitions
WHERE job_id = $1
ORDER BY sequence_id ASC;
```

```sql
-- Last known sequence baseline for one job
SELECT job_id, last_sequence_id, current_state, updated_at
FROM scheduler_durable_jobs
WHERE job_id = $1;
```

## Remediation Playbook

1. Connectivity/auth issue:
- fix DSN/network/credentials
- restart API server

2. Timeout issue:
- increase `SCHEDULER_RECOVERY_TIMEOUT` (for example from `5m` to `10m`)
- restart API server

3. Single-job invalid transition stream:
- isolate the failing `job_id` from logs
- correct bad transition data in durable store only if you have DB change control
- if immediate availability is required, temporarily move job to terminal state and restart

4. Emergency availability fallback:
- switch to `SCHEDULER_TRANSITION_STORE=memory`
- restart API server
- note: this bypasses durable replay input and should be temporary

## HITL Version Mismatch and DLQ Remediation

Use this flow when resumes fail with graph version mismatch or checkpoints are moved to DLQ.

### Detection

- Logs include: `hitl graph version mismatch during resume`
- Error class includes: `ErrGraphVersionMismatch`
- Metrics to watch:
  - `GraphVersionMismatches`
  - `CheckpointsMovedToDLQ`
  - `DLQSize`

### Immediate Triage

1. Confirm the checkpoint graph version and current registered graph version for the same `graph_id`.
2. Confirm whether deployment introduced an incompatible graph change after checkpoints were created.
3. Confirm impact scope:
- single request only
- one graph only
- broad mismatch across many pending checkpoints

### Remediation Options

1. Preferred: restore or re-register the prior graph version and replay affected checkpoints.
2. Controlled migration: create a versioned migration path for checkpoint state and resume explicitly (outside automatic runtime path).
3. Terminal handling: keep strict pinning, leave mismatched checkpoints in DLQ, and resolve manually with operator approval.

### Runbook Policy

- Do not auto-resume mismatched checkpoints by default.
- Do not silently rewrite `graph_version` in stored checkpoints.
- Treat mismatch as a release-management error until proven otherwise.

### Verification

After remediation:

1. `GraphVersionMismatches` stops increasing.
2. `DLQSize` stabilizes or decreases with planned replay/drain.
3. No repeated mismatch logs for the same `request_id`.

## Recovery Verification Checklist

After remediation and restart:

1. API server reaches healthy state
2. Recovery status logs show `succeeded`
3. Unfinished jobs are visible via API/read path
4. New submissions are accepted and executed
5. No repeated replay error for the same `job_id`

## Escalation Criteria

Escalate for code-level investigation when:
- replay repeatedly fails for different jobs after DB integrity checks
- legal transition streams still fail deterministic replay
- recovery succeeds but reconstructed visibility state is inconsistent
- startup timeout continues even after capacity/timeout tuning

Escalation inputs:
- API logs around recovery
- failing `job_id`
- ordered transition rows for that job
- scheduler config (`store mode`, timeout, queue/capacity settings)
