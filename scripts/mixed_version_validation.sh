#!/usr/bin/env bash
set -euo pipefail

# Adjacent-version mixed control-plane/worker validation harness.
#
# This harness intentionally does not build versioned artifacts itself.
# Callers must provide adjacent image tags representing N and N+1.
#
# Required images:
#   MIXED_VERSION_API_IMAGE_A      old/new API image for slot A
#   MIXED_VERSION_API_IMAGE_B      old/new API image for slot B
#   MIXED_VERSION_WORKER_IMAGE_A   old/new worker image for slot A
#   MIXED_VERSION_WORKER_IMAGE_B   old/new worker image for slot B
#
# Default first scenario:
#   api-server   = N
#   api-server-b = N+1
#   worker-1     = N
#   worker-2     = N+1
#
# The script brings up the existing HA topology with version-pinned images,
# then runs the current failover drill as the first adjacent-version gate.

COMPOSE_BASE="${COMPOSE_BASE:-docker-compose.yml}"
COMPOSE_HA="${COMPOSE_HA:-deploy/ha/docker-compose.ha.yml}"
COMPOSE_MIXED="${COMPOSE_MIXED:-deploy/ha/docker-compose.mixed-version.yml}"
API_URL="${API_URL:-http://localhost:18083}"
API_A_URL="${API_A_URL:-http://localhost:${HA_API_A_PORT:-18081}}"
API_B_URL="${API_B_URL:-http://localhost:${HA_API_B_PORT:-18082}}"
DATABASE_URL="${DATABASE_URL:-postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable}"
JOB_COUNT="${JOB_COUNT:-3}"
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS:-180}"
STACK_READY_TIMEOUT_SECONDS="${STACK_READY_TIMEOUT_SECONDS:-180}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-2}"
WORKER_REGISTRY_WAIT_TIMEOUT_SECONDS="${WORKER_REGISTRY_WAIT_TIMEOUT_SECONDS:-60}"
SCHEDULER_READINESS_WAIT_TIMEOUT_SECONDS="${SCHEDULER_READINESS_WAIT_TIMEOUT_SECONDS:-30}"
EXPECTED_HEALTHY_WORKERS="${EXPECTED_HEALTHY_WORKERS:-2}"
EVIDENCE_ROOT="${EVIDENCE_ROOT:-/tmp/dagens-mixed-version-validation-$(date -u +%Y%m%dT%H%M%SZ)}"
FAILED=0

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "error: ${name} is required" >&2
    exit 1
  fi
}

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }

require_env MIXED_VERSION_API_IMAGE_A
require_env MIXED_VERSION_API_IMAGE_B
require_env MIXED_VERSION_WORKER_IMAGE_A
require_env MIXED_VERSION_WORKER_IMAGE_B

mkdir -p "${EVIDENCE_ROOT}"

compose_cmd=(
  docker compose
  -f "${COMPOSE_BASE}"
  -f "${COMPOSE_HA}"
  -f "${COMPOSE_MIXED}"
)

capture_compose_diagnostics() {
  local out_dir="$1"
  mkdir -p "${out_dir}"
  "${compose_cmd[@]}" ps > "${out_dir}/compose-ps.txt" 2>&1 || true
  "${compose_cmd[@]}" logs --no-color > "${out_dir}/compose-logs.txt" 2>&1 || true

  for service in etcd postgres redis api-server api-server-b api-lb worker-1 worker-2; do
    cid="$("${compose_cmd[@]}" ps -q "${service}" 2>/dev/null || true)"
    if [[ -n "${cid}" ]]; then
      printf '%s %s %s\n' \
        "${service}" \
        "${cid}" \
        "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${cid}" 2>/dev/null || echo unknown)" \
        >> "${out_dir}/container-status.txt"
    fi
  done
}

wait_for_service_state() {
  local service="$1"
  local wanted="$2"
  local deadline=$(( $(date +%s) + STACK_READY_TIMEOUT_SECONDS ))

  while [[ $(date +%s) -lt ${deadline} ]]; do
    cid="$("${compose_cmd[@]}" ps -q "${service}" 2>/dev/null || true)"
    if [[ -n "${cid}" ]]; then
      state="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${cid}" 2>/dev/null || true)"
      case "${wanted}" in
        healthy)
          if [[ "${state}" == "healthy" ]]; then
            return 0
          fi
          ;;
        running)
          if [[ "${state}" == "running" || "${state}" == "healthy" ]]; then
            return 0
          fi
          ;;
      esac
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done

  echo "error: service ${service} did not reach ${wanted} within ${STACK_READY_TIMEOUT_SECONDS}s" >&2
  return 1
}

wait_for_worker_registry_entries() {
  local etcd_cid="$1"
  local deadline=$(( $(date +%s) + WORKER_REGISTRY_WAIT_TIMEOUT_SECONDS ))
  local node=""

  while [[ $(date +%s) -lt ${deadline} ]]; do
    local all_ready=1
    for node in worker-1 worker-2; do
      local output
      output="$(
        docker run --rm --network "container:${etcd_cid}" quay.io/coreos/etcd:v3.5.15 \
          etcdctl --endpoints=http://127.0.0.1:2379 get "/dagens/nodes/${node}" 2>/dev/null || true
      )"
      if [[ "${output}" != *"\"healthy\":true"* ]]; then
        all_ready=0
        break
      fi
    done
    if [[ ${all_ready} -eq 1 ]]; then
      return 0
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done

  echo "error: worker registry entries did not become healthy within ${WORKER_REGISTRY_WAIT_TIMEOUT_SECONDS}s" >&2
  return 1
}

