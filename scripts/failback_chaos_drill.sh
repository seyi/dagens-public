#!/usr/bin/env bash
set -euo pipefail

# HA compose failback drill that verifies follower catch-up before reclaiming
# leadership back to the former primary.

API_URL="${API_URL:-http://localhost:18083}"
API_A_URL="${API_A_URL:-http://localhost:18081}"
API_B_URL="${API_B_URL:-http://localhost:18082}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/ha/docker-compose.ha.yml}"
SCHEDULER_LEADERSHIP_KEY="${SCHEDULER_LEADERSHIP_KEY:-/dagens/control-plane/scheduler}"
ETCD_SERVICE="${ETCD_SERVICE:-etcd}"
API_A_SERVICE="${API_A_SERVICE:-api-server}"
API_B_SERVICE="${API_B_SERVICE:-api-server-b}"
API_A_ID="${API_A_ID:-api-a}"
API_B_ID="${API_B_ID:-api-b}"
DRILL_ID="${DRILL_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
DATABASE_URL="${DATABASE_URL:-${SCHEDULER_TRANSITION_POSTGRES_DSN:-}}"
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS:-180}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-2}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-240}"
FAILBACK_SETTLE_SECONDS="${FAILBACK_SETTLE_SECONDS:-5}"
MAX_RECONCILE_AGE_SECONDS="${MAX_RECONCILE_AGE_SECONDS:-30}"
READINESS_TIMEOUT_SECONDS="${READINESS_TIMEOUT_SECONDS:-180}"
EVIDENCE_ROOT="${EVIDENCE_ROOT:-/tmp/dagens-failback-chaos-${DRILL_ID}}"
METRICS_NAMESPACE="${METRICS_NAMESPACE:-dagens}"
RESULT_STATUS="FAIL"

command -v bash >/dev/null 2>&1 || { echo "bash is required"; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "docker is required"; exit 1; }

compose() {
  docker compose -f "${COMPOSE_FILE}" "$@"
}

leader_url() {
  case "$1" in
    "${API_A_ID}") printf '%s' "${API_A_URL}" ;;
    "${API_B_ID}") printf '%s' "${API_B_URL}" ;;
    *)
      echo "Unknown control-plane id: $1" >&2
      return 1
      ;;
  esac
}

leader_service() {
  case "$1" in
    "${API_A_ID}") printf '%s' "${API_A_SERVICE}" ;;
    "${API_B_ID}") printf '%s' "${API_B_SERVICE}" ;;
    *)
      echo "Unknown control-plane id: $1" >&2
      return 1
      ;;
  esac
}

wait_for_http_health() {
  local url="$1"
  local timeout="${2:-${HEALTH_TIMEOUT_SECONDS}}"
  local deadline=$(( $(date +%s) + timeout ))
  until curl -fsS "${url}/health" >/dev/null; do
    if [[ $(date +%s) -ge ${deadline} ]]; then
      echo "Timed out waiting for health endpoint: ${url}/health"
      return 1
    fi
    sleep 3
  done
}

restore_topology() {
  compose up -d "${API_A_SERVICE}" "${API_B_SERVICE}" >/dev/null
  wait_for_http_health "${API_A_URL}" "${HEALTH_TIMEOUT_SECONDS}"
  wait_for_http_health "${API_B_URL}" "${HEALTH_TIMEOUT_SECONDS}"
  wait_for_http_health "${API_URL}" "${HEALTH_TIMEOUT_SECONDS}"
}

resolve_leader() {
  local etcd_cid=""
  etcd_cid="$(compose ps -q "${ETCD_SERVICE}")"
  if [[ -z "${etcd_cid}" ]]; then
    echo "Unable to resolve etcd container for service=${ETCD_SERVICE}" >&2
    return 1
  fi
  docker run --rm --network "container:${etcd_cid}" quay.io/coreos/etcd:v3.5.15 \
    etcdctl --endpoints=http://127.0.0.1:2379 get --prefix "${SCHEDULER_LEADERSHIP_KEY}" \
    | awk 'NR % 2 == 0 { print; exit }'
}

