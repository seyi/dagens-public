# Recovery Runbook

This runbook covers operator actions when scheduler startup replay/recovery fails or times out.

Scope:
- API server startup recovery in the scheduler
- transition replay from configured transition store
- visibility-first recovery semantics
- HITL resume operational recovery for graph version mismatch and DLQ handling

Local HA compose environments are development-only:
- hardcoded local credentials are used for Postgres and Redis
- service-to-service traffic is plaintext
- auth may be disabled via `DEV_MODE=true`
- published ports should remain localhost-only

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

## HA Failover Drill (Scripted)

Use this drill to validate leader failover behavior and dispatch fencing in a
multi-control-plane deployment.

Script:
- [`scripts/failover_drill.sh`](../scripts/failover_drill.sh)
- [`scripts/repeat_failover_drill.sh`](../scripts/repeat_failover_drill.sh) for repeated-gate evidence capture
- [`scripts/hitl_failover_drill.sh`](../scripts/hitl_failover_drill.sh) for operator-facing HITL pause/resume takeover validation
- [`scripts/chaos_load_suite.sh`](../scripts/chaos_load_suite.sh) for combined repeat/backpressure/HITL evidence capture with machine-readable `results.json`
- [`scripts/failback_chaos_drill.sh`](../scripts/failback_chaos_drill.sh) for follower-readiness-gated reclaim back to a restored former leader, with `result.env` and `result.json`

### Prerequisites

1. Scheduler leadership is enabled (for example `SCHEDULER_LEADERSHIP_BACKEND=etcd`).
2. At least two control-plane instances are deployed.
3. API endpoint is reachable (`API_URL`).
4. SQL verification requires `DATABASE_URL`; either host `psql` or `docker` must be available.
5. A concrete leader stop command is known (`LEADER_STOP_CMD`) for takeover validation.

### Example Commands

Canary-only validation:

```bash
API_URL=http://localhost:8080 \
JOB_COUNT=3 \
./scripts/failover_drill.sh
```

Local two-control-plane HA topology (etcd + dual API + LB):

```bash
docker compose -f docker-compose.yml -f deploy/ha/docker-compose.ha.yml up -d --build
```

Then run takeover drill through the HA load balancer:

```bash
API_URL=http://localhost:18083 \
JOB_COUNT=3 \
DRILL_TIMEOUT_SECONDS=180 \
LEADER_STOP_CMD="docker kill ha-api-server-b-1" \
DATABASE_URL="postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable" \
./scripts/failover_drill.sh
```

Repeated-gate validation with archived evidence:

```bash
API_URL=http://localhost:18083 \
DATABASE_URL="postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable" \
EVIDENCE_ROOT=/tmp/dagens-ha-evidence-repeat-$(date -u +%Y%m%dT%H%M%SZ) \
./scripts/repeat_failover_drill.sh
```

HITL pause/resume takeover validation through the HA load balancer:

```bash
API_URL=http://localhost:18083 \
DATABASE_URL="postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable" \
EVIDENCE_ROOT=/tmp/dagens-hitl-ha-evidence-$(date -u +%Y%m%dT%H%M%SZ) \
./scripts/hitl_failover_drill.sh
```

Backpressure recovery validation through the HA load balancer:

```bash
API_URL=http://localhost:18083 \
EVIDENCE_ROOT=/tmp/dagens-backpressure-ha-evidence-$(date -u +%Y%m%dT%H%M%SZ) \
./scripts/backpressure_failover_drill.sh
```

Combined chaos/load validation in one pass:

```bash
API_URL=http://localhost:18083 \
DATABASE_URL="postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable" \
EVIDENCE_ROOT=/tmp/dagens-chaos-suite-$(date -u +%Y%m%dT%H%M%SZ) \
./scripts/chaos_load_suite.sh
```

Planned failback validation with follower-readiness gating:

```bash
API_URL=http://localhost:18083 \
DATABASE_URL="postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable" \
EVIDENCE_ROOT=/tmp/dagens-failback-chaos-$(date -u +%Y%m%dT%H%M%SZ) \
./scripts/failback_chaos_drill.sh
```

Failover + fencing validation:

