# frozen_string_literal: true

class AgentInstallScript
  class << self
    def render(base_url:, stable_version:, edge_version: "")
      default_base_url = shell_single_quote(base_url)
      default_stable_agent_version = shell_single_quote(stable_version)
      default_edge_agent_version = shell_single_quote(edge_version)

      <<~SH
        #!/usr/bin/env bash
        set -euo pipefail

        BASE_URL="${DEVOPSELLENCE_BASE_URL:-}"
        if [[ -z "$BASE_URL" ]]; then
          BASE_URL=#{default_base_url}
        fi
        AGENT_CHANNEL="${DEVOPSELLENCE_AGENT_CHANNEL:-stable}"
        TOKEN=""
        AGENT_VERSION="${DEVOPSELLENCE_AGENT_VERSION:-}"
        AGENT_STABLE_VERSION=#{default_stable_agent_version}
        AGENT_EDGE_VERSION=#{default_edge_agent_version}
        AGENT_CHECKSUM_URL="${DEVOPSELLENCE_AGENT_CHECKSUM_URL:-$BASE_URL/agent/checksums}"

        while [[ $# -gt 0 ]]; do
          case "$1" in
            --token)
              TOKEN="$2"
              shift 2
              ;;
            --token=*)
              TOKEN="${1#*=}"
              shift
              ;;
            --base-url)
              BASE_URL="$2"
              shift 2
              ;;
            --base-url=*)
              BASE_URL="${1#*=}"
              shift
              ;;
            --channel)
              AGENT_CHANNEL="$2"
              shift 2
              ;;
            --channel=*)
              AGENT_CHANNEL="${1#*=}"
              shift
              ;;
            --agent-version)
              AGENT_VERSION="$2"
              shift 2
              ;;
            --agent-version=*)
              AGENT_VERSION="${1#*=}"
              shift
              ;;
            *)
              echo "unknown argument: $1" >&2
              exit 1
              ;;
          esac
        done

        if [[ -z "$TOKEN" ]]; then
          echo "missing --token" >&2
          exit 1
        fi

        case "$AGENT_CHANNEL" in
          stable|edge)
            ;;
          *)
            echo "unsupported channel: $AGENT_CHANNEL" >&2
            exit 1
            ;;
        esac

        if [[ -z "$AGENT_VERSION" ]]; then
          if [[ "$AGENT_CHANNEL" == "edge" ]]; then
            AGENT_VERSION="$AGENT_EDGE_VERSION"
          else
            AGENT_VERSION="$AGENT_STABLE_VERSION"
          fi
        fi

        OS_RAW="$(uname -s | tr [:upper:] [:lower:])"
        ARCH_RAW="$(uname -m)"

        case "$OS_RAW" in
          linux)
            OS="linux"
            ;;
          darwin)
            OS="darwin"
            ;;
          *)
            echo "unsupported operating system: $OS_RAW" >&2
            exit 1
            ;;
        esac

        case "$ARCH_RAW" in
          x86_64|amd64)
            ARCH="amd64"
            ;;
          arm64|aarch64)
            ARCH="arm64"
            ;;
          *)
            echo "unsupported architecture: $ARCH_RAW" >&2
            exit 1
            ;;
        esac

        if [[ "$OS" != "linux" ]]; then
          echo "managed install is linux-only; download the $OS/$ARCH binary manually" >&2
          exit 1
        fi

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

        docker_ready() {
          command -v docker >/dev/null 2>&1 && run_root docker info >/dev/null 2>&1
        }

        detect_supported_ubuntu() {
          if [[ ! -r /etc/os-release ]]; then
            return 1
          fi

          . /etc/os-release

          if [[ "${ID:-}" != "ubuntu" ]]; then
            return 1
          fi

          case "${VERSION_CODENAME:-}" in
            jammy|noble)
              printf '%s\n' "${VERSION_CODENAME}"
              ;;
            *)
              return 1
              ;;
          esac
        }

        install_docker_for_supported_ubuntu() {
          local codename

          if ! codename="$(detect_supported_ubuntu)"; then
            echo "Docker Engine is a prerequisite. Automatic install is supported only on Ubuntu 22.04 (jammy) and 24.04 (noble)." >&2
            exit 1
          fi

          echo "Docker not found; installing Docker Engine for Ubuntu ${codename}..."
          run_root apt-get update
          run_root apt-get install -y ca-certificates curl
          run_root install -m 0755 -d /etc/apt/keyrings
          run_root curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
          run_root chmod a+r /etc/apt/keyrings/docker.asc
          run_root tee /etc/apt/sources.list.d/docker.list >/dev/null <<EOF_DOCKER_REPO
        deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${codename} stable
        EOF_DOCKER_REPO
          run_root apt-get update
          run_root apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
          run_root systemctl enable --now docker
        }

        AGENT_URL="${DEVOPSELLENCE_AGENT_URL:-$BASE_URL/agent/download}"
        DOWNLOAD_URL="$AGENT_URL?os=$OS&arch=$ARCH"
        CHECKSUM_URL="$AGENT_CHECKSUM_URL"
        if [[ "$AGENT_CHANNEL" != "stable" ]]; then
          DOWNLOAD_URL="$DOWNLOAD_URL&channel=$AGENT_CHANNEL"
          CHECKSUM_URL="$CHECKSUM_URL?channel=$AGENT_CHANNEL"
        fi
        if [[ -n "$AGENT_VERSION" ]]; then
          DOWNLOAD_URL="$DOWNLOAD_URL&version=$AGENT_VERSION"
          if [[ "$CHECKSUM_URL" == *"?"* ]]; then
            CHECKSUM_URL="$CHECKSUM_URL&version=$AGENT_VERSION"
          else
            CHECKSUM_URL="$CHECKSUM_URL?version=$AGENT_VERSION"
          fi
        fi
        ARTIFACT_NAME="$OS-$ARCH"
        AGENT_BIN="/usr/local/bin/devopsellence-agent"
        ENV_DIR="/etc/devopsellence"
        ENV_FILE="$ENV_DIR/agent.env"
        SERVICE_FILE="/etc/systemd/system/devopsellence-agent.service"
        TMP_BIN="$(mktemp)"
        TMP_SUMS="$(mktemp)"
        cleanup() {
          rm -f "$TMP_BIN"
          rm -f "$TMP_SUMS"
        }
        trap cleanup EXIT

        checksum_value() {
          local path="$1"

          if command -v sha256sum >/dev/null 2>&1; then
            sha256sum "$path" | awk '{print $1}'
            return
          fi
          if command -v shasum >/dev/null 2>&1; then
            shasum -a 256 "$path" | awk '{print $1}'
            return
          fi

          echo "sha256 checksum tool not found (need sha256sum or shasum)" >&2
          exit 1
        }

        verify_download() {
          local expected actual

          expected="$(awk -v name="$ARTIFACT_NAME" '$2 == name { print $1; exit }' "$TMP_SUMS")"
          if [[ -z "$expected" ]]; then
            echo "missing checksum entry for $ARTIFACT_NAME" >&2
            exit 1
          fi

          actual="$(checksum_value "$TMP_BIN")"
          if [[ "$actual" != "$expected" ]]; then
            echo "checksum mismatch for downloaded agent" >&2
            exit 1
          fi
        }

        if ! command -v docker >/dev/null 2>&1; then
          install_docker_for_supported_ubuntu
        fi

        if ! docker_ready; then
          echo "Docker found but the engine is not available; attempting to start docker.service..."
          if ! run_root systemctl enable --now docker; then
            echo "failed to start docker.service" >&2
          fi
        fi

        if ! docker_ready; then
          echo "Docker Engine is a prerequisite. Install and start Docker, then rerun this command." >&2
          exit 1
        fi

        echo "downloading devopsellence agent..."
        run_root mkdir -p "$ENV_DIR"
        curl -fsSL "$DOWNLOAD_URL" -o "$TMP_BIN"
        curl -fsSL "$CHECKSUM_URL" -o "$TMP_SUMS"
        verify_download
        chmod +x "$TMP_BIN"

        run_root tee "$ENV_FILE" >/dev/null <<EOF_ENV
        DEVOPSELLENCE_BASE_URL=$BASE_URL
        DEVOPSELLENCE_BOOTSTRAP_TOKEN=$TOKEN
        EOF_ENV
        run_root chmod 600 "$ENV_FILE"

        run_root tee "$SERVICE_FILE" >/dev/null <<EOF_SERVICE
        [Unit]
        Description=devopsellence agent
        After=network-online.target docker.service docker.socket
        Wants=network-online.target docker.service docker.socket

        [Service]
        EnvironmentFile=$ENV_FILE
        ExecStart=$AGENT_BIN
        Restart=always
        RestartSec=2

        [Install]
        WantedBy=multi-user.target
        EOF_SERVICE

        run_root systemctl daemon-reload
        run_root systemctl stop devopsellence-agent || true
        run_root install -m 0755 "$TMP_BIN" "$AGENT_BIN"
        run_root systemctl enable --now devopsellence-agent

        echo "devopsellence agent installed and started."
      SH
    end

    private

    def shell_single_quote(value)
      escaped = value.to_s.gsub("'", %q('"'"'))
      "'#{escaped}'"
    end
  end
end
