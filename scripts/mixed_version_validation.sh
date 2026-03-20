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
DATABASE_URL="${DATABASE_URL:-postgres://postgres:postgres@localhost:55432/dagens?sslmode=disable}"
JOB_COUNT="${JOB_COUNT:-3}"
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS:-180}"
EVIDENCE_ROOT="${EVIDENCE_ROOT:-/tmp/dagens-mixed-version-validation-$(date -u +%Y%m%dT%H%M%SZ)}"

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

cleanup() {
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

for service in etcd postgres redis api-server api-server-b api-lb worker-1 worker-2; do
  cid="$("${compose_cmd[@]}" ps -q "${service}")"
  if [[ -z "${cid}" ]]; then
    echo "error: failed to resolve container for ${service}" >&2
    exit 1
  fi
  printf '%s %s %s\n' \
    "${service}" \
    "${cid}" \
    "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${cid}")" \
    >> "${EVIDENCE_ROOT}/container-status.txt"
done

leader_stop_cmd=""
etcd_cid="$("${compose_cmd[@]}" ps -q etcd)"
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
DRILL_ID="mixed-version-$(date -u +%Y%m%dT%H%M%SZ)" \
bash scripts/failover_drill.sh | tee "${EVIDENCE_ROOT}/failover-drill.txt"

echo "PASS: mixed-version validation baseline completed"
echo "evidence_root=${EVIDENCE_ROOT}"
