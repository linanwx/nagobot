#!/usr/bin/env bash
set -euo pipefail

# nagobot Docker install.
#
#   curl -fsSL https://nagobot.com/docker_install.sh | bash
#
# Everything lives in ~/.nagobot — data, config.yaml, docker-compose.yml and
# .env — mirroring the native install layout, so native <-> container
# migration is zero-copy and backup/uninstall is one directory.
#
# Idempotent: re-running updates the image and restarts the container without
# touching an existing docker-compose.yml / .env / config.yaml.
#
# Non-interactive key setup: pass a provider key via env, e.g.
#   DEEPSEEK_API_KEY=sk-... curl -fsSL https://nagobot.com/docker_install.sh | bash
# With no key in env and a usable TTY, the script prompts for one. With
# neither, it still starts the container and prints how to add a key.

NAGOBOT_HOME="${NAGOBOT_HOME:-$HOME/.nagobot}"
COMPOSE_FILE="$NAGOBOT_HOME/docker-compose.yml"
ENV_FILE="$NAGOBOT_HOME/.env"
IMAGE="ghcr.io/linanwx/nagobot:latest"

say()  { printf '%s\n' "$*"; }
fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }

# ── Docker availability ──────────────────────────────────────────────────────
if ! command -v docker >/dev/null 2>&1; then
  case "$(uname -s)" in
    Darwin) fail "docker not found. Install OrbStack (https://orbstack.dev) or Docker Desktop, then re-run." ;;
    *)      fail "docker not found. Install it with: curl -fsSL https://get.docker.com | sh" ;;
  esac
fi
docker info >/dev/null 2>&1 || fail "docker daemon is not running. Start it and re-run."
docker compose version >/dev/null 2>&1 || fail "docker compose plugin not found. Update Docker, or install docker-compose-plugin."

mkdir -p "$NAGOBOT_HOME"

# ── Compose file (only written if absent — user edits are preserved) ─────────
if [ ! -f "$COMPOSE_FILE" ]; then
  cat > "$COMPOSE_FILE" <<'EOF'
# nagobot container deployment. This file lives inside the data directory
# (~/.nagobot) so config, sessions and deployment travel together.
# Update: docker compose -f ~/.nagobot/docker-compose.yml pull && \
#         docker compose -f ~/.nagobot/docker-compose.yml up -d
# (or just re-run the install script.)

services:
  nagobot:
    image: ghcr.io/linanwx/nagobot:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      # "." = the directory containing this compose file, i.e. ~/.nagobot.
      - .:/root/.nagobot
    environment:
      # First-boot config bootstrap (ignored once config.yaml exists):
      - NAGOBOT_PROVIDER=${NAGOBOT_PROVIDER:-deepseek}
      - NAGOBOT_MODEL=${NAGOBOT_MODEL:-deepseek-v4-flash}
      # Provider keys / channel tokens — set what you have in ~/.nagobot/.env.
      # SILICONFLOW_API_KEY / OPENROUTER_API_KEY also power pre-think embedding.
      - DEEPSEEK_API_KEY=${DEEPSEEK_API_KEY:-}
      - OPENAI_API_KEY=${OPENAI_API_KEY:-}
      - OPENROUTER_API_KEY=${OPENROUTER_API_KEY:-}
      - SILICONFLOW_API_KEY=${SILICONFLOW_API_KEY:-}
      - MOONSHOT_API_KEY=${MOONSHOT_API_KEY:-}
      - TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN:-}
      - DISCORD_BOT_TOKEN=${DISCORD_BOT_TOKEN:-}
      - TZ=${TZ:-UTC}
EOF
  say "Wrote $COMPOSE_FILE"
else
  say "Keeping existing $COMPOSE_FILE"
fi

# ── .env assembly ────────────────────────────────────────────────────────────
touch "$ENV_FILE"

# env_has KEY — true if .env already defines KEY (possibly empty).
env_has() { grep -q "^$1=" "$ENV_FILE" 2>/dev/null; }
# env_set KEY VALUE — append KEY=VALUE unless KEY already present.
env_set() { env_has "$1" || printf '%s=%s\n' "$1" "$2" >> "$ENV_FILE"; }

# Detect host timezone (best effort) so wake timestamps match local time.
if ! env_has TZ; then
  TZ_GUESS=""
  if [ -f /etc/timezone ]; then
    TZ_GUESS="$(cat /etc/timezone)"
  elif [ -L /etc/localtime ]; then
    TZ_GUESS="$(readlink /etc/localtime | sed 's|.*/zoneinfo/||')"
  fi
  [ -n "$TZ_GUESS" ] && env_set TZ "$TZ_GUESS"
