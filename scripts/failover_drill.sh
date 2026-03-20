#!/usr/bin/env bash
set -euo pipefail

# Scripted HA failover drill for Dagens scheduler leadership/fencing verification.
#
# This script:
# 1) submits canary jobs
# 2) optionally executes a leader stop/failover command
# 3) verifies canary jobs reach terminal states
# 4) optionally checks for duplicate TASK_DISPATCHED claims in Postgres
#
# Usage:
#   API_URL=http://localhost:8080 JOB_COUNT=3 ./scripts/failover_drill.sh
#   API_URL=http://localhost:8080 LEADER_STOP_CMD="kubectl delete pod <leader-pod> -n <ns>" ./scripts/failover_drill.sh
#   API_URL=http://localhost:8080 DATABASE_URL="postgres://..." ./scripts/failover_drill.sh

API_URL="${API_URL:-http://localhost:8080}"
JOB_COUNT="${JOB_COUNT:-3}"
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS:-180}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-2}"
LEADER_STOP_CMD="${LEADER_STOP_CMD:-}"
DATABASE_URL="${DATABASE_URL:-${SCHEDULER_TRANSITION_POSTGRES_DSN:-}}"
DRILL_ID="${DRILL_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
DRILL_START_EPOCH="$(date +%s)"
REQUIRE_SUCCESS_TERMINAL="${REQUIRE_SUCCESS_TERMINAL:-false}"

command -v curl >/dev/null 2>&1 || { echo "curl is required"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required"; exit 1; }

has_sql_client() {
  command -v psql >/dev/null 2>&1 || command -v docker >/dev/null 2>&1
}

run_sql_check() {
  local sql="$1"

  if command -v psql >/dev/null 2>&1; then
    psql "${DATABASE_URL}" -At -F '|' -c "${sql}"
    return $?
  fi

  if command -v docker >/dev/null 2>&1; then
    local docker_network_args=()
    # Host networking allows localhost-based DATABASE_URL values to resolve.
    if [[ "${DATABASE_URL}" == *"@localhost"* || "${DATABASE_URL}" == *"@127.0.0.1"* ]]; then
      docker_network_args=(--network host)
    fi
    docker run --rm "${docker_network_args[@]}" postgres:15-alpine \
      psql "${DATABASE_URL}" -At -F '|' -c "${sql}"
    return $?
  fi

  return 127
}

run_sql_check_must_succeed() {
  local sql="$1"
  local label="$2"
  local output=""

  if ! output="$(run_sql_check "${sql}" 2>&1)"; then
    echo "FAIL: SQL check failed (${label})."
    echo "${output}"
    exit 1
  fi

  printf '%s' "${output}"
}

sql_in_list_from_job_ids() {
  local out=""
  local id=""
  for id in "${job_ids[@]}"; do
    id="${id//\'/\'\'}"
    if [[ -n "${out}" ]]; then
      out+=", "
    fi
    out+="'${id}'"
  done
  echo "${out}"
}

echo "== Dagens HA Failover Drill =="
echo "API_URL=${API_URL}"
echo "JOB_COUNT=${JOB_COUNT}"
echo "DRILL_TIMEOUT_SECONDS=${DRILL_TIMEOUT_SECONDS}"
echo "POLL_INTERVAL_SECONDS=${POLL_INTERVAL_SECONDS}"
echo "DRILL_ID=${DRILL_ID}"
echo "REQUIRE_SUCCESS_TERMINAL=${REQUIRE_SUCCESS_TERMINAL}"
if [[ -n "${LEADER_STOP_CMD}" ]]; then
  echo "LEADER_STOP_CMD is set"
else
  echo "LEADER_STOP_CMD is not set (drill will validate canary flow only)"
fi

job_ids=()
echo "Submitting ${JOB_COUNT} canary jobs..."
for i in $(seq 1 "${JOB_COUNT}"); do
  payload="$(cat <<JSON
{
  "name": "ha-failover-canary-${DRILL_ID}-${i}",
  "nodes": [
    {"id": "start", "type": "function", "name": "Start"},
    {"id": "end", "type": "function", "name": "End"}
  ],
  "edges": [{"from": "start", "to": "end"}],
  "entry_node": "start",
  "finish_nodes": ["end"],
  "input": {"instruction": "ha failover canary ${i}"}
}
JSON
)"
  job_id=""
  submit_attempt=0
  while [[ ${submit_attempt} -lt 5 ]]; do
    submit_attempt=$((submit_attempt + 1))
    resp="$(curl -sS -X POST "${API_URL}/v1/jobs" -H "Content-Type: application/json" -d "${payload}" || true)"
    job_id="$(echo "${resp}" | jq -r '.job_id // .id // empty' 2>/dev/null || true)"
    if [[ -n "${job_id}" && "${job_id}" != "null" ]]; then
      break
    fi
    if [[ ${submit_attempt} -lt 5 ]]; then
      sleep 1
    fi
  done
  if [[ -z "${job_id}" || "${job_id}" == "null" ]]; then
    echo "Failed to parse job id from response after retries: ${resp}"
    exit 1
  fi
  echo "  submitted: ${job_id}"
  job_ids+=("${job_id}")
done

if [[ -n "${LEADER_STOP_CMD}" ]]; then
  echo "Triggering failover command..."
  bash -lc "${LEADER_STOP_CMD}"
fi