wait_for_scheduler_readiness() {
  local base_url="$1"
  local deadline=$(( $(date +%s) + SCHEDULER_READINESS_WAIT_TIMEOUT_SECONDS ))

  while [[ $(date +%s) -lt ${deadline} ]]; do
    local resp=""
    resp="$(curl -fsS "${base_url}/v1/internal/scheduler_readiness" 2>/dev/null || true)"
    if [[ -n "${resp}" ]]; then
      local recovering=""
      local healthy_workers=""
      recovering="$(printf '%s' "${resp}" | jq -r '.IsRecovering // .is_recovering // empty' 2>/dev/null || true)"
      healthy_workers="$(printf '%s' "${resp}" | jq -r '.HealthyWorkerCount // .healthy_worker_count // empty' 2>/dev/null || true)"
      if [[ "${recovering}" == "false" && "${healthy_workers}" =~ ^[0-9]+$ && "${healthy_workers}" -ge "${EXPECTED_HEALTHY_WORKERS}" ]]; then
        return 0
      fi
    fi
    sleep "${POLL_INTERVAL_SECONDS}"
  done

  echo "error: scheduler readiness did not converge for ${base_url} within ${SCHEDULER_READINESS_WAIT_TIMEOUT_SECONDS}s" >&2
  return 1
}

cleanup() {
  if [[ ${FAILED} -ne 0 ]]; then
    capture_compose_diagnostics "${EVIDENCE_ROOT}/failure"
  fi
  "${compose_cmd[@]}" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== Dagens Mixed-Version Validation =="
echo "EVIDENCE_ROOT=${EVIDENCE_ROOT}"
echo "API slot A image=${MIXED_VERSION_API_IMAGE_A}"
echo "API slot B image=${MIXED_VERSION_API_IMAGE_B}"
echo "Worker slot A image=${MIXED_VERSION_WORKER_IMAGE_A}"
echo "Worker slot B image=${MIXED_VERSION_WORKER_IMAGE_B}"

printf '%s\n' \
  "api_a=${MIXED_VERSION_API_IMAGE_A}" \
  "api_b=${MIXED_VERSION_API_IMAGE_B}" \
  "worker_a=${MIXED_VERSION_WORKER_IMAGE_A}" \
  "worker_b=${MIXED_VERSION_WORKER_IMAGE_B}" \
  > "${EVIDENCE_ROOT}/images.env"

"${compose_cmd[@]}" up -d

for service in etcd postgres redis api-server api-server-b api-lb; do
  wait_for_service_state "${service}" healthy || { FAILED=1; exit 1; }
done

for service in worker-1 worker-2; do
  wait_for_service_state "${service}" running || { FAILED=1; exit 1; }
done

capture_compose_diagnostics "${EVIDENCE_ROOT}/initial"

leader_stop_cmd=""
etcd_cid="$("${compose_cmd[@]}" ps -q etcd)"
wait_for_worker_registry_entries "${etcd_cid}" || { FAILED=1; exit 1; }
wait_for_scheduler_readiness "${API_A_URL}" || { FAILED=1; exit 1; }
wait_for_scheduler_readiness "${API_B_URL}" || { FAILED=1; exit 1; }
api_a_cid="$("${compose_cmd[@]}" ps -q api-server)"
api_b_cid="$("${compose_cmd[@]}" ps -q api-server-b)"
leader_id="$(
  docker run --rm --network "container:${etcd_cid}" quay.io/coreos/etcd:v3.5.15 \
    etcdctl --endpoints=http://127.0.0.1:2379 get --prefix /dagens/control-plane/scheduler \
    | awk 'NR % 2 == 0 { print; exit }'
)"

case "${leader_id}" in
  api-a) leader_stop_cmd="docker kill ${api_a_cid}" ;;
  api-b) leader_stop_cmd="docker kill ${api_b_cid}" ;;
  *)
    FAILED=1
    echo "error: could not resolve current leader from etcd" >&2
    exit 1
    ;;
esac

printf 'leader_id=%s\nleader_stop_cmd=%s\n' "${leader_id}" "${leader_stop_cmd}" \
  > "${EVIDENCE_ROOT}/leader-resolution.txt"

API_URL="${API_URL}" \
JOB_COUNT="${JOB_COUNT}" \
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS}" \
DATABASE_URL="${DATABASE_URL}" \
LEADER_STOP_CMD="${leader_stop_cmd}" \
REQUIRE_SUCCESS_TERMINAL="true" \
DRILL_ID="mixed-version-$(date -u +%Y%m%dT%H%M%SZ)" \
bash scripts/failover_drill.sh | tee "${EVIDENCE_ROOT}/failover-drill.txt" || {
  FAILED=1
  exit 1
}

capture_compose_diagnostics "${EVIDENCE_ROOT}/final"
echo "PASS: mixed-version validation baseline completed"
echo "evidence_root=${EVIDENCE_ROOT}"
