#!/usr/bin/env bash
set -euo pipefail

# Backpressure + failover validation drill for the HA compose topology.
#
# This script:
# 1) reapplies the HA stack with drill-specific queue/capacity settings
# 2) forces admission saturation through the LB until a 429 is observed
# 3) kills the current scheduler leader during the overload window
# 4) verifies leadership changes and LB-routed submissions recover to 202
# 5) captures evidence showing Retry-After behavior and post-failover recovery

API_URL="${API_URL:-http://localhost:18083}"
API_A_URL="${API_A_URL:-http://localhost:18081}"
API_B_URL="${API_B_URL:-http://localhost:18082}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/ha/docker-compose.ha.yml}"
SCHEDULER_LEADERSHIP_KEY="${SCHEDULER_LEADERSHIP_KEY:-/dagens/control-plane/scheduler}"
ETCD_SERVICE="${ETCD_SERVICE:-etcd}"
API_A_SERVICE="${API_A_SERVICE:-api-server}"
API_B_SERVICE="${API_B_SERVICE:-api-server-b}"
WORKER_A_SERVICE="${WORKER_A_SERVICE:-worker-1}"
WORKER_B_SERVICE="${WORKER_B_SERVICE:-worker-2}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-240}"
OVERLOAD_SUBMISSIONS="${OVERLOAD_SUBMISSIONS:-6}"
SIMULATED_SLEEP_MS="${SIMULATED_SLEEP_MS:-4000}"
RECOVERY_TIMEOUT_SECONDS="${RECOVERY_TIMEOUT_SECONDS:-45}"
PROBE_INTERVAL_SECONDS="${PROBE_INTERVAL_SECONDS:-1}"
SETTLE_SECONDS="${SETTLE_SECONDS:-5}"
MAX_CONSECUTIVE_429_AFTER_FAILOVER="${MAX_CONSECUTIVE_429_AFTER_FAILOVER:-10}"
DRILL_ID="${DRILL_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
EVIDENCE_ROOT="${EVIDENCE_ROOT:-/tmp/dagens-backpressure-ha-evidence-${DRILL_ID}}"

# Compose tuning used for this drill.
HA_SCHEDULER_JOB_QUEUE_SIZE="${HA_SCHEDULER_JOB_QUEUE_SIZE:-2}"
HA_WORKER_MAX_CONCURRENCY="${HA_WORKER_MAX_CONCURRENCY:-1}"
HA_SCHEDULER_CAPACITY_TTL="${HA_SCHEDULER_CAPACITY_TTL:-5s}"
HA_SCHEDULER_ENABLE_STAGE_CAPACITY_DEFERRAL="${HA_SCHEDULER_ENABLE_STAGE_CAPACITY_DEFERRAL:-false}"

command -v curl >/dev/null 2>&1 || { echo "curl is required"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "docker is required"; exit 1; }

TMP_DIR="$(mktemp -d)"
TOPOLOGY_RESTORE_NEEDED=0
cleanup() {
  local exit_code=$?
  if [[ ${exit_code} -ne 0 && ${TOPOLOGY_RESTORE_NEEDED} -eq 1 ]]; then
    echo "[cleanup] restoring topology after failure"
    restore_topology || true
    capture_diagnostics || true
  fi
  rm -rf "${TMP_DIR}"
  exit "${exit_code}"
}
trap cleanup EXIT

compose() {
  docker compose -f "${COMPOSE_FILE}" "$@"
}

wait_for_http_health() {
  local url="$1"
  local deadline=$(( $(date +%s) + HEALTH_TIMEOUT_SECONDS ))
  until curl -fsS "${url}" >/dev/null; do
    if [[ $(date +%s) -ge ${deadline} ]]; then
      echo "Timed out waiting for health endpoint: ${url}"
      return 1
    fi
    sleep 3
  done
}

wait_for_service_health() {
  local service="$1"
  local cid=""
  local deadline=$(( $(date +%s) + HEALTH_TIMEOUT_SECONDS ))
  local status=""

  until [[ -n "${cid}" ]]; do
    cid="$(compose ps -q "${service}")"
    if [[ -n "${cid}" ]]; then
      break
    fi
    if [[ $(date +%s) -ge ${deadline} ]]; then
      echo "Timed out waiting for container id for service=${service}"
      return 1
    fi
    sleep 2
  done

  until [[ $(date +%s) -ge ${deadline} ]]; do
    status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${cid}")"
    if [[ "${status}" == "healthy" || "${status}" == "running" ]]; then
      return 0
    fi
    sleep 3
  done

  echo "Timed out waiting for ready service=${service} cid=${cid} status=${status}"
  docker inspect "${cid}" || true
  return 1
}

