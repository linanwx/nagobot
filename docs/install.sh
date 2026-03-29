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

# Detect mainland China — rank mirrors by speed before downloading.
COUNTRY="$(curl -s --max-time 3 https://ipinfo.io/country 2>/dev/null || echo "")"
CHINA_MIRRORS=("https://gh-proxy.com/" "https://ghfast.top/" "https://gh-proxy.org/")

echo "Downloading nagobot ${VERSION}..."
# Remove old binary first — a running process keeps its inode,
# so deleting is safe and avoids "text file busy" / write errors.
rm -f "${INSTALL_DIR}/nagobot"

DOWNLOADED=false
if [ "$COUNTRY" = "CN" ]; then
  echo "Detected mainland China, ranking mirrors..."
  # Probe each mirror (first 100KB) in parallel, sort by speed.
  RANKED=()
  declare -A SPEEDS
  for MIRROR in "${CHINA_MIRRORS[@]}"; do
    (
      SPEED=$(curl -s -o /dev/null -w '%{speed_download}' --max-time 10 -r 0-102399 "${MIRROR}${URL}" 2>/dev/null || echo "0")
      echo "${SPEED} ${MIRROR}"
    ) &
  done > /tmp/nagobot_mirror_probe 2>&1
  wait
  # Sort by speed descending, print ranking.
  while IFS=' ' read -r SPEED MIRROR; do
    KB=$(awk "BEGIN {printf \"%.0f\", ${SPEED:-0}/1024}")
    if [ "$KB" -gt 0 ] 2>/dev/null; then
      echo "    ${MIRROR} ${KB} KB/s"
    else
      echo "    ${MIRROR} failed"
    fi
    RANKED+=("$MIRROR")
  done < <(sort -rn /tmp/nagobot_mirror_probe)
  rm -f /tmp/nagobot_mirror_probe
  # Download using ranked order, then direct as fallback.
  for MIRROR in "${RANKED[@]}"; do
    echo "    Trying ${MIRROR}"
    if curl -fsSL --max-time 120 "${MIRROR}${URL}" -o "${INSTALL_DIR}/nagobot" 2>/dev/null; then
      DOWNLOADED=true
      break
    fi
    rm -f "${INSTALL_DIR}/nagobot"
  done
  if [ "$DOWNLOADED" = false ]; then
    echo "    All mirrors failed, trying direct..."
    curl -fsSL --retry 3 --retry-delay 5 "$URL" -o "${INSTALL_DIR}/nagobot"
  fi
else
  curl -fsSL --retry 3 --retry-delay 5 "$URL" -o "${INSTALL_DIR}/nagobot"
fi
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
