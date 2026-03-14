#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://localhost:18083}"
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS:-180}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-2}"
DATABASE_URL="${DATABASE_URL:-${SCHEDULER_TRANSITION_POSTGRES_DSN:-}}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/ha/docker-compose.ha.yml}"
LEADER_STOP_CMD="${LEADER_STOP_CMD:-}"
DRILL_ID="${DRILL_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
EVIDENCE_ROOT="${EVIDENCE_ROOT:-/tmp/dagens-hitl-ha-evidence-${DRILL_ID}}"

command -v curl >/dev/null 2>&1 || { echo "curl is required"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required"; exit 1; }

run_sql_check() {
  local sql="$1"

  if command -v psql >/dev/null 2>&1; then
    psql "${DATABASE_URL}" -At -F '|' -c "${sql}"
    return $?
  fi

  if command -v docker >/dev/null 2>&1; then
    local docker_network_args=()
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

resolve_local_leader_stop_cmd() {
  local etcd_cid=""
  local api_a_cid=""
  local api_b_cid=""
  local leader_id=""

  etcd_cid="$(docker compose -f "${COMPOSE_FILE}" ps -q etcd)"
  api_a_cid="$(docker compose -f "${COMPOSE_FILE}" ps -q api-server)"
  api_b_cid="$(docker compose -f "${COMPOSE_FILE}" ps -q api-server-b)"
  leader_id="$(docker run --rm --network "container:${etcd_cid}" quay.io/coreos/etcd:v3.5.15 \
    etcdctl --endpoints=http://127.0.0.1:2379 get --prefix /dagens/control-plane/scheduler \
    | awk 'NR % 2 == 0 { print; exit }')"

  case "${leader_id}" in
    api-a)
      echo "docker kill ${api_a_cid}"
      ;;
    api-b)
      echo "docker kill ${api_b_cid}"
      ;;
    *)
      echo ""
      ;;
  esac
}

capture_diagnostics() {
  local out_dir="$1"
  mkdir -p "${out_dir}"
  if command -v docker >/dev/null 2>&1; then
    docker compose -f "${COMPOSE_FILE}" ps > "${out_dir}/compose-ps.txt" || true
    docker compose -f "${COMPOSE_FILE}" logs --no-color > "${out_dir}/compose-logs.txt" || true
  fi
  curl -sS "${API_URL}/health" > "${out_dir}/lb-health.json" || true
}

mkdir -p "${EVIDENCE_ROOT}"

if [[ -z "${LEADER_STOP_CMD}" ]] && command -v docker >/dev/null 2>&1; then
  LEADER_STOP_CMD="$(resolve_local_leader_stop_cmd)"
fi
if [[ -z "${LEADER_STOP_CMD}" ]]; then
  echo "LEADER_STOP_CMD is required (or run in the local HA compose topology)."
  exit 1
fi

echo "== Dagens HITL HA Failover Drill =="
echo "API_URL=${API_URL}"
echo "DRILL_TIMEOUT_SECONDS=${DRILL_TIMEOUT_SECONDS}"
echo "POLL_INTERVAL_SECONDS=${POLL_INTERVAL_SECONDS}"
echo "DRILL_ID=${DRILL_ID}"
echo "EVIDENCE_ROOT=${EVIDENCE_ROOT}"
echo "LEADER_STOP_CMD is set"

echo "Creating HITL drill job..."
create_resp="$(curl -sS -X POST "${API_URL}/api/dev/hitl-failover-drill")"
echo "${create_resp}" > "${EVIDENCE_ROOT}/create-response.json"
job_id="$(echo "${create_resp}" | jq -r '.job_id // empty')"
request_id="$(echo "${create_resp}" | jq -r '.request_id // empty')"
callback_url="$(echo "${create_resp}" | jq -r '.callback_url // empty')"

if [[ -z "${job_id}" || -z "${request_id}" || -z "${callback_url}" ]]; then
  echo "Failed to parse drill response: ${create_resp}"
  exit 1
fi

echo "job_id=${job_id}" | tee "${EVIDENCE_ROOT}/drill-ids.txt"
echo "request_id=${request_id}" >> "${EVIDENCE_ROOT}/drill-ids.txt"
echo "callback_url=${callback_url}" >> "${EVIDENCE_ROOT}/drill-ids.txt"

deadline=$(( $(date +%s) + DRILL_TIMEOUT_SECONDS ))
awaiting_human=0
while [[ $(date +%s) -lt ${deadline} ]]; do
  status_resp="$(curl -sS "${API_URL}/v1/jobs/${job_id}" || true)"
  echo "${status_resp}" > "${EVIDENCE_ROOT}/job-status.json"
  status="$(echo "${status_resp}" | jq -r '.status // .Status // .state // .State // empty' 2>/dev/null || true)"
  if [[ "${status}" == "AWAITING_HUMAN" ]]; then
    awaiting_human=1
    break
  fi
  sleep "${POLL_INTERVAL_SECONDS}"
done

