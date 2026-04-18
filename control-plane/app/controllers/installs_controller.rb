# frozen_string_literal: true

class InstallsController < ActionController::Base
  def show
    render plain: install_script, content_type: "text/plain"
  end

  def uninstall
    render plain: uninstall_script, content_type: "text/plain"
  end

  private

  def install_script
    base_url = PublicBaseUrl.resolve(request)
    default_version = params[:version].to_s.presence || Devopsellence::RuntimeConfig.current.agent_stable_version
    AgentInstallScript.render(base_url: base_url, default_version: default_version)
  end

  def uninstall_script
    <<~SH
      #!/usr/bin/env bash
      set -euo pipefail

      PURGE_RUNTIME=0

      while [[ $# -gt 0 ]]; do
        case "$1" in
          --purge-runtime)
            PURGE_RUNTIME=1
            shift
            ;;
          *)
            echo "unknown argument: $1" >&2
            exit 1
            ;;
        esac
      done

      if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
        SUDO="sudo"
      else
        SUDO=""
      fi

      run_root() {
        if [[ -n "$SUDO" ]]; then
          "$SUDO" "$@"
        else
          "$@"
        fi
      }

      AGENT_BIN="/usr/local/bin/devopsellence-agent"
      ENV_DIR="/etc/devopsellence"
      ENV_FILE="$ENV_DIR/agent.env"
      SERVICE_FILE="/etc/systemd/system/devopsellence-agent.service"
      STATE_DIR="/var/lib/devopsellence"
      AUTH_STATE_FILE="$STATE_DIR/agent-auth-state.json"
      STATUS_FILE="$STATE_DIR/status.json"
      NETWORK_NAME="devopsellence"

      purge_runtime() {
        if ! command -v docker >/dev/null 2>&1; then
          echo "Docker CLI not found; skipping runtime purge."
          return
        fi

        mapfile -t container_ids < <(
          {
            run_root docker ps -aq --filter label=devopsellence.managed=true
            run_root docker ps -aq --filter label=devopsellence.system
          } | awk 'NF && !seen[$0]++'
        )
        if [[ "${#container_ids[@]}" -gt 0 ]]; then
          run_root docker rm -f "${container_ids[@]}"
        fi

        run_root docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
      }

      if command -v systemctl >/dev/null 2>&1; then
        run_root systemctl disable --now devopsellence-agent >/dev/null 2>&1 || true
      fi

      run_root rm -f "$SERVICE_FILE"

      if command -v systemctl >/dev/null 2>&1; then
        run_root systemctl daemon-reload
        run_root systemctl reset-failed devopsellence-agent >/dev/null 2>&1 || true
      fi

      run_root rm -f "$AGENT_BIN"
      run_root rm -f "$ENV_FILE"
      run_root rm -f "$AUTH_STATE_FILE"
      run_root rm -f "$STATUS_FILE"
      run_root rm -rf "$ENV_DIR"
      run_root rmdir "$STATE_DIR" >/dev/null 2>&1 || true

      if [[ "$PURGE_RUNTIME" == "1" ]]; then
        purge_runtime
      fi

      echo "devopsellence agent uninstalled."
      if [[ "$PURGE_RUNTIME" == "1" ]]; then
        echo "managed Docker runtime resources removed."
      else
        echo "managed Docker runtime resources left intact; rerun with --purge-runtime to remove them."
      fi
    SH
  end
end
