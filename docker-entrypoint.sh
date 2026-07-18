#!/bin/sh
# Container entrypoint: bootstrap config on first run, sync workspace
# templates, then run the given command (default: serve).
#
# Provider keys and channel tokens come from env at call time (DEEPSEEK_API_KEY,
# OPENAI_API_KEY, MOONSHOT_API_KEY, TELEGRAM_BOT_TOKEN, DISCORD_BOT_TOKEN, ...)
# — they never need to be in config.yaml, so a bare volume plus one key is a
# complete deployment. Model routing is adjusted afterwards from chat or CLI
# (nagobot set-model).
set -e

CONFIG_DIR="${HOME}/.nagobot"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"

if [ ! -f "$CONFIG_FILE" ]; then
  mkdir -p "$CONFIG_DIR"
  cat > "$CONFIG_FILE" <<EOF
thread:
  provider: ${NAGOBOT_PROVIDER:-deepseek}
  modelType: ${NAGOBOT_MODEL:-deepseek-v4-flash}
channels:
  web:
    addr: ${NAGOBOT_WEB_ADDR:-0.0.0.0:8080}
EOF
  echo "Bootstrapped ${CONFIG_FILE} (provider=${NAGOBOT_PROVIDER:-deepseek})"
fi

# Template sync only matters for the long-running daemon; one-off CLI
# invocations (--version, cron, set-model) skip it.
if [ "$1" = "serve" ]; then
  nagobot onboard --sync
fi

exec nagobot "$@"
