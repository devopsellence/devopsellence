# frozen_string_literal: true

class CliInstallsController < ActionController::Base
  def show
    render plain: install_script, content_type: "text/plain"
  end

  private

  def install_script
    # For curl | bash installs, default the downloader to the exact host serving
    # the script so callers do not need to pass --base-url explicitly.
    default_base_url = ShellQuoting.single_quote(request.base_url)
    script_install_url = ShellQuoting.single_quote("#{request.base_url}/lfg.sh")
    default_version = ShellQuoting.single_quote(params[:version].to_s.presence || Devopsellence::RuntimeConfig.current.stable_version)

    <<~SH
      #!/usr/bin/env bash
      set -euo pipefail

      BASE_URL="${DEVOPSELLENCE_BASE_URL:-}"
      if [[ -z "$BASE_URL" ]]; then
        BASE_URL=#{default_base_url}
      fi
      INSTALL_SCRIPT_URL=#{script_install_url}
      CLI_VERSION="${DEVOPSELLENCE_CLI_VERSION:-}"
      if [[ -z "$CLI_VERSION" ]]; then
        CLI_VERSION=#{default_version}
      fi
      CLI_CHECKSUM_URL="${DEVOPSELLENCE_CLI_CHECKSUM_URL:-}"
      INSTALL_DIR="${DEVOPSELLENCE_CLI_INSTALL_DIR:-}"
      INSTALL_AGENT_SKILL="${DEVOPSELLENCE_INSTALL_AGENT_SKILL:-}"
      TARGET_NAME="devopsellence"

      while [[ $# -gt 0 ]]; do
        case "$1" in
          --base-url)
            BASE_URL="$2"
            shift 2
            ;;
          --base-url=*)
            BASE_URL="${1#*=}"
            shift
            ;;
          --version)
            CLI_VERSION="$2"
            shift 2
            ;;
          --version=*)
            CLI_VERSION="${1#*=}"
            shift
            ;;
          --install-dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
          --install-dir=*)
            INSTALL_DIR="${1#*=}"
            shift
            ;;
          --install-agent-skill)
            INSTALL_AGENT_SKILL=1
            shift
            ;;
          *)
            echo "unknown argument: $1" >&2
            exit 1
            ;;
        esac
      done

      if [[ -z "$CLI_CHECKSUM_URL" ]]; then
        CLI_CHECKSUM_URL="$BASE_URL/cli/checksums"
      fi

      if [[ -z "$CLI_VERSION" ]]; then
        echo "missing --version (or use ?version=... or set DEVOPSELLENCE_CLI_VERSION)" >&2
        exit 1
      fi

      validate_version() {
        local version="$1"

        if [[ ! "$version" =~ ^[0-9A-Za-z][0-9A-Za-z._-]*$ ]]; then
          echo "invalid version: $version" >&2
          exit 1
        fi
      }

      validate_version "$CLI_VERSION"

      OS_RAW="$(uname -s | tr '[:upper:]' '[:lower:]')"
      ARCH_RAW="$(uname -m)"
      case "$OS_RAW" in
        linux) OS="linux" ;;
        darwin) OS="darwin" ;;
        *)
          echo "unsupported operating system: $OS_RAW" >&2
          exit 1
          ;;
      esac

      case "$ARCH_RAW" in
        x86_64|amd64) ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *)
          echo "unsupported architecture: $ARCH_RAW" >&2
          exit 1
          ;;
      esac

      if [[ -z "$INSTALL_DIR" ]]; then
        INSTALL_DIR="$HOME/.local/bin"
      fi

      DOWNLOAD_URL="$BASE_URL/cli/download?os=$OS&arch=$ARCH&version=$CLI_VERSION"
      CHECKSUM_URL="$CLI_CHECKSUM_URL?version=$CLI_VERSION"
      ARTIFACT_NAME="cli-$OS-$ARCH"
      TMP_BIN="$(mktemp)"
      TMP_SUMS="$(mktemp)"
      cleanup() { rm -f "$TMP_BIN" "$TMP_SUMS"; }
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
          echo "checksum mismatch for downloaded CLI" >&2
          exit 1
        fi
      }

      json_string() {
        local value="$1"
        value="${value//\\\\/\\\\\\\\}"
        value="${value//\\"/\\\\\\"}"
        value="${value//$'\\n'/\\\\n}"
        printf '"%s"' "$value"
      }

      echo "downloading devopsellence CLI..."
      curl -fsSL "$DOWNLOAD_URL" -o "$TMP_BIN"
      curl -fsSL "$CHECKSUM_URL" -o "$TMP_SUMS"
      verify_download
      chmod +x "$TMP_BIN"

      if [[ ! -d "$INSTALL_DIR" ]]; then
        mkdir -p "$INSTALL_DIR"
      fi

      if [[ -w "$INSTALL_DIR" ]]; then
        mv "$TMP_BIN" "$INSTALL_DIR/$TARGET_NAME"
      else
        if command -v sudo >/dev/null 2>&1; then
          sudo mv "$TMP_BIN" "$INSTALL_DIR/$TARGET_NAME"
        else
          echo "install directory $INSTALL_DIR is not writable and sudo is not available" >&2
          exit 1
        fi
      fi

      echo "devopsellence CLI installed to $INSTALL_DIR/$TARGET_NAME"
      case ":$PATH:" in
        *":$INSTALL_DIR:"*) ;;
        *)
          PATH_EXPORT='export PATH="'"$INSTALL_DIR"':$PATH"'
          case "${SHELL##*/}" in
            zsh)
              RC_FILE="$HOME/.zprofile"
              ;;
            bash)
              if [[ "$OS" == "darwin" ]]; then
                RC_FILE="$HOME/.bash_profile"
              else
                RC_FILE="$HOME/.bashrc"
              fi
              ;;
            *)
              RC_FILE=""
              ;;
          esac

          if [[ -n "$RC_FILE" ]]; then
            echo "add $INSTALL_DIR to your PATH:"
            echo "  echo '$PATH_EXPORT' >> $RC_FILE"
            echo "  source $RC_FILE"
          else
            echo "add $INSTALL_DIR to your PATH:"
            echo "  $PATH_EXPORT"
          fi
          ;;
      esac

      case "$INSTALL_AGENT_SKILL" in
        1|true|TRUE|yes|YES)
          if command -v npx >/dev/null 2>&1; then
            echo "installing devopsellence agent skill..."
            npx --yes skills add devopsellence/devopsellence --skill devopsellence -g --yes
            printf '{"schema_version":1,"event":"result","operation":"devopsellence install","cli_installed":true,"cli_path":'
            json_string "$INSTALL_DIR/$TARGET_NAME"
            printf ',"agent_skill_requested":true,"agent_skill_installed":true,"agent_skill":"devopsellence"}\\n'
          else
            echo "devopsellence CLI installed. Agent skill install requested, but npx was not found." >&2
            echo "Install the skill later with:" >&2
            echo "  npx --yes skills add devopsellence/devopsellence --skill devopsellence -g --yes" >&2
            exit 1
          fi
          ;;
        *)
          echo "agent skill available:"
          echo "  npx --yes skills add devopsellence/devopsellence --skill devopsellence -g --yes"
          echo "or install CLI + skill together with:"
          echo "  curl -fsSL \"$INSTALL_SCRIPT_URL?version=$CLI_VERSION\" | bash -s -- --install-agent-skill"
          ;;
      esac
    SH
  end
end
