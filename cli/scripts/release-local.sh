#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLI_DIR="$ROOT_DIR/cli"
TARGET_NAME="devopsellence"
INSTALL_DIR="${DEVOPSELLENCE_CLI_INSTALL_DIR:-}"
INSTALL_AGENT_SKILL="${DEVOPSELLENCE_INSTALL_AGENT_SKILL:-}"
AGENT_SKILLS_DIR="${DEVOPSELLENCE_AGENT_SKILLS_DIR:-}"

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

BUILD_DIR="$CLI_DIR/dist/local-head"
mkdir -p "$BUILD_DIR"
TMP_BIN="$(mktemp "$BUILD_DIR/devopsellence.XXXXXX")"
cleanup() { rm -f "$TMP_BIN"; }
trap cleanup EXIT

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

install_agent_skill_from_checkout() {
  local skills_dir skill_dest source_dir

  skills_dir="$AGENT_SKILLS_DIR"
  if [[ -z "$skills_dir" ]]; then
    skills_dir="$HOME/.agents/skills"
  fi
  skill_dest="$skills_dir/devopsellence"
  source_dir="$ROOT_DIR/skills/devopsellence"

  if [[ ! -f "$source_dir/SKILL.md" ]]; then
    echo "devopsellence CLI installed, but agent skill install failed: missing $source_dir/SKILL.md" >&2
    exit 1
  fi

  echo "installing devopsellence agent skill..."
  mkdir -p "$skills_dir"
  rm -rf "$skill_dest"
  cp -R "$source_dir" "$skill_dest"
  echo "devopsellence agent skill installed"
  echo "  version: local-head ($COMMIT)"
  echo "  source: $source_dir"
  echo "  path: $skill_dest"
}

case "$INSTALL_AGENT_SKILL" in
  1|true|TRUE|yes|YES)
    install_agent_skill_from_checkout
    ;;
  *)
    echo "agent skill available; rerun installer with DEVOPSELLENCE_INSTALL_AGENT_SKILL=1"
    ;;
esac
