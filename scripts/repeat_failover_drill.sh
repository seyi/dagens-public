#!/usr/bin/env bash
set -euo pipefail

# Repeatable HA failover drill harness for Dagens.
#
# This script:
# 1) ensures the dual-control-plane HA stack is healthy
# 2) runs repeated leader-loss failover drills
# 3) restores the killed leader between runs
# 4) optionally runs one higher-load drill
# 5) archives evidence per run under EVIDENCE_ROOT
#
# Expected topology:
# - etcd-backed leadership
# - compose services: etcd, api-server, api-server-b
#
# Usage:
#   API_URL=http://localhost:18083 \
#   DATABASE_URL="postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable" \
#   ./scripts/repeat_failover_drill.sh

API_URL="${API_URL:-http://localhost:18083}"
DATABASE_URL="${DATABASE_URL:-${SCHEDULER_TRANSITION_POSTGRES_DSN:-}}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/ha/docker-compose.ha.yml}"
ETCD_SERVICE="${ETCD_SERVICE:-etcd}"
API_A_SERVICE="${API_A_SERVICE:-api-server}"
API_B_SERVICE="${API_B_SERVICE:-api-server-b}"
REPEAT_RUNS="${REPEAT_RUNS:-3}"
REPEAT_JOB_COUNT="${REPEAT_JOB_COUNT:-3}"
HIGH_LOAD_JOB_COUNT="${HIGH_LOAD_JOB_COUNT:-20}"
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS:-180}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-2}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-240}"
SETTLE_SECONDS="${SETTLE_SECONDS:-5}"
DRILL_ID="${DRILL_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
EVIDENCE_ROOT="${EVIDENCE_ROOT:-/tmp/dagens-ha-evidence-repeat-${DRILL_ID}}"

command -v curl >/dev/null 2>&1 || { echo "curl is required"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "docker is required"; exit 1; }

compose() {
  docker compose -f "${COMPOSE_FILE}" "$@"
}

run_sql_probe() {
  if [[ -z "${DATABASE_URL}" ]]; then
    return 0
  fi

  if command -v psql >/dev/null 2>&1; then
    PGPASSWORD="${PGPASSWORD:-}" psql "${DATABASE_URL}" -At -c "SELECT 1;" >/dev/null
    return $?
  fi

  if command -v docker >/dev/null 2>&1; then
    local docker_network_args=()
    if [[ "${DATABASE_URL}" == *"@localhost"* || "${DATABASE_URL}" == *"@127.0.0.1"* ]]; then
      docker_network_args=(--network host)
    fi
    docker run --rm "${docker_network_args[@]}" postgres:15-alpine \
      psql "${DATABASE_URL}" -At -c "SELECT 1;" >/dev/null
    return $?
  fi

  return 127
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

  until [[ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${cid}")" == "healthy" ]]; do
    if [[ $(date +%s) -ge ${deadline} ]]; then
      echo "Timed out waiting for healthy service=${service} cid=${cid}"
      docker inspect "${cid}" || true
      return 1
    fi
    sleep 3
  done
}

wait_for_sql_ready() {
  local deadline=$(( $(date +%s) + HEALTH_TIMEOUT_SECONDS ))

  if [[ -z "${DATABASE_URL}" ]]; then
    return 0
  fi

  until run_sql_probe >/dev/null 2>&1; do
    if [[ $(date +%s) -ge ${deadline} ]]; then
      echo "Timed out waiting for SQL readiness for DATABASE_URL."
      run_sql_probe || true
      return 1
    fi
    sleep 3
  done
}

wait_for_topology_ready() {
  wait_for_service_health "${ETCD_SERVICE}"
  wait_for_service_health "${API_A_SERVICE}"
  wait_for_service_health "${API_B_SERVICE}"
  wait_for_http_health "${API_URL}/health"
  wait_for_sql_ready
}

resolve_leader() {
  local etcd_cid="$1"
  docker run --rm --network "container:${etcd_cid}" quay.io/coreos/etcd:v3.5.15 \
    etcdctl --endpoints=http://127.0.0.1:2379 get --prefix /dagens/control-plane/scheduler \
    | awk 'NR % 2 == 0 { print; exit }'
}

