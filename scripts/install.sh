#!/usr/bin/env bash
set -euo pipefail

REPO="linanwx/nagobot"

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) echo "unsupported" ;;
  esac
}

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(detect_arch)"

if [ "$ARCH" = "unsupported" ]; then
  echo "Error: unsupported architecture $(uname -m)"
  exit 1
fi

if [ "$OS" = "darwin" ]; then
  # macOS: use Homebrew (source build, no code signing issues)
  if ! command -v brew &>/dev/null; then
    echo "Installing Homebrew..."
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  fi
  brew install linanwx/nagobot/nagobot
else
  # Linux: download pre-built binary from GitHub Releases
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)"
  if [ -z "$VERSION" ]; then
    echo "Error: failed to fetch latest version"
    exit 1
  fi

  URL="https://github.com/${REPO}/releases/download/${VERSION}/nagobot-${OS}-${ARCH}"
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"

  echo "Downloading nagobot ${VERSION}..."
  curl -fsSL "$URL" -o "${INSTALL_DIR}/nagobot"
  chmod +x "${INSTALL_DIR}/nagobot"

  if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
    echo ""
    echo "NOTE: Add ${INSTALL_DIR} to your PATH:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    echo ""
  fi
fi

echo "Registering system service..."
if [ "$OS" = "darwin" ]; then
  nagobot install
else
  "${INSTALL_DIR}/nagobot" install
fi
