#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLI_DIR="$ROOT_DIR/cli"
TARGET_NAME="devopsellence"
INSTALL_DIR="${DEVOPSELLENCE_CLI_INSTALL_DIR:-}"
INSTALL_AGENT_SKILL="${DEVOPSELLENCE_INSTALL_AGENT_SKILL:-}"

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
case "$INSTALL_DIR" in
  /*) ;;
  *) INSTALL_DIR="$(pwd -P)/$INSTALL_DIR" ;;
esac

BUILD_DIR="$CLI_DIR/dist/local-head"
mkdir -p "$BUILD_DIR"
TMP_BIN="$(mktemp "$BUILD_DIR/devopsellence.XXXXXX")"
cleanup() { rm -f "$TMP_BIN"; }
trap cleanup EXIT

json_string() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  printf '"%s"' "$value"
}

COMMIT="$(git -C "$ROOT_DIR" rev-parse --short HEAD)"
BUILD_TIME="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
MODULE_PATH="github.com/devopsellence/cli/internal/version"

echo "building devopsellence CLI from HEAD for $OS/$ARCH..."
cd "$CLI_DIR"
mise install
mkdir -p .gocache
GOCACHE="$CLI_DIR/.gocache" mise exec -- go build \
  -trimpath \
  -ldflags "-s -w -X ${MODULE_PATH}.Commit=${COMMIT} -X ${MODULE_PATH}.Date=${BUILD_TIME}" \
  -o "$TMP_BIN" \
  ./cmd/devopsellence

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
      printf '{"schema_version":1,"event":"result","operation":"devopsellence release-local","cli_installed":true,"cli_path":'
      json_string "$INSTALL_DIR/$TARGET_NAME"
      printf ',"commit":'
      json_string "$COMMIT"
      printf ',"agent_skill_requested":true,"agent_skill_installed":true,"agent_skill":"devopsellence"}\n'
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
    echo "or rerun installer with DEVOPSELLENCE_INSTALL_AGENT_SKILL=1"
    ;;
esac
