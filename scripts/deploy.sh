#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

echo "==> Building nagobot..."
go build -o nagobot .

echo "==> Syncing templates..."
./nagobot onboard --sync

echo "==> Restarting service..."
launchctl unload ~/Library/LaunchAgents/com.nagobot.serve.plist 2>/dev/null || true
launchctl load ~/Library/LaunchAgents/com.nagobot.serve.plist

echo "==> Deploy complete"
