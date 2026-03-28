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

# Fetch latest version from GitHub Releases.
VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)"
if [ -z "$VERSION" ]; then
  echo "Error: failed to fetch latest version"
  exit 1
fi

URL="https://github.com/${REPO}/releases/download/${VERSION}/nagobot-${OS}-${ARCH}"
INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "$INSTALL_DIR"

# Detect mainland China — use gh-proxy mirror for faster downloads.
COUNTRY="$(curl -s --max-time 3 https://ipinfo.io/country 2>/dev/null || echo "")"
if [ "$COUNTRY" = "CN" ]; then
  GHPROXY="https://gh-proxy.com/"
  echo "Detected mainland China, using mirror ${GHPROXY}"
  URL="${GHPROXY}${URL}"
fi

echo "Downloading nagobot ${VERSION}..."
# Remove old binary first — a running process keeps its inode,
# so deleting is safe and avoids "text file busy" / write errors.
rm -f "${INSTALL_DIR}/nagobot"
curl -fsSL --retry 3 --retry-delay 5 "$URL" -o "${INSTALL_DIR}/nagobot"
chmod +x "${INSTALL_DIR}/nagobot"

# macOS: remove quarantine attribute to bypass Gatekeeper.
if [ "$OS" = "darwin" ]; then
  xattr -d com.apple.quarantine "${INSTALL_DIR}/nagobot" 2>/dev/null || true
  codesign --sign - --force "${INSTALL_DIR}/nagobot" 2>/dev/null || true
fi

# Add to PATH persistently if not already present.
RC=""
if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
  LINE="export PATH=\"${INSTALL_DIR}:\$PATH\""
  if [ -n "${ZSH_VERSION:-}" ] || [ "$(basename "${SHELL:-}")" = "zsh" ]; then
    RC="$HOME/.zshrc"
  elif [ -f "$HOME/.bashrc" ]; then
    RC="$HOME/.bashrc"
  elif [ -f "$HOME/.profile" ]; then
    RC="$HOME/.profile"
  fi
  if [ -n "$RC" ]; then
    if ! grep -qF "$INSTALL_DIR" "$RC" 2>/dev/null; then
      echo "" >> "$RC"
      echo "# nagobot" >> "$RC"
      echo "$LINE" >> "$RC"
      echo "Added ${INSTALL_DIR} to PATH in ${RC}"
    fi
  fi
  export PATH="${INSTALL_DIR}:$PATH"
fi

echo "Registering system service..."
"${INSTALL_DIR}/nagobot" install

# Enable linger for root so the user service survives SSH disconnects.
if [ "$(id -u)" = "0" ]; then
    loginctl enable-linger root 2>/dev/null || true
fi

# Remind user to reload shell if PATH was updated.
if [ -n "${RC:-}" ]; then
  echo ""
  echo "    Restart your terminal or run: source ${RC}"
fi