capture_run_diagnostics() {
  local out_dir="$1"
  mkdir -p "${out_dir}"
  compose ps > "${out_dir}/compose-ps.txt" || true
  compose logs --no-color > "${out_dir}/compose-logs.txt" || true
  curl -sS "${API_URL}/health" > "${out_dir}/lb-health.json" || true
  if [[ -n "${DATABASE_URL}" ]]; then
    run_sql_probe > "${out_dir}/sql-ready.txt" 2>&1 || true
  fi
}

run_single_drill() {
  local run_label="$1"
  local job_count="$2"
  local run_dir="${EVIDENCE_ROOT}/${run_label}"
  local etcd_cid=""
  local api_a_cid=""
  local api_b_cid=""
  local leader_id=""
  local leader_stop_cmd=""

  mkdir -p "${run_dir}"

  wait_for_topology_ready

  etcd_cid="$(compose ps -q "${ETCD_SERVICE}")"
  api_a_cid="$(compose ps -q "${API_A_SERVICE}")"
  api_b_cid="$(compose ps -q "${API_B_SERVICE}")"
  leader_id="$(resolve_leader "${etcd_cid}")"

  case "${leader_id}" in
    api-a)
      leader_stop_cmd="docker kill ${api_a_cid}"
      ;;
    api-b)
      leader_stop_cmd="docker kill ${api_b_cid}"
      ;;
    *)
      echo "Unable to resolve leader id from etcd (got: ${leader_id})"
      return 1
      ;;
  esac

  {
    echo "run_label=${run_label}"
    echo "job_count=${job_count}"
    echo "leader_id=${leader_id}"
    echo "leader_stop_cmd=${leader_stop_cmd}"
    echo "drill_id=${DRILL_ID}-${run_label}"
  } > "${run_dir}/leader-resolution.txt"

  if ! API_URL="${API_URL}" \
    DATABASE_URL="${DATABASE_URL}" \
    JOB_COUNT="${job_count}" \
    DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS}" \
    POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS}" \
    DRILL_ID="${DRILL_ID}-${run_label}" \
    LEADER_STOP_CMD="${leader_stop_cmd}" \
    ./scripts/failover_drill.sh | tee "${run_dir}/failover-drill.log"; then
    capture_run_diagnostics "${run_dir}"
    return 1
  fi

  capture_run_diagnostics "${run_dir}"

  echo "${run_label}|PASS|job_count=${job_count}|leader_id=${leader_id}" >> "${EVIDENCE_ROOT}/summary.txt"
}

restore_topology() {
  compose up -d "${API_A_SERVICE}" "${API_B_SERVICE}" >/dev/null
  if [[ "${SETTLE_SECONDS}" != "0" ]]; then
    sleep "${SETTLE_SECONDS}"
  fi
  wait_for_topology_ready
}

echo "== Dagens HA Repeat Failover Drill =="
echo "API_URL=${API_URL}"
echo "REPEAT_RUNS=${REPEAT_RUNS}"
echo "REPEAT_JOB_COUNT=${REPEAT_JOB_COUNT}"
echo "HIGH_LOAD_JOB_COUNT=${HIGH_LOAD_JOB_COUNT}"
echo "DRILL_TIMEOUT_SECONDS=${DRILL_TIMEOUT_SECONDS}"
echo "EVIDENCE_ROOT=${EVIDENCE_ROOT}"

mkdir -p "${EVIDENCE_ROOT}"
: > "${EVIDENCE_ROOT}/summary.txt"

for run_index in $(seq 1 "${REPEAT_RUNS}"); do
  run_label="repeat-${run_index}"
  echo "[${run_label}] starting"
  run_single_drill "${run_label}" "${REPEAT_JOB_COUNT}"
  if [[ "${run_index}" -lt "${REPEAT_RUNS}" || "${HIGH_LOAD_JOB_COUNT}" != "0" ]]; then
    echo "[${run_label}] restoring topology"
    restore_topology
  fi
done

if [[ "${HIGH_LOAD_JOB_COUNT}" != "0" ]]; then
  run_label="high-load"
  echo "[${run_label}] starting"
  run_single_drill "${run_label}" "${HIGH_LOAD_JOB_COUNT}"
fi

echo "[final] restoring topology"
restore_topology
capture_run_diagnostics "${EVIDENCE_ROOT}/final-topology"

echo "PASS: repeated failover drill checks completed."
echo "Evidence root: ${EVIDENCE_ROOT}"