wait_for_topology_ready() {
  wait_for_service_health "${ETCD_SERVICE}"
  wait_for_service_health "${API_A_SERVICE}"
  wait_for_service_health "${API_B_SERVICE}"
  wait_for_service_health "${WORKER_A_SERVICE}"
  wait_for_service_health "${WORKER_B_SERVICE}"
  wait_for_http_health "${API_URL}/health"
}

resolve_leader() {
  local etcd_cid="$1"
  docker run --rm --network "container:${etcd_cid}" quay.io/coreos/etcd:v3.5.15 \
    etcdctl --endpoints=http://127.0.0.1:2379 get --prefix "${SCHEDULER_LEADERSHIP_KEY}" \
    | awk 'NR % 2 == 0 { print; exit }'
}

wait_for_leader_change() {
  local previous="$1"
  local etcd_cid="$2"
  local deadline=$(( $(date +%s) + HEALTH_TIMEOUT_SECONDS ))
  local current=""

  until [[ -n "${current}" && "${current}" != "${previous}" ]]; do
    current="$(resolve_leader "${etcd_cid}")"
    if [[ $(date +%s) -ge ${deadline} ]]; then
      echo "Timed out waiting for leader change away from ${previous} (last=${current})"
      return 1
    fi
    sleep 2
  done

  printf '%s' "${current}"
}

capture_diagnostics() {
  mkdir -p "${EVIDENCE_ROOT}"
  compose ps > "${EVIDENCE_ROOT}/compose-ps.txt" || true
  compose logs --no-color > "${EVIDENCE_ROOT}/compose-logs.txt" || true
  curl -sS "${API_URL}/metrics" > "${EVIDENCE_ROOT}/lb-metrics.txt" || true
  curl -sS "${API_A_URL}/metrics" > "${EVIDENCE_ROOT}/api-a-metrics.txt" || true
  curl -sS "${API_B_URL}/metrics" > "${EVIDENCE_ROOT}/api-b-metrics.txt" || true
}

submit_job() {
  local sleep_ms="$1"
  local suffix="$2"
  local body_file="${TMP_DIR}/body-${suffix}.json"
  local headers_file="${TMP_DIR}/headers-${suffix}.txt"
  local payload_file="${TMP_DIR}/payload-${suffix}.json"
  local http_code=""
  local retry_after=""
  local job_id=""

  cat > "${payload_file}" <<JSON
{
  "name": "backpressure-${DRILL_ID}-${suffix}",
  "nodes": [
    {"id": "start", "type": "function", "name": "Start"},
    {"id": "end", "type": "function", "name": "End"}
  ],
  "edges": [{"from": "start", "to": "end"}],
  "entry_node": "start",
  "finish_nodes": ["end"],
  "input": {
    "instruction": "backpressure drill ${suffix}",
    "data": {"dagens_simulated_sleep_ms": ${sleep_ms}}
  }
}
JSON

  http_code="$(curl -sS -D "${headers_file}" -o "${body_file}" -w "%{http_code}" \
    -X POST "${API_URL}/v1/jobs" \
    -H "Content-Type: application/json" \
    --data @"${payload_file}" || true)"
  retry_after="$(awk 'BEGIN{IGNORECASE=1} /^Retry-After:/ {gsub("\r","",$2); print $2}' "${headers_file}" | tail -n1)"
  job_id="$(jq -r '.job_id // .id // empty' "${body_file}" 2>/dev/null || true)"

  printf '%s|%s|%s|%s|%s\n' "${http_code}" "${retry_after}" "${job_id}" "${body_file}" "${headers_file}"
}

wait_for_job_terminal() {
  local job_id="$1"
  local timeout_seconds="$2"
  local deadline=$(( $(date +%s) + timeout_seconds ))
  local status=""

  until [[ $(date +%s) -ge ${deadline} ]]; do
    status="$(curl -sS "${API_URL}/v1/jobs/${job_id}" | jq -r '.status // .Status // .state // .State // empty' 2>/dev/null || true)"
    case "${status}" in
      COMPLETED|SUCCEEDED|FAILED|CANCELED)
        printf '%s' "${status}"
        return 0
        ;;
    esac
    sleep "${PROBE_INTERVAL_SECONDS}"
  done

  return 1
}

