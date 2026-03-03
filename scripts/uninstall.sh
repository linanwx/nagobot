#!/usr/bin/env bash
set -euo pipefail

LABEL="com.nagobot.serve"
PLIST_PATH="$HOME/Library/LaunchAgents/${LABEL}.plist"
INSTALL_BIN="/usr/local/bin/nagobot"
SOCKET_PATH="$HOME/.nagobot/nagobot.sock"
DATA_DIR="$HOME/.nagobot"

echo "==> Stopping service..."
launchctl unload "$PLIST_PATH" 2>/dev/null || true

echo "==> Removing launchd plist..."
rm -f "$PLIST_PATH"

echo "==> Removing binary..."
sudo rm -f "$INSTALL_BIN"

echo "==> Removing socket..."
rm -f "$SOCKET_PATH"

echo ""
read -p "Remove all data (~/.nagobot)? This deletes config, sessions, and logs. [y/N] " answer
if [[ "${answer:-N}" =~ ^[Yy]$ ]]; then
    echo "==> Removing ${DATA_DIR}..."
    rm -rf "$DATA_DIR"
else
    echo "    Keeping ${DATA_DIR}"
fi

echo ""
echo "==> Uninstall complete."
