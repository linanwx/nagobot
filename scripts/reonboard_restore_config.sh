#!/usr/bin/env bash
set -euo pipefail

CONFIG_DIR="${1:-$HOME/.nagobot}"
CONFIG_PATH="$CONFIG_DIR/config.yaml"
WORKSPACE_PATH=""
CRON_PATH=""
SESSIONS_PATH=""

BACKUP_DIR=""
BACKUP_CONFIG_PATH=""
BACKUP_CRON_PATH=""
BACKUP_SESSIONS_PATH=""
RESTORED=0

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

run_nagobot_cmd() {
  (
    cd "$REPO_ROOT"
    go run . --config-dir "$CONFIG_DIR" "$@"
  )
}

resolve_workspace_path() {
  local workspace
  workspace="$(run_nagobot_cmd status 2>/dev/null | sed -n 's/^Workspace:[[:space:]]*//p' | head -n1)"
  if [[ -z "$workspace" ]]; then
    workspace="$CONFIG_DIR/workspace"
  fi
  printf '%s\n' "$workspace"
}

cleanup_backups() {
  if [[ -n "$BACKUP_DIR" && -d "$BACKUP_DIR" ]]; then
    rm -rf "$BACKUP_DIR"
  fi
}

restore_state() {
  if [[ "$RESTORED" -eq 0 && -n "$BACKUP_CONFIG_PATH" && -f "$BACKUP_CONFIG_PATH" ]]; then
    mkdir -p "$CONFIG_DIR"
    cp "$BACKUP_CONFIG_PATH" "$CONFIG_PATH"
    echo "Restored config to: $CONFIG_PATH"

    if [[ -n "$BACKUP_CRON_PATH" && -f "$BACKUP_CRON_PATH" ]]; then
      mkdir -p "$WORKSPACE_PATH"
      cp "$BACKUP_CRON_PATH" "$CRON_PATH"
      echo "Restored cron file to: $CRON_PATH"
    fi

    if [[ -n "$BACKUP_SESSIONS_PATH" && -d "$BACKUP_SESSIONS_PATH" ]]; then
      mkdir -p "$WORKSPACE_PATH"
      rm -rf "$SESSIONS_PATH"
      cp -R "$BACKUP_SESSIONS_PATH" "$SESSIONS_PATH"
      echo "Restored sessions dir to: $SESSIONS_PATH"
    fi

    RESTORED=1
  fi

  cleanup_backups
}

trap restore_state EXIT

if [[ ! -f "$CONFIG_PATH" ]]; then
  echo "Config not found: $CONFIG_PATH" >&2
  exit 1
fi

if ! command -v trash >/dev/null 2>&1; then
  echo "'trash' command not found. Install it first (e.g. brew install trash)." >&2
  exit 1
fi

WORKSPACE_PATH="$(resolve_workspace_path)"
CRON_PATH="$WORKSPACE_PATH/cron.jsonl"
SESSIONS_PATH="$WORKSPACE_PATH/sessions"

BACKUP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/nagobot-reonboard-backup-XXXXXX")"
BACKUP_CONFIG_PATH="$BACKUP_DIR/config.yaml"
BACKUP_CRON_PATH="$BACKUP_DIR/cron.jsonl"
BACKUP_SESSIONS_PATH="$BACKUP_DIR/sessions"

cp "$CONFIG_PATH" "$BACKUP_CONFIG_PATH"
echo "Backed up config to: $BACKUP_CONFIG_PATH"

if [[ -f "$CRON_PATH" ]]; then
  cp "$CRON_PATH" "$BACKUP_CRON_PATH"
  echo "Backed up cron file to: $BACKUP_CRON_PATH"
else
  BACKUP_CRON_PATH=""
fi

if [[ -d "$SESSIONS_PATH" ]]; then
  cp -R "$SESSIONS_PATH" "$BACKUP_SESSIONS_PATH"
  echo "Backed up sessions dir to: $BACKUP_SESSIONS_PATH"
else
  BACKUP_SESSIONS_PATH=""
fi

echo "Trashing config directory: $CONFIG_DIR"
trash "$CONFIG_DIR"

echo "Running onboard in repo: $REPO_ROOT"
run_nagobot_cmd onboard

# Restore immediately after onboard; trap also protects failure paths.
restore_state
trap - EXIT

echo "Done."
