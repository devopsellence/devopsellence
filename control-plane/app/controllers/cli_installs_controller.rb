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
    default_version = ShellQuoting.single_quote(params[:version].to_s.presence || Devopsellence::RuntimeConfig.current.cli_stable_version)

    <<~SH
      #!/usr/bin/env bash
      set -euo pipefail

      BASE_URL="${DEVOPSELLENCE_BASE_URL:-}"
      if [[ -z "$BASE_URL" ]]; then
        BASE_URL=#{default_base_url}
      fi
      CLI_VERSION="${DEVOPSELLENCE_CLI_VERSION:-}"
      if [[ -z "$CLI_VERSION" ]]; then
        CLI_VERSION=#{default_version}
      fi
      CLI_CHECKSUM_URL="${DEVOPSELLENCE_CLI_CHECKSUM_URL:-$BASE_URL/cli/checksums}"
      INSTALL_DIR="${DEVOPSELLENCE_CLI_INSTALL_DIR:-}"
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
          *)
            echo "unknown argument: $1" >&2
            exit 1
            ;;
        esac
      done

      if [[ -z "$CLI_VERSION" ]]; then
        echo "missing --version (or set DEVOPSELLENCE_CLI_VERSION or DEVOPSELLENCE_CLI_STABLE_VERSION)" >&2
        exit 1
      fi

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
        if [[ "$OS" == "darwin" ]]; then
          INSTALL_DIR="$HOME/.local/bin"
        else
          INSTALL_DIR="/usr/local/bin"
        fi
      fi

      DOWNLOAD_URL="$BASE_URL/cli/download?os=$OS&arch=$ARCH&version=$CLI_VERSION"
      CHECKSUM_URL="$CLI_CHECKSUM_URL?version=$CLI_VERSION"
      ARTIFACT_NAME="$OS-$ARCH"
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
    SH
  end
end