restore_topology() {
  compose up -d "${API_A_SERVICE}" "${API_B_SERVICE}" >/dev/null
  sleep "${SETTLE_SECONDS}"
  wait_for_topology_ready
  TOPOLOGY_RESTORE_NEEDED=0
}

apply_drill_stack_tuning() {
  export HA_SCHEDULER_JOB_QUEUE_SIZE
  export HA_WORKER_MAX_CONCURRENCY
  export HA_SCHEDULER_CAPACITY_TTL
  export HA_SCHEDULER_ENABLE_STAGE_CAPACITY_DEFERRAL

  compose up -d --build \
    "${ETCD_SERVICE}" postgres redis \
    "${API_A_SERVICE}" "${API_B_SERVICE}" api-lb \
    "${WORKER_A_SERVICE}" "${WORKER_B_SERVICE}" >/dev/null
}

echo "== Dagens HA Backpressure Failover Drill =="
echo "API_URL=${API_URL}"
echo "DRILL_ID=${DRILL_ID}"
echo "EVIDENCE_ROOT=${EVIDENCE_ROOT}"
echo "HA_SCHEDULER_JOB_QUEUE_SIZE=${HA_SCHEDULER_JOB_QUEUE_SIZE}"
echo "HA_WORKER_MAX_CONCURRENCY=${HA_WORKER_MAX_CONCURRENCY}"
echo "SIMULATED_SLEEP_MS=${SIMULATED_SLEEP_MS}"

mkdir -p "${EVIDENCE_ROOT}"
: > "${EVIDENCE_ROOT}/summary.txt"

echo "[setup] applying drill-specific compose tuning"
apply_drill_stack_tuning
wait_for_topology_ready

etcd_cid="$(compose ps -q "${ETCD_SERVICE}")"
leader_id="$(resolve_leader "${etcd_cid}")"
api_a_cid="$(compose ps -q "${API_A_SERVICE}")"
api_b_cid="$(compose ps -q "${API_B_SERVICE}")"

if [[ -z "${api_a_cid}" ]]; then
  echo "FAIL: could not resolve container id for ${API_A_SERVICE}"
  capture_diagnostics
  exit 1
fi
if [[ -z "${api_b_cid}" ]]; then
  echo "FAIL: could not resolve container id for ${API_B_SERVICE}"
  capture_diagnostics
  exit 1
fi

case "${leader_id}" in
  api-a)
    leader_stop_cmd="docker kill ${api_a_cid}"
    ;;
  api-b)
    leader_stop_cmd="docker kill ${api_b_cid}"
    ;;
  *)
    echo "Unable to resolve leader id from etcd (got: ${leader_id})"
    exit 1
    ;;
esac

if ! docker inspect "${api_a_cid}" >/dev/null 2>&1; then
  echo "FAIL: container ${api_a_cid} for ${API_A_SERVICE} does not exist"
  capture_diagnostics
  exit 1
fi
if ! docker inspect "${api_b_cid}" >/dev/null 2>&1; then
  echo "FAIL: container ${api_b_cid} for ${API_B_SERVICE} does not exist"
  capture_diagnostics
  exit 1
fi

{
  echo "drill_id=${DRILL_ID}"
  echo "leader_id_before=${leader_id}"
  echo "leader_stop_cmd=${leader_stop_cmd}"
  echo "job_queue_size=${HA_SCHEDULER_JOB_QUEUE_SIZE}"
  echo "worker_max_concurrency=${HA_WORKER_MAX_CONCURRENCY}"
  echo "simulated_sleep_ms=${SIMULATED_SLEEP_MS}"
} > "${EVIDENCE_ROOT}/drill-config.txt"

