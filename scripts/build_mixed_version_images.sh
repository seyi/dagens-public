#!/usr/bin/env bash
set -euo pipefail

# Build adjacent-version API and worker images for mixed-version validation.
#
# This script uses temporary git worktrees so the current checkout is not mutated.
#
# Required inputs:
#   REF_OLD   git ref for version N
#   REF_NEW   git ref for version N+1
#
# Optional:
#   IMAGE_PREFIX   docker image prefix (default: dagens-mixed)
#   WORKTREE_ROOT  temp worktree root (default: /tmp/dagens-mixed-version-worktrees)
#
# Output:
#   writes image tags to stdout and to IMAGE_ENV_OUT in shell-env format

REF_OLD="${REF_OLD:-}"
REF_NEW="${REF_NEW:-}"
IMAGE_PREFIX="${IMAGE_PREFIX:-dagens-mixed}"
WORKTREE_ROOT="${WORKTREE_ROOT:-/tmp/dagens-mixed-version-worktrees}"
IMAGE_ENV_OUT="${IMAGE_ENV_OUT:-/tmp/dagens-mixed-version-images.env}"

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "error: ${name} is required" >&2
    exit 1
  fi
}

command -v git >/dev/null 2>&1 || { echo "git is required" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }

require_env REF_OLD
require_env REF_NEW

sanitize_ref() {
  printf '%s' "$1" | tr '/:@ ' '-' | tr -cd '[:alnum:]._-\n'
}

OLD_TAG_SUFFIX="$(sanitize_ref "${REF_OLD}")"
NEW_TAG_SUFFIX="$(sanitize_ref "${REF_NEW}")"

OLD_WORKTREE="${WORKTREE_ROOT}/old-${OLD_TAG_SUFFIX}"
NEW_WORKTREE="${WORKTREE_ROOT}/new-${NEW_TAG_SUFFIX}"

API_IMAGE_OLD="${IMAGE_PREFIX}/api:${OLD_TAG_SUFFIX}"
API_IMAGE_NEW="${IMAGE_PREFIX}/api:${NEW_TAG_SUFFIX}"
WORKER_IMAGE_OLD="${IMAGE_PREFIX}/worker:${OLD_TAG_SUFFIX}"
WORKER_IMAGE_NEW="${IMAGE_PREFIX}/worker:${NEW_TAG_SUFFIX}"

cleanup() {
  git worktree remove --force "${OLD_WORKTREE}" >/dev/null 2>&1 || true
  git worktree remove --force "${NEW_WORKTREE}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

mkdir -p "${WORKTREE_ROOT}"
rm -f "${IMAGE_ENV_OUT}"

git worktree add --detach "${OLD_WORKTREE}" "${REF_OLD}" >/dev/null
git worktree add --detach "${NEW_WORKTREE}" "${REF_NEW}" >/dev/null

docker build -f "${OLD_WORKTREE}/Dockerfile.api" -t "${API_IMAGE_OLD}" "${OLD_WORKTREE}"
docker build -f "${NEW_WORKTREE}/Dockerfile.api" -t "${API_IMAGE_NEW}" "${NEW_WORKTREE}"
docker build -f "${OLD_WORKTREE}/Dockerfile.worker" -t "${WORKER_IMAGE_OLD}" "${OLD_WORKTREE}"
docker build -f "${NEW_WORKTREE}/Dockerfile.worker" -t "${WORKER_IMAGE_NEW}" "${NEW_WORKTREE}"

cat > "${IMAGE_ENV_OUT}" <<EOF
MIXED_VERSION_API_IMAGE_A=${API_IMAGE_OLD}
MIXED_VERSION_API_IMAGE_B=${API_IMAGE_NEW}
MIXED_VERSION_WORKER_IMAGE_A=${WORKER_IMAGE_OLD}
MIXED_VERSION_WORKER_IMAGE_B=${WORKER_IMAGE_NEW}
REF_OLD=${REF_OLD}
REF_NEW=${REF_NEW}
EOF

cat "${IMAGE_ENV_OUT}"