fi

# Chat-provider keys the script understands: env var / provider name / model.
KEY_VARS=(DEEPSEEK_API_KEY OPENAI_API_KEY OPENROUTER_API_KEY MOONSHOT_API_KEY)
provider_for() {
  case "$1" in
    DEEPSEEK_API_KEY)   echo "deepseek deepseek-v4-flash" ;;
    OPENAI_API_KEY)     echo "openai gpt-5.5" ;;
    OPENROUTER_API_KEY) echo "openrouter moonshotai/kimi-k2.6" ;;
    MOONSHOT_API_KEY)   echo "moonshot-cn kimi-k3" ;;
  esac
}

have_key=""
# 1. Keys passed via environment win.
for var in "${KEY_VARS[@]}"; do
  val="${!var:-}"
  if [ -n "$val" ]; then
    env_set "$var" "$val"
    if [ -z "$have_key" ]; then
      read -r prov model <<< "$(provider_for "$var")"
      env_set NAGOBOT_PROVIDER "$prov"
      env_set NAGOBOT_MODEL "$model"
      have_key="$var"
    fi
  fi
done
# Embedding-only key: record it, but it doesn't count as a chat provider.
[ -n "${SILICONFLOW_API_KEY:-}" ] && env_set SILICONFLOW_API_KEY "$SILICONFLOW_API_KEY"

# 2. Otherwise check whether .env already has a non-empty key.
if [ -z "$have_key" ]; then
  for var in "${KEY_VARS[@]}"; do
    if grep -q "^$var=..*" "$ENV_FILE" 2>/dev/null; then have_key="$var"; break; fi
  done
fi

# 3. Otherwise prompt, if a TTY is available (works under `curl | bash`).
if [ -z "$have_key" ] && [ -r /dev/tty ] && [ -w /dev/tty ]; then
  {
    say ""
    say "nagobot needs at least one LLM provider API key to chat."
    say "  1) deepseek     (recommended — https://platform.deepseek.com)"
    say "  2) openai       (https://platform.openai.com)"
    say "  3) openrouter   (https://openrouter.ai/keys)"
    say "  4) moonshot     (https://platform.moonshot.cn)"
    say "  5) skip for now"
    printf "Choose [1-5]: "
  } > /dev/tty
  choice="$(head -n1 < /dev/tty || true)"
  var=""
  case "$choice" in
    1) var=DEEPSEEK_API_KEY ;;
    2) var=OPENAI_API_KEY ;;
    3) var=OPENROUTER_API_KEY ;;
    4) var=MOONSHOT_API_KEY ;;
  esac
  if [ -n "$var" ]; then
    printf "Paste your %s: " "$var" > /dev/tty
    key="$(head -n1 < /dev/tty || true)"
    if [ -n "$key" ]; then
      env_set "$var" "$key"
      read -r prov model <<< "$(provider_for "$var")"
      env_set NAGOBOT_PROVIDER "$prov"
      env_set NAGOBOT_MODEL "$model"
      have_key="$var"
    fi
  fi
fi

# ── Pull & start ─────────────────────────────────────────────────────────────
say ""
say "Pulling $IMAGE ..."
docker compose -f "$COMPOSE_FILE" pull
docker compose -f "$COMPOSE_FILE" up -d

say ""
say "nagobot is running."
say "  Web UI:   http://localhost:8080"
say "  Data:     $NAGOBOT_HOME (config.yaml, sessions, memory)"
say "  Logs:     docker compose -f $COMPOSE_FILE logs -f"
say "  Update:   re-run this script, or: docker compose -f $COMPOSE_FILE pull && docker compose -f $COMPOSE_FILE up -d"
if [ -z "$have_key" ]; then
  say ""
  say "No provider key configured yet — chat will not work until you add one:"
  say "  1. Edit $ENV_FILE (e.g. DEEPSEEK_API_KEY=sk-...)"
  say "  2. docker compose -f $COMPOSE_FILE up -d"
fi
say ""
say "Optional: add SILICONFLOW_API_KEY or OPENROUTER_API_KEY to $ENV_FILE to"
say "enable semantic pre-think (powers destructive-action detection), and"
say "TELEGRAM_BOT_TOKEN / DISCORD_BOT_TOKEN to connect chat channels."