deadline=$(( $(date +%s) + DRILL_TIMEOUT_SECONDS ))
declare -A final_status
while [[ $(date +%s) -lt ${deadline} ]]; do
  complete=0
  for id in "${job_ids[@]}"; do
    if [[ -n "${final_status[${id}]:-}" ]]; then
      ((complete+=1))
      continue
    fi
    status_resp="$(curl -sS "${API_URL}/v1/jobs/${id}" || true)"
    status="$(echo "${status_resp}" | jq -r '.status // .Status // .state // .State // empty' 2>/dev/null || true)"
    case "${status}" in
      COMPLETED|SUCCEEDED|FAILED|CANCELED)
        final_status["${id}"]="${status}"
        ((complete+=1))
        ;;
      *)
        ;;
    esac
  done
  if [[ ${complete} -eq ${#job_ids[@]} ]]; then
    break
  fi
  sleep "${POLL_INTERVAL_SECONDS}"
done

echo "Final canary job states:"
failed_terminal=0
failed_success=0
timed_out_job_ids=()
unsuccessful_job_ids=()
for id in "${job_ids[@]}"; do
  st="${final_status[${id}]:-TIMEOUT}"
  echo "  ${id}: ${st}"
  if [[ "${st}" == "TIMEOUT" ]]; then
    failed_terminal=1
    timed_out_job_ids+=("${id}")
  elif [[ "${REQUIRE_SUCCESS_TERMINAL}" == "true" && "${st}" != "COMPLETED" && "${st}" != "SUCCEEDED" ]]; then
    failed_success=1
    unsuccessful_job_ids+=("${id}:${st}")
  fi
done

fencing_violation=0
integrity_violation=0
if [[ -n "${DATABASE_URL}" ]] && has_sql_client; then
  canary_job_ids_sql="$(sql_in_list_from_job_ids)"
  sql_mode_label="host psql"
  if ! command -v psql >/dev/null 2>&1; then
    sql_mode_label="containerized psql"
  fi
  echo "Running fencing duplicate-claim check via ${sql_mode_label}..."
  sql="
SELECT task_id, attempt, COUNT(*) AS dispatch_count
FROM scheduler_job_transitions
WHERE transition = 'TASK_DISPATCHED'
  AND occurred_at >= to_timestamp(${DRILL_START_EPOCH})
  AND job_id IN (${canary_job_ids_sql})
GROUP BY task_id, attempt
HAVING COUNT(*) > 1
ORDER BY dispatch_count DESC, task_id, attempt;
"
  dupes="$(run_sql_check_must_succeed "${sql}" "duplicate dispatch check")"
  if [[ -n "${dupes}" ]]; then
    echo "Potential duplicate dispatch claims detected:"
    echo "${dupes}"
    fencing_violation=1
  else
    echo "No duplicate TASK_DISPATCHED claims detected for the drill window."
  fi
  echo "Running durable incomplete-task integrity check via ${sql_mode_label}..."
  incomplete_sql="
SELECT t.job_id, t.task_id, t.current_state, t.last_attempt, COALESCE(t.node_id, '')
FROM scheduler_durable_tasks t
JOIN scheduler_durable_jobs j ON j.job_id = t.job_id
WHERE t.job_id IN (${canary_job_ids_sql})
  AND j.current_state NOT IN ('SUCCEEDED', 'FAILED', 'CANCELED')
  AND t.current_state NOT IN ('SUCCEEDED', 'FAILED')
ORDER BY job_id, task_id;
"
  incomplete="$(run_sql_check_must_succeed "${incomplete_sql}" "durable integrity check")"
  if [[ -n "${incomplete}" ]]; then
    echo "Potential durable integrity gap detected (non-terminal task rows for canary jobs):"
    echo "${incomplete}"
    integrity_violation=1
  else
    echo "No incomplete durable tasks detected for canary jobs."
  fi
elif [[ -n "${DATABASE_URL}" ]]; then
  echo "FAIL: DATABASE_URL was set but neither psql nor docker is available for mandatory SQL checks."
  exit 1
else
  echo "DATABASE_URL not set; skipping SQL fence check."
fi

if [[ ${failed_terminal} -ne 0 ]]; then
  if [[ -n "${DATABASE_URL}" ]] && [[ ${#timed_out_job_ids[@]} -gt 0 ]]; then
    echo "Collecting durable diagnostics for timed-out jobs..."
    for timed_out_job_id in "${timed_out_job_ids[@]}"; do
      echo "--- timed_out_job_id=${timed_out_job_id} durable_job ---"
      run_sql_check "
        SELECT job_id, current_state, last_sequence_id, created_at, updated_at
        FROM scheduler_durable_jobs
        WHERE job_id = '${timed_out_job_id}';
      " || true

      echo "--- timed_out_job_id=${timed_out_job_id} durable_tasks ---"
      run_sql_check "
        SELECT task_id, current_state, last_attempt, node_id, updated_at
        FROM scheduler_durable_tasks
        WHERE job_id = '${timed_out_job_id}'
        ORDER BY task_id;
      " || true

      echo "--- timed_out_job_id=${timed_out_job_id} transitions ---"
      run_sql_check "
        SELECT sequence_id, entity_type, transition, previous_state, new_state, task_id, node_id, attempt, occurred_at
        FROM scheduler_job_transitions
        WHERE job_id = '${timed_out_job_id}'
        ORDER BY sequence_id;
      " || true
    done
  fi
  echo "FAIL: one or more canary jobs did not reach a terminal state before timeout."
  exit 1
fi
if [[ ${failed_success} -ne 0 ]]; then
  echo "FAIL: one or more canary jobs reached a non-success terminal state."
  printf '  %s\n' "${unsuccessful_job_ids[@]}"
  exit 1
fi
if [[ ${fencing_violation} -ne 0 ]]; then
  echo "FAIL: duplicate dispatch claims found. Investigate fencing behavior."
  exit 1
fi
if [[ ${integrity_violation} -ne 0 ]]; then
  echo "FAIL: incomplete durable task state found for one or more canary jobs."
  exit 1
fi

echo "PASS: failover drill checks completed."