wait_for_leader() {
  local expected="$1"
  local deadline=$(( $(date +%s) + HEALTH_TIMEOUT_SECONDS ))
  local current=""
  until [[ "${current}" == "${expected}" ]]; do
    current="$(resolve_leader || true)"
    if [[ $(date +%s) -ge ${deadline} ]]; then
      echo "Timed out waiting for leader=${expected} (last=${current})"
      return 1
    fi
    sleep 2
  done
}

metric_value() {
  local url="$1"
  local metric="$2"
  local full_metric="${metric}"
  if [[ "${metric}" != "${METRICS_NAMESPACE}_"* ]]; then
    full_metric="${METRICS_NAMESPACE}_${metric}"
  fi
  curl -fsS "${url}/metrics" | awk -v name="${full_metric}" '$1 == name { print $2; exit }'
}

metric_to_epoch_seconds() {
  local raw="$1"
  awk -v value="${raw}" 'BEGIN {
    if (value == "") {
      exit 1
    }
    printf "%.0f\n", value + 0
  }'
}

wait_for_follower_readiness() {
  local target_id="$1"
  local target_url=""
  local deadline=$(( $(date +%s) + READINESS_TIMEOUT_SECONDS ))
  local reconcile_ts=""
  local reconcile_age=""

  target_url="$(leader_url "${target_id}")"
  wait_for_http_health "${target_url}" "${READINESS_TIMEOUT_SECONDS}"

  until [[ $(date +%s) -ge ${deadline} ]]; do
    reconcile_ts="$(metric_value "${target_url}" "scheduler_last_reconcile_timestamp_seconds" || true)"
    if reconcile_epoch="$(metric_to_epoch_seconds "${reconcile_ts}" 2>/dev/null)"; then
      reconcile_age=$(( $(date +%s) - reconcile_epoch ))
      if (( reconcile_age >= 0 && reconcile_age <= MAX_RECONCILE_AGE_SECONDS )); then
        printf '%s|%s\n' "${target_url}" "${reconcile_epoch}"
        return 0
      fi
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done

  echo "Timed out waiting for follower readiness on ${target_id}" >&2
  return 1
}

kill_service_container() {
  local service="$1"
  local cid=""
  cid="$(compose ps -q "${service}")"
  if [[ -z "${cid}" ]]; then
    echo "No container id found for service=${service}" >&2
    return 1
  fi
  docker kill "${cid}" >/dev/null
}

capture_diagnostics() {
  local out_dir="$1"
  mkdir -p "${out_dir}"
  compose ps > "${out_dir}/compose-ps.txt" || true
  compose logs --no-color > "${out_dir}/compose-logs.txt" || true
  curl -sS "${API_URL}/health" > "${out_dir}/lb-health.json" || true
  curl -sS "${API_A_URL}/metrics" > "${out_dir}/api-a-metrics.txt" || true
  curl -sS "${API_B_URL}/metrics" > "${out_dir}/api-b-metrics.txt" || true
}

write_result_artifacts() {
  cat > "${EVIDENCE_ROOT}/result.env" <<EOF
status=${RESULT_STATUS}
evidence_root=${EVIDENCE_ROOT}
initial_leader_id=${initial_leader_id:-}
target_leader_id=${target_leader_id:-}
leader_after_failover=${leader_after_failover:-}
target_url=${target_url:-}
target_reconcile_ts=${target_reconcile_ts:-}
final_leader_id=${final_leader_id:-}
EOF

  cat > "${EVIDENCE_ROOT}/result.json" <<EOF
{
  "status": "${RESULT_STATUS}",
  "evidence_root": "${EVIDENCE_ROOT}",
  "initial_leader_id": "${initial_leader_id:-}",
  "target_leader_id": "${target_leader_id:-}",
  "leader_after_failover": "${leader_after_failover:-}",
  "target_url": "${target_url:-}",
  "target_reconcile_ts": "${target_reconcile_ts:-}",
  "final_leader_id": "${final_leader_id:-}"
}
EOF
}