echo "[phase] forcing overload until a 429 is observed"
accepted_job_ids=()
saw_429=0
for idx in $(seq 1 "${OVERLOAD_SUBMISSIONS}"); do
  result="$(submit_job "${SIMULATED_SLEEP_MS}" "overload-${idx}")"
  IFS='|' read -r http_code retry_after job_id body_file headers_file <<< "${result}"
  echo "overload-${idx}|status=${http_code}|retry_after=${retry_after}|job_id=${job_id}" | tee -a "${EVIDENCE_ROOT}/submission-log.txt"
  cp "${body_file}" "${EVIDENCE_ROOT}/response-overload-${idx}.json"
  cp "${headers_file}" "${EVIDENCE_ROOT}/headers-overload-${idx}.txt"

  case "${http_code}" in
    202)
      accepted_job_ids+=("${job_id}")
      ;;
    429)
      saw_429=1
      if [[ "${retry_after}" != "5" ]]; then
        echo "FAIL: expected Retry-After=5 on overload 429, got ${retry_after}"
        exit 1
      fi
      break
      ;;
    *)
      echo "FAIL: unexpected overload submission status=${http_code}"
      exit 1
      ;;
  esac
done

if [[ ${saw_429} -ne 1 ]]; then
  echo "FAIL: overload phase did not produce a 429 response."
  exit 1
fi

echo "[phase] killing leader during overload"
TOPOLOGY_RESTORE_NEEDED=1
bash -lc "${leader_stop_cmd}"
sleep 1
new_leader_id="$(wait_for_leader_change "${leader_id}" "${etcd_cid}")"
echo "leader_id_after=${new_leader_id}" | tee -a "${EVIDENCE_ROOT}/summary.txt"
wait_for_http_health "${API_URL}/health"

echo "[phase] probing for post-failover admission recovery"
post_failover_429s=0
consecutive_429s=0
probe_job_id=""
probe_status=""
deadline=$(( $(date +%s) + RECOVERY_TIMEOUT_SECONDS ))
probe_index=0

until [[ $(date +%s) -ge ${deadline} ]]; do
  probe_index=$((probe_index + 1))
  result="$(submit_job "0" "probe-${probe_index}")"
  IFS='|' read -r http_code retry_after job_id body_file headers_file <<< "${result}"
  echo "probe-${probe_index}|status=${http_code}|retry_after=${retry_after}|job_id=${job_id}" | tee -a "${EVIDENCE_ROOT}/submission-log.txt"
  cp "${body_file}" "${EVIDENCE_ROOT}/response-probe-${probe_index}.json"
  cp "${headers_file}" "${EVIDENCE_ROOT}/headers-probe-${probe_index}.txt"

  case "${http_code}" in
    202)
      probe_job_id="${job_id}"
      probe_status="202"
      break
      ;;
    429)
      post_failover_429s=$((post_failover_429s + 1))
      consecutive_429s=$((consecutive_429s + 1))
      if [[ "${retry_after}" != "5" ]]; then
        echo "FAIL: expected Retry-After=5 on post-failover 429, got ${retry_after}"
        exit 1
      fi
      if [[ ${consecutive_429s} -gt ${MAX_CONSECUTIVE_429_AFTER_FAILOVER} ]]; then
        echo "FAIL: exceeded MAX_CONSECUTIVE_429_AFTER_FAILOVER=${MAX_CONSECUTIVE_429_AFTER_FAILOVER}"
        exit 1
      fi
      ;;
    *)
      echo "FAIL: unexpected probe status=${http_code}"
      exit 1
      ;;
  esac

  sleep "${PROBE_INTERVAL_SECONDS}"
done

if [[ "${probe_status}" != "202" || -z "${probe_job_id}" ]]; then
  echo "FAIL: no post-failover submission recovered to HTTP 202 within ${RECOVERY_TIMEOUT_SECONDS}s."
  exit 1
fi

echo "[phase] waiting for recovered probe job to finish"
terminal_status="$(wait_for_job_terminal "${probe_job_id}" 60 || true)"
if [[ -z "${terminal_status}" ]]; then
  echo "FAIL: recovered probe job ${probe_job_id} did not reach terminal state."
  exit 1
fi

capture_diagnostics
restore_topology

{
  echo "leader_before=${leader_id}"
  echo "leader_after=${new_leader_id}"
  echo "accepted_overload_jobs=${#accepted_job_ids[@]}"
  echo "post_failover_429s=${post_failover_429s}"
  echo "probe_job_id=${probe_job_id}"
  echo "probe_terminal_status=${terminal_status}"
  echo "result=PASS"
} | tee -a "${EVIDENCE_ROOT}/summary.txt"

echo "PASS: backpressure failover drill recovered admission after leader loss."
