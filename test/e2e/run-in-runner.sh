#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECKOUT_ROOT="$(git -C "${ROOT}" rev-parse --show-toplevel)"
WORKSPACE_ROOT="$(dirname "${CHECKOUT_ROOT}")"
RUNNER_IMAGE="${DEVOPSELLENCE_E2E_RUNNER_IMAGE:-devopsellence/e2e-runner:local}"
GIT_COMMON_DIR="$(git -C "${ROOT}" rev-parse --git-common-dir)"

resolve_repo_root() {
  shift

  for candidate in "$@"; do
    if [[ -d "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  printf '%s\n' "$1"
}

CLI_ROOT="${DEVOPSELLENCE_CLI_ROOT:-$(resolve_repo_root cli "${CHECKOUT_ROOT}/cli" "${CHECKOUT_ROOT}/ci-repos/cli" "${WORKSPACE_ROOT}/cli")}"
AGENT_ROOT="${DEVOPSELLENCE_AGENT_ROOT:-$(resolve_repo_root agent "${CHECKOUT_ROOT}/agent" "${CHECKOUT_ROOT}/ci-repos/agent" "${WORKSPACE_ROOT}/agent")}"
GCP_MOCK_ROOT="${DEVOPSELLENCE_GCP_MOCK_ROOT:-$(resolve_repo_root test/support/gcp-mock "${CHECKOUT_ROOT}/test/support/gcp-mock" "${CHECKOUT_ROOT}/ci-repos/gcp-mock" "${WORKSPACE_ROOT}/gcp-mock")}"

if [[ "${GIT_COMMON_DIR}" != /* ]]; then
  GIT_COMMON_DIR="${ROOT}/${GIT_COMMON_DIR}"
fi

EXTRA_MOUNTS=()
declare -A SEEN_MOUNTS

add_mount_if_needed() {
  local repo_root="$1"
  local common_dir

  [[ -d "${repo_root}" ]] || return

  common_dir="$(git -C "${repo_root}" rev-parse --git-common-dir)"
  if [[ "${common_dir}" != /* ]]; then
    common_dir="${repo_root}/${common_dir}"
  fi

  if [[ "${common_dir}" != "${repo_root}"* ]] && [[ -z "${SEEN_MOUNTS[${common_dir}]:-}" ]]; then
    EXTRA_MOUNTS+=(-v "${common_dir}:${common_dir}")
    SEEN_MOUNTS["${common_dir}"]=1
  fi
}

add_mount_if_needed "${ROOT}"
for repo_root in "${CLI_ROOT}" "${AGENT_ROOT}" "${GCP_MOCK_ROOT}"; do
  if [[ "${repo_root}" != "${ROOT}"* ]]; then
    EXTRA_MOUNTS+=(-v "${repo_root}:${repo_root}")
  fi
  add_mount_if_needed "${repo_root}"
done

docker run --rm \
  --network host \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "${ROOT}:${ROOT}" \
  "${EXTRA_MOUNTS[@]}" \
  -w "${ROOT}" \
  -e DEVOPSELLENCE_E2E_REPO_MOUNT="${ROOT}/control-plane" \
  -e DEVOPSELLENCE_CLI_ROOT="${CLI_ROOT}" \
  -e DEVOPSELLENCE_AGENT_ROOT="${AGENT_ROOT}" \
  -e DEVOPSELLENCE_GCP_MOCK_ROOT="${GCP_MOCK_ROOT}" \
  -e DEVOPSELLENCE_E2E_RUNNER_IMAGE="${RUNNER_IMAGE}" \
  -e DEVOPSELLENCE_E2E_POSTGRES_IMAGE="${DEVOPSELLENCE_E2E_POSTGRES_IMAGE:-postgres:16}" \
  -e DEVOPSELLENCE_E2E_REGISTRY_IMAGE="${DEVOPSELLENCE_E2E_REGISTRY_IMAGE:-registry:2}" \
  -e DEVOPSELLENCE_E2E_ENVOY_IMAGE="${DEVOPSELLENCE_E2E_ENVOY_IMAGE:-docker.io/envoyproxy/envoy@sha256:d9b4a70739d92b3e28cd407f106b0e90d55df453d7d87773efd22b4429777fe8}" \
  -e DEVOPSELLENCE_E2E_BUILD_RUNNER_IMAGE="${DEVOPSELLENCE_E2E_BUILD_RUNNER_IMAGE:-}" \
  -e DEVOPSELLENCE_E2E_RELEASE_VERSION="${DEVOPSELLENCE_E2E_RELEASE_VERSION:-}" \
  -e DEVOPSELLENCE_E2E_RUN_ID="${DEVOPSELLENCE_E2E_RUN_ID:-}" \
  -e DEVOPSELLENCE_E2E_KEEP="${DEVOPSELLENCE_E2E_KEEP:-}" \
  -e DEVOPSELLENCE_E2E_GO_BIN="${DEVOPSELLENCE_E2E_GO_BIN:-/usr/local/go/bin/go}" \
  "${RUNNER_IMAGE}" \
  bash -lc "export PATH=/usr/local/go/bin:\$PATH && git config --global --add safe.directory '${ROOT}' && git config --global --add safe.directory '${ROOT}/control-plane' && git config --global --add safe.directory '${CLI_ROOT}' && git config --global --add safe.directory '${AGENT_ROOT}' && git config --global --add safe.directory '${GCP_MOCK_ROOT}' && ruby test/e2e/e2e.rb"
