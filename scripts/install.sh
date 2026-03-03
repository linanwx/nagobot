#!/usr/bin/env bash
set -euo pipefail

LABEL="com.nagobot.serve"
PLIST_PATH="$HOME/Library/LaunchAgents/${LABEL}.plist"
INSTALL_BIN="/usr/local/bin/nagobot"
LOG_DIR="$HOME/.nagobot/logs"

cd "$(dirname "$0")/.."

echo "==> Checking dependencies..."
if ! command -v go &>/dev/null; then
    echo "Error: Go is not installed. Install it from https://go.dev/dl/"
    exit 1
fi

echo "==> Building nagobot..."
go build -o nagobot .

echo "==> Installing binary to ${INSTALL_BIN}..."
sudo cp nagobot "$INSTALL_BIN"
sudo chmod 755 "$INSTALL_BIN"

echo "==> Running initial setup..."
"$INSTALL_BIN" onboard

echo "==> Creating log directory..."
mkdir -p "$LOG_DIR"

echo "==> Detecting Homebrew prefix..."
if [ -d "/opt/homebrew" ]; then
    BREW_PREFIX="/opt/homebrew"
elif [ -d "/usr/local/Cellar" ]; then
    BREW_PREFIX="/usr/local"
else
    BREW_PREFIX="/usr/local"
    echo "    Homebrew not detected, using ${BREW_PREFIX} as default prefix."
fi
echo "    Using: ${BREW_PREFIX}"

echo "==> Stopping existing service (if any)..."
launchctl unload "$PLIST_PATH" 2>/dev/null || true

echo "==> Generating launchd plist..."
cat > "$PLIST_PATH" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${LABEL}</string>

    <key>ProgramArguments</key>
    <array>
        <string>${INSTALL_BIN}</string>
        <string>serve</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>StandardOutPath</key>
    <string>${LOG_DIR}/launchd-stdout.log</string>

    <key>StandardErrorPath</key>
    <string>${LOG_DIR}/launchd-stderr.log</string>

    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>${HOME}</string>
        <key>PATH</key>
        <string>${BREW_PREFIX}/bin:${BREW_PREFIX}/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    </dict>
</dict>
</plist>
EOF

echo "==> Starting service..."
launchctl load "$PLIST_PATH"

echo ""
echo "==> Installation complete!"
echo "    Service: launchctl print gui/$(id -u)/${LABEL}"
echo "    Logs:    ${LOG_DIR}/launchd-stdout.log"
echo "    CLI:     nagobot cli"
echo ""
echo "    To connect: nagobot cli"