mkdir -p "${EVIDENCE_ROOT}"
: > "${EVIDENCE_ROOT}/summary.txt"
trap write_result_artifacts EXIT

wait_for_http_health "${API_URL}" "${HEALTH_TIMEOUT_SECONDS}"
wait_for_http_health "${API_A_URL}" "${HEALTH_TIMEOUT_SECONDS}"
wait_for_http_health "${API_B_URL}" "${HEALTH_TIMEOUT_SECONDS}"

initial_leader_id="$(resolve_leader)"
if [[ -z "${initial_leader_id}" ]]; then
  echo "Unable to resolve initial leader id"
  exit 1
fi
target_leader_id="${FAILBACK_TARGET_LEADER_ID:-${initial_leader_id}}"
target_service="$(leader_service "${target_leader_id}")"
initial_leader_service="$(leader_service "${initial_leader_id}")"

echo "initial_leader_id=${initial_leader_id}" | tee -a "${EVIDENCE_ROOT}/summary.txt"
echo "target_leader_id=${target_leader_id}" | tee -a "${EVIDENCE_ROOT}/summary.txt"

echo "[phase-1] validating failover away from ${initial_leader_id}"
API_URL="${API_URL}" \
DATABASE_URL="${DATABASE_URL}" \
JOB_COUNT="${FAILOVER_JOB_COUNT:-3}" \
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS}" \
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS}" \
DRILL_ID="${DRILL_ID}-failover" \
LEADER_STOP_CMD="docker kill $(compose ps -q "${initial_leader_service}")" \
./scripts/failover_drill.sh | tee "${EVIDENCE_ROOT}/phase-1-failover.log" >/dev/null

leader_after_failover="$(resolve_leader)"
echo "leader_after_failover=${leader_after_failover}" | tee -a "${EVIDENCE_ROOT}/summary.txt"

echo "[phase-2] restoring ${target_leader_id} as follower and waiting for readiness"
compose up -d "${target_service}" >/dev/null
readiness_info="$(wait_for_follower_readiness "${target_leader_id}")"
target_url="${readiness_info%%|*}"
target_reconcile_ts="${readiness_info##*|}"
echo "target_url=${target_url}" | tee -a "${EVIDENCE_ROOT}/summary.txt"
echo "target_reconcile_ts=${target_reconcile_ts}" | tee -a "${EVIDENCE_ROOT}/summary.txt"

echo "[phase-3] reclaiming leadership to ${target_leader_id}"
current_leader_id="$(resolve_leader)"
if [[ "${current_leader_id}" != "${target_leader_id}" ]]; then
  current_leader_service="$(leader_service "${current_leader_id}")"
  kill_service_container "${current_leader_service}"
fi
wait_for_leader "${target_leader_id}"

if [[ "${FAILBACK_SETTLE_SECONDS}" != "0" ]]; then
  sleep "${FAILBACK_SETTLE_SECONDS}"
fi

echo "[phase-4] validating post-failback continuity"
API_URL="${API_URL}" \
DATABASE_URL="${DATABASE_URL}" \
JOB_COUNT="${POST_FAILBACK_JOB_COUNT:-3}" \
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS}" \
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS}" \
DRILL_ID="${DRILL_ID}-post-failback" \
./scripts/failover_drill.sh | tee "${EVIDENCE_ROOT}/phase-4-post-failback.log" >/dev/null

final_leader_id="$(resolve_leader)"
echo "final_leader_id=${final_leader_id}" | tee -a "${EVIDENCE_ROOT}/summary.txt"

capture_diagnostics "${EVIDENCE_ROOT}/final"

echo "[phase-5] restoring full control-plane topology"
restore_topology
capture_diagnostics "${EVIDENCE_ROOT}/restored-topology"

RESULT_STATUS="PASS"
echo "PASS: failback chaos drill completed"
echo "evidence_root=${EVIDENCE_ROOT}"