```bash
API_URL=http://localhost:8080 \
LEADER_STOP_CMD="kubectl delete pod <leader-pod> -n <namespace>" \
DATABASE_URL="postgres://postgres:postgres@localhost:5432/dagens?sslmode=disable" \
./scripts/failover_drill.sh
```

### Standard Operator Flow

1. Verify both API instances and LB are healthy.
2. Identify current leader (for example via etcd key inspection).
3. Set `LEADER_STOP_CMD` to terminate the current leader instance.
4. Run `scripts/failover_drill.sh` through the LB endpoint.
5. Confirm:
   - all canary jobs reached terminal state before timeout
   - duplicate `TASK_DISPATCHED` check reports none
   - leadership moved to follower
6. Restore the stopped API instance and confirm both instances are healthy.

### Repeatability Gate

Use `scripts/repeat_failover_drill.sh` when you need release-style evidence rather
than a single failover sample.

Default gate:
- 3 consecutive leader-loss takeover drills with `JOB_COUNT=3`
- 1 higher-load takeover drill with `JOB_COUNT=20`
- per-run artifacts under `EVIDENCE_ROOT`

The harness restores the killed API instance between runs and captures:
- leader-resolution metadata
- failover drill logs
- compose status/log snapshots
- load-balancer health output

### Chaos/Load Suite Gate

Use `scripts/chaos_load_suite.sh` when you want one evidence root containing:
- repeated failover validation
- backpressure recovery validation
- HITL pause/resume takeover validation
- optional failback reclaim validation

The suite:
- runs the enabled drill scripts sequentially
- restores HA topology after every attempted drill, including failed ones
- writes per-drill logs plus a shared summary under one `EVIDENCE_ROOT`
- continues collecting later drill evidence even if an earlier drill fails
- is suitable for CI artifact retention and release evidence collection

Set `RUN_FAILBACK_CHAOS=true` when you also want the suite to validate:
- failover away from the current leader
- follower warm-replay/readiness freshness on the restored former leader
- controlled reclaim back to that restored leader
- post-reclaim continuity through the LB

### HITL Takeover Gate

Use `scripts/hitl_failover_drill.sh` when you need operator-facing evidence that
the `AWAITING_HUMAN` pause/resume path survives leader loss and callback replay.

The drill validates:
- a real scheduler job reaches `AWAITING_HUMAN`
- callback submission succeeds through the LB after leader termination
- duplicate callback delivery stays idempotent (`202` then `200` in the latest pass)
- exactly one durable `JOB_AWAITING_HUMAN`, `JOB_RESUMED`, and `JOB_SUCCEEDED`
- checkpoint cleanup leaves `hitl_execution_checkpoints` empty for the `request_id`

Latest validated run:
- Date: 2026-03-13
- Evidence root: `/tmp/dagens-hitl-ha-evidence-run7`
- SQL summary:
  - `awaiting_human=1`
  - `job_resumed=1`
  - `job_succeeded=1`
  - `checkpoint_rows=0`

### Backpressure Recovery Gate

Use `scripts/backpressure_failover_drill.sh` when you need operator-facing
evidence that admission pressure recovers after leader loss without a prolonged
`429` storm.

The drill validates:
- LB-routed overload reaches `429`
- overload responses carry `Retry-After: 5`
- follower control planes forward `/v1/jobs` submissions to the current leader
- leader loss during overload still produces a clean leader transition
- LB-routed probe submissions recover to `202`
- the recovered probe job reaches terminal state before restore completes

Latest validated run:
- Date: 2026-03-13
- Evidence root: `/tmp/dagens-backpressure-ha-evidence-run4`
- Summary:
  - `leader_before=api-a`
  - `leader_after=api-b`
  - `accepted_overload_jobs=3`
  - `post_failover_429s=0`
  - `probe_terminal_status=COMPLETED`

### Containerized Test Execution Notes

For local package verification without a host Go toolchain:

```bash
docker run --rm \
  -v /data/repos/dagens:/src \
  -w /src \
  golang:1.25.2 \
  go test ./cmd/api_server -count=1 -v
```

`pkg/hitl` uses `TestMain` plus `testcontainers-go`, so the Docker socket must be
mounted when running it from a Go container:

