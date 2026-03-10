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

command -v curl >/dev/null 2>&1 || { echo "curl is required"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required"; exit 1; }

echo "== Dagens HA Failover Drill =="
echo "API_URL=${API_URL}"
echo "JOB_COUNT=${JOB_COUNT}"
echo "DRILL_TIMEOUT_SECONDS=${DRILL_TIMEOUT_SECONDS}"
echo "POLL_INTERVAL_SECONDS=${POLL_INTERVAL_SECONDS}"
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
  "name": "ha-failover-canary-${i}",
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
  resp="$(curl -sS -X POST "${API_URL}/v1/jobs" -H "Content-Type: application/json" -d "${payload}")"
  job_id="$(echo "${resp}" | jq -r '.job_id // .id // empty')"
  if [[ -z "${job_id}" || "${job_id}" == "null" ]]; then
    echo "Failed to parse job id from response: ${resp}"
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
    status_resp="$(curl -sS "${API_URL}/v1/jobs/${id}")"
    status="$(echo "${status_resp}" | jq -r '.status // .Status // .state // .State // empty')"
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
for id in "${job_ids[@]}"; do
  st="${final_status[${id}]:-TIMEOUT}"
  echo "  ${id}: ${st}"
  if [[ "${st}" == "TIMEOUT" ]]; then
    failed_terminal=1
  fi
done

fencing_violation=0
if [[ -n "${DATABASE_URL}" ]] && command -v psql >/dev/null 2>&1; then
  echo "Running fencing duplicate-claim check..."
  sql="
    SELECT task_id, attempt, COUNT(*) AS dispatch_count
    FROM scheduler_job_transitions
    WHERE transition = 'TASK_DISPATCHED'
      AND occurred_at > NOW() - INTERVAL '30 minutes'
    GROUP BY task_id, attempt
    HAVING COUNT(*) > 1
    ORDER BY dispatch_count DESC, task_id, attempt;
  "
  dupes="$(psql "${DATABASE_URL}" -At -F '|' -c "${sql}" || true)"
  if [[ -n "${dupes}" ]]; then
    echo "Potential duplicate dispatch claims detected:"
    echo "${dupes}"
    fencing_violation=1
  else
    echo "No duplicate TASK_DISPATCHED claims detected for the drill window."
  fi
elif [[ -n "${DATABASE_URL}" ]]; then
  echo "DATABASE_URL was set but psql was not found; skipping SQL fence check."
else
  echo "DATABASE_URL not set; skipping SQL fence check."
fi

if [[ ${failed_terminal} -ne 0 ]]; then
  echo "FAIL: one or more canary jobs did not reach a terminal state before timeout."
  exit 1
fi
if [[ ${fencing_violation} -ne 0 ]]; then
  echo "FAIL: duplicate dispatch claims found. Investigate fencing behavior."
  exit 1
fi

echo "PASS: failover drill checks completed."