if [[ ${awaiting_human} -ne 1 ]]; then
  echo "FAIL: job did not reach AWAITING_HUMAN before timeout."
  capture_diagnostics "${EVIDENCE_ROOT}/failure"
  exit 1
fi

echo "Triggering leader failover..."
bash -lc "${LEADER_STOP_CMD}"

callback_payload='{"selected_option":"approve"}'
first_callback_code="$(curl -sS -o "${EVIDENCE_ROOT}/callback-first.body" -w "%{http_code}" -X POST "${callback_url}" -H "Content-Type: application/json" -d "${callback_payload}")"
second_callback_code="$(curl -sS -o "${EVIDENCE_ROOT}/callback-second.body" -w "%{http_code}" -X POST "${callback_url}" -H "Content-Type: application/json" -d "${callback_payload}")"
printf 'first=%s\nsecond=%s\n' "${first_callback_code}" "${second_callback_code}" | tee "${EVIDENCE_ROOT}/callback-status-codes.txt"

case "${first_callback_code}" in
  200|202) ;;
  *)
    echo "FAIL: first callback status=${first_callback_code}, want 200 or 202"
    capture_diagnostics "${EVIDENCE_ROOT}/failure"
    exit 1
    ;;
esac
case "${second_callback_code}" in
  200|202) ;;
  *)
    echo "FAIL: second callback status=${second_callback_code}, want 200 or 202"
    capture_diagnostics "${EVIDENCE_ROOT}/failure"
    exit 1
    ;;
esac

terminal=""
while [[ $(date +%s) -lt ${deadline} ]]; do
  status_resp="$(curl -sS "${API_URL}/v1/jobs/${job_id}" || true)"
  echo "${status_resp}" > "${EVIDENCE_ROOT}/job-status-final.json"
  status="$(echo "${status_resp}" | jq -r '.status // .Status // .state // .State // empty' 2>/dev/null || true)"
  case "${status}" in
    COMPLETED|SUCCEEDED)
      terminal="${status}"
      break
      ;;
    FAILED|CANCELED)
      echo "FAIL: HITL drill ended in unexpected terminal state ${status}"
      capture_diagnostics "${EVIDENCE_ROOT}/failure"
      exit 1
      ;;
    *)
      ;;
  esac
  sleep "${POLL_INTERVAL_SECONDS}"
done

if [[ -z "${terminal}" ]]; then
  echo "FAIL: HITL drill job did not reach successful terminal state before timeout."
  capture_diagnostics "${EVIDENCE_ROOT}/failure"
  exit 1
fi

if [[ -n "${DATABASE_URL}" ]]; then
  awaiting_count="$(run_sql_check_must_succeed "SELECT COUNT(*) FROM scheduler_job_transitions WHERE job_id = '${job_id}' AND transition = 'JOB_AWAITING_HUMAN';" "awaiting-human count")"
  resumed_count="$(run_sql_check_must_succeed "SELECT COUNT(*) FROM scheduler_job_transitions WHERE job_id = '${job_id}' AND transition = 'JOB_RESUMED';" "resumed count")"
  succeeded_count="$(run_sql_check_must_succeed "SELECT COUNT(*) FROM scheduler_job_transitions WHERE job_id = '${job_id}' AND transition = 'JOB_SUCCEEDED';" "succeeded count")"
  checkpoint_count="$(run_sql_check_must_succeed "SELECT COUNT(*) FROM hitl_execution_checkpoints WHERE request_id = '${request_id}';" "checkpoint cleanup")"

  printf 'awaiting_human=%s\njob_resumed=%s\njob_succeeded=%s\ncheckpoint_rows=%s\n' \
    "${awaiting_count}" "${resumed_count}" "${succeeded_count}" "${checkpoint_count}" \
    | tee "${EVIDENCE_ROOT}/sql-summary.txt"

  if [[ "${awaiting_count}" != "1" ]]; then
    echo "FAIL: JOB_AWAITING_HUMAN count=${awaiting_count}, want 1"
    capture_diagnostics "${EVIDENCE_ROOT}/failure"
    exit 1
  fi
  if [[ "${resumed_count}" != "1" ]]; then
    echo "FAIL: JOB_RESUMED count=${resumed_count}, want 1"
    capture_diagnostics "${EVIDENCE_ROOT}/failure"
    exit 1
  fi
  if [[ "${succeeded_count}" != "1" ]]; then
    echo "FAIL: JOB_SUCCEEDED count=${succeeded_count}, want 1"
    capture_diagnostics "${EVIDENCE_ROOT}/failure"
    exit 1
  fi
  if [[ "${checkpoint_count}" != "0" ]]; then
    echo "FAIL: checkpoint row count=${checkpoint_count}, want 0"
    capture_diagnostics "${EVIDENCE_ROOT}/failure"
    exit 1
  fi
fi

capture_diagnostics "${EVIDENCE_ROOT}/final"
echo "PASS: HITL HA failover drill completed."
echo "job_id=${job_id}"
echo "request_id=${request_id}"
echo "evidence_root=${EVIDENCE_ROOT}"