```bash
docker run --rm \
  -v /data/repos/dagens:/src \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -w /src \
  golang:1.25.2 \
  go test ./pkg/hitl \
    -run '^(TestHITL_EndToEnd_CallbackQueueResumeFinish|TestHITL_CallbackFailoverDuplicateStaysIdempotent|TestResumptionWorker_UsesGraphNativeResume)$' \
    -count=1 -v
```

Without the socket mount, `pkg/hitl` containerized runs fail with a Docker-host
discovery error from `testcontainers-go`.

### Failback Validation Flow

Use this when the former primary has been restored and you want to validate a
controlled reclaim path without dispatch disruption.

1. Run a failover validation phase (leader stop + continuity/fencing checks).
2. Verify readiness/catch-up conditions before reclaim.
   Confirm the target follower is no longer recovering and that its durable
   reconciliation signal is fresh before preferring it. The scheduler now
   records `scheduler_last_reconcile_timestamp_seconds`; planned reclaim should
   wait until that timestamp is advancing/current on the instance you intend to
   promote.
3. Execute failback command to prefer or restore target leader topology.
4. Run post-failback continuity/fencing validation.

For the local HA compose topology, [`scripts/failback_chaos_drill.sh`](../scripts/failback_chaos_drill.sh)
automates that sequence by:
1. failing over away from the current leader
2. restoring that former leader as a follower
3. waiting for a fresh `scheduler_last_reconcile_timestamp_seconds` signal on the restored follower
4. reclaiming leadership back to that node
5. re-running continuity validation through the LB

Scripted wrapper:

```bash
API_URL=http://localhost:18083 \
DATABASE_URL="postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable" \
FAILOVER_LEADER_STOP_CMD="docker kill <current-leader-cid>" \
FAILBACK_READINESS_CHECK_CMD="docker compose -f deploy/ha/docker-compose.ha.yml ps" \
FAILBACK_CMD="docker compose -f deploy/ha/docker-compose.ha.yml up -d api-server api-server-b" \
./scripts/failback_drill.sh
```

This wrapper calls `scripts/failover_drill.sh` twice:
- `phase-1`: failover continuity validation
- `phase-4`: post-failback continuity validation

### Pass Criteria

1. All canary jobs reach terminal state (`COMPLETED`/`SUCCEEDED`/`FAILED`/`CANCELED`) before timeout.
2. No duplicate `TASK_DISPATCHED` rows for the same `(task_id, attempt)` in the drill window.
3. No non-terminal durable task rows remain for submitted canary jobs after drill completion.
4. No prolonged dispatch outage after leader termination.
5. Former follower becomes leader within expected failover window.

Note:
- `FAILED` canary outcomes are acceptable for this gate as long as they are
  terminal and fencing integrity holds. The gate validates takeover continuity
  and anti-duplication semantics, not business-success-only outcomes.

### SQL Check Behavior

When `DATABASE_URL` is set, the drill performs duplicate-dispatch validation:

1. Uses host `psql` if installed.
2. Falls back to containerized `psql` (`postgres:15-alpine`) when `psql` is not installed.
3. For localhost DSNs, containerized fallback uses host networking to reach Postgres.

It also performs durable integrity checks for submitted canary jobs and fails when
any canary task remains non-terminal in `scheduler_durable_tasks`.
Checks are scoped to the current run using `DRILL_ID` + drill start timestamp.

If neither `psql` nor `docker` is available, SQL checks are skipped and must be run manually.

### Optional Alerting Hooks

Scheduler can emit critical startup/failover alerts to a webhook endpoint:

- `SCHEDULER_ALERT_WEBHOOK_URL` (optional): webhook URL for alert payloads
- `SCHEDULER_ALERT_REQUEST_TIMEOUT` (optional, duration): send timeout (default `3s`)
- `SCHEDULER_ALERT_MAX_ATTEMPTS` (optional, int): alert retry attempts (default `3`)
- `SCHEDULER_ALERT_RETRY_BASE_INTERVAL` (optional, duration): exponential backoff base (default `200ms`)

### Follow-up If Drill Fails

1. Check scheduler logs for leadership errors and dispatch deferrals.
2. Verify etcd leadership key health and lease turnover timing.
3. Query `scheduler_job_transitions` for duplicate dispatch claims and inspect `attempt`.
4. Re-run drill after remediation and attach outputs to release evidence.

### Evidence Capture Template

For release evidence, record:

1. Drill command used (including `API_URL`, `JOB_COUNT`, `DRILL_TIMEOUT_SECONDS`).
2. Leader stop action (`LEADER_STOP_CMD`) and timestamp.
3. Final canary statuses.
4. SQL duplicate-claim check output.
5. Post-drill service state (API A/API B/LB healthy).

For failback drills also capture:

6. Readiness check command + result before reclaim.
7. Failback command + timestamp.
8. Post-failback canary terminal-state summary.

### Local Drill Procedure (Validated)

Use this exact flow to reproduce the HA takeover drill locally and collect
evidence in one pass.

```bash
cd /data/repos/dagens
mkdir -p /tmp/dagens-ha-evidence

# 1) Start HA stack
docker compose -f deploy/ha/docker-compose.ha.yml up -d --build

# 2) Wait for LB health
deadline=$(( $(date +%s) + 300 ))
until curl -fsS http://localhost:18083/health >/dev/null; do
  if [[ $(date +%s) -ge ${deadline} ]]; then
    echo "LB health timeout" >&2
    exit 1
  fi
  sleep 2
done

# 3) Resolve current leader from etcd
ETCD_CID="$(docker compose -f deploy/ha/docker-compose.ha.yml ps -q etcd)"
LEADER_ID="$(docker run --rm --network "container:${ETCD_CID}" quay.io/coreos/etcd:v3.5.15 \
  etcdctl --endpoints=http://127.0.0.1:2379 get --prefix /dagens/control-plane/scheduler \
  | awk 'NR % 2 == 0 { print; exit }')"

# 4) Map leader identity -> kill command
API_A_CID="$(docker compose -f deploy/ha/docker-compose.ha.yml ps -q api-server)"
API_B_CID="$(docker compose -f deploy/ha/docker-compose.ha.yml ps -q api-server-b)"
if [[ "${LEADER_ID}" == "api-a" ]]; then
  LEADER_STOP_CMD="docker kill ${API_A_CID}"
elif [[ "${LEADER_ID}" == "api-b" ]]; then
  LEADER_STOP_CMD="docker kill ${API_B_CID}"
else
  echo "Unable to resolve leader id from etcd: ${LEADER_ID}" >&2
  exit 1
fi
echo "leader=${LEADER_ID}" | tee /tmp/dagens-ha-evidence/leader.txt
echo "stop_cmd=${LEADER_STOP_CMD}" | tee -a /tmp/dagens-ha-evidence/leader.txt

# 5) Run drill through LB with SQL fencing verification
API_URL="http://localhost:18083" \
JOB_COUNT=3 \
DRILL_TIMEOUT_SECONDS=180 \
LEADER_STOP_CMD="${LEADER_STOP_CMD}" \
DATABASE_URL="postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable" \
./scripts/failover_drill.sh | tee /tmp/dagens-ha-evidence/failover-drill.log

# 6) Capture diagnostics
docker compose -f deploy/ha/docker-compose.ha.yml ps \
  > /tmp/dagens-ha-evidence/compose-ps.txt
docker compose -f deploy/ha/docker-compose.ha.yml logs --no-color \
  > /tmp/dagens-ha-evidence/compose-logs.txt
curl -sS http://localhost:18083/health \
  > /tmp/dagens-ha-evidence/lb-health.json

# 7) Teardown
docker compose -f deploy/ha/docker-compose.ha.yml down -v
```

Expected success signals:

1. Drill prints `PASS: failover drill checks completed.`
2. All canary jobs are terminal (`COMPLETED`/`SUCCEEDED`/`FAILED`/`CANCELED`).
3. SQL fence check reports no duplicate `TASK_DISPATCHED` claims.

### Latest Validated Result (2026-03-11)

- Command path: scripted local HA takeover drill via LB + `LEADER_STOP_CMD`
- Outcome: `PASS: failover drill checks completed.`
- Canary terminal states:
  - `1bb56287-d6ab-463c-be08-ba3023990cea`: `COMPLETED`
  - `ee249b39-d377-4f43-ada1-16502ced430c`: `FAILED`
  - `90a45f53-b9aa-4ce6-93cb-70cbd657bf36`: `FAILED`
- Fencing SQL check: no duplicate `TASK_DISPATCHED` claims detected.
- Evidence bundle: `/tmp/dagens-ha-evidence-verify5`

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
