#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://localhost:18083}"
DATABASE_URL="${DATABASE_URL:-${SCHEDULER_TRANSITION_POSTGRES_DSN:-}}"
DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS:-180}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/ha/docker-compose.ha.yml}"
SUITE_ID="${SUITE_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
EVIDENCE_ROOT="${EVIDENCE_ROOT:-/tmp/dagens-chaos-suite-${SUITE_ID}}"
RUN_REPEAT_FAILOVER="${RUN_REPEAT_FAILOVER:-true}"
RUN_BACKPRESSURE_FAILOVER="${RUN_BACKPRESSURE_FAILOVER:-true}"
RUN_HITL_FAILOVER="${RUN_HITL_FAILOVER:-true}"
RUN_FAILBACK_CHAOS="${RUN_FAILBACK_CHAOS:-false}"
SETTLE_SECONDS="${SETTLE_SECONDS:-5}"
FAILED_STEPS=0
RESULTS_JSON_FILE=""
RESULTS_TSV_FILE=""

command -v bash >/dev/null 2>&1 || { echo "bash is required"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "docker is required"; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required"; exit 1; }

wait_for_lb_health() {
  local deadline=$(( $(date +%s) + 240 ))
  until curl -fsS "${API_URL}/health" >/dev/null; do
    if [[ $(date +%s) -ge ${deadline} ]]; then
      echo "Timed out waiting for LB health at ${API_URL}/health"
      return 1
    fi
    sleep 3
  done
}

restore_topology() {
  docker compose -f "${COMPOSE_FILE}" up -d api-server api-server-b worker-1 worker-2 >/dev/null
  if [[ "${SETTLE_SECONDS}" != "0" ]]; then
    sleep "${SETTLE_SECONDS}"
  fi
  wait_for_lb_health
}

run_step() {
  local name="$1"
  shift
  local out_dir="${EVIDENCE_ROOT}/${name}"
  mkdir -p "${out_dir}"
  echo "[${name}] starting"
  if ! "$@" | tee "${out_dir}/run.log"; then
    echo "[${name}] FAIL" | tee -a "${EVIDENCE_ROOT}/summary.txt"
    FAILED_STEPS=$((FAILED_STEPS + 1))
    return 1
  fi
  echo "[${name}] PASS" | tee -a "${EVIDENCE_ROOT}/summary.txt"
}

append_result_record() {
  local name="$1"
  local status="$2"
  local evidence_dir="$3"
  local log_path="$4"
  local restore_status="$5"

  if [[ -n "${RESULTS_TSV_FILE}" ]]; then
    printf '%s\t%s\t%s\t%s\t%s\n' \
      "${name}" \
      "${status}" \
      "${evidence_dir}" \
      "${log_path}" \
      "${restore_status}" >> "${RESULTS_TSV_FILE}"
  fi
}

write_results_json() {
  local first=1
  local name=""
  local status=""
  local evidence_dir=""
  local log_path=""
  local restore_status=""

  [[ -n "${RESULTS_JSON_FILE}" ]] || return 0

  printf '[\n' > "${RESULTS_JSON_FILE}"
  while IFS=$'\t' read -r name status evidence_dir log_path restore_status; do
    [[ -n "${name}" ]] || continue
    if [[ ${first} -eq 0 ]]; then
      printf ',\n' >> "${RESULTS_JSON_FILE}"
    fi
    first=0
    printf '  {"step":"%s","status":"%s","evidence_dir":"%s","log_path":"%s","restore_status":"%s"}' \
      "${name}" \
      "${status}" \
      "${evidence_dir}" \
      "${log_path}" \
      "${restore_status}" >> "${RESULTS_JSON_FILE}"
  done < "${RESULTS_TSV_FILE}"
  printf '\n]\n' >> "${RESULTS_JSON_FILE}"
}

run_step_with_restore() {
  local name="$1"
  shift
  local step_status=0

  local out_dir="${EVIDENCE_ROOT}/${name}"
  local log_path="${out_dir}/run.log"
  local status_label="PASS"
  local restore_status="PASS"

  if ! run_step "${name}" "$@"; then
    step_status=1
    status_label="FAIL"
  fi

  if ! restore_topology; then
    echo "[${name}] RESTORE_FAIL" | tee -a "${EVIDENCE_ROOT}/summary.txt"
    FAILED_STEPS=$((FAILED_STEPS + 1))
    restore_status="FAIL"
    append_result_record "${name}" "${status_label}" "${out_dir}" "${log_path}" "${restore_status}"
    return 1
  fi

  append_result_record "${name}" "${status_label}" "${out_dir}" "${log_path}" "${restore_status}"
  return "${step_status}"
}

mkdir -p "${EVIDENCE_ROOT}"
: > "${EVIDENCE_ROOT}/summary.txt"
RESULTS_JSON_FILE="${EVIDENCE_ROOT}/results.json"
RESULTS_TSV_FILE="${EVIDENCE_ROOT}/results.tsv"
: > "${RESULTS_TSV_FILE}"

if [[ "${RUN_REPEAT_FAILOVER}" == "true" ]]; then
  run_step_with_restore repeat-failover \
    env API_URL="${API_URL}" \
        DATABASE_URL="${DATABASE_URL}" \
        DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS}" \
        EVIDENCE_ROOT="${EVIDENCE_ROOT}/repeat-failover-evidence" \
        ./scripts/repeat_failover_drill.sh || true
fi

if [[ "${RUN_BACKPRESSURE_FAILOVER}" == "true" ]]; then
  run_step_with_restore backpressure-failover \
    env API_URL="${API_URL}" \
        DATABASE_URL="${DATABASE_URL}" \
        EVIDENCE_ROOT="${EVIDENCE_ROOT}/backpressure-failover-evidence" \
        ./scripts/backpressure_failover_drill.sh || true
fi

if [[ "${RUN_HITL_FAILOVER}" == "true" ]]; then
  run_step_with_restore hitl-failover \
    env API_URL="${API_URL}" \
        DATABASE_URL="${DATABASE_URL}" \
        DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS}" \
        EVIDENCE_ROOT="${EVIDENCE_ROOT}/hitl-failover-evidence" \
        ./scripts/hitl_failover_drill.sh || true
fi

if [[ "${RUN_FAILBACK_CHAOS}" == "true" ]]; then
  run_step_with_restore failback-chaos \
    env API_URL="${API_URL}" \
        DATABASE_URL="${DATABASE_URL}" \
        DRILL_TIMEOUT_SECONDS="${DRILL_TIMEOUT_SECONDS}" \
        EVIDENCE_ROOT="${EVIDENCE_ROOT}/failback-chaos-evidence" \
        ./scripts/failback_chaos_drill.sh || true
fi

docker compose -f "${COMPOSE_FILE}" ps > "${EVIDENCE_ROOT}/compose-ps.txt" || true
docker compose -f "${COMPOSE_FILE}" logs --no-color > "${EVIDENCE_ROOT}/compose-logs.txt" || true
curl -sS "${API_URL}/health" > "${EVIDENCE_ROOT}/lb-health.json" || true
write_results_json

if [[ "${FAILED_STEPS}" -ne 0 ]]; then
  echo "FAIL: chaos/load suite completed with ${FAILED_STEPS} failing step(s)"
  echo "evidence_root=${EVIDENCE_ROOT}"
  exit 1
fi

echo "PASS: chaos/load suite completed"
echo "evidence_root=${EVIDENCE_ROOT}"
