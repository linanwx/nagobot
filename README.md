# nagobot

<p align="center">
  <img src="img/head.png" alt="nagobot" width="120" />
</p>

<p align="center">
  Autonomous AI bot framework built with Go. Multi-channel, multi-provider, multi-agent.
</p>

<p align="center">
  <a href="https://nagobot.com">Website</a> · <a href="https://github.com/linanwx/nagobot/releases">Releases</a> · <a href="https://nagobot.com">Docs</a>
</p>

<p align="center">
  <img src="img/screenshot-web.png" alt="Web Dashboard" width="680" />
</p>

<p align="center">
  <img src="img/screenshot-telegram.png" alt="Telegram Bot" width="360" />
</p>

## Install

### Docker (recommended for servers)

```bash
curl -fsSL https://nagobot.com/docker_install.sh | bash
```

One command: it writes `~/.nagobot/docker-compose.yml`, asks for a provider API key (or takes one non-interactively: `DEEPSEEK_API_KEY=sk-... curl -fsSL https://nagobot.com/docker_install.sh | bash`), and starts the container. Web UI on port 8080 — login-gated: mint a one-time login link with `nagobot login-link` (in Docker: `docker compose -f ~/.nagobot/docker-compose.yml exec nagobot nagobot login-link`), open it, and register a passkey.

Everything lives in `~/.nagobot` — config, sessions, memory, plus the compose file and `.env` — the same layout as a native install, so migrating between native and container is zero-copy and backup/uninstall is one directory. Update by re-running the script, or `docker compose -f ~/.nagobot/docker-compose.yml pull && docker compose -f ~/.nagobot/docker-compose.yml up -d`.

The image (`ghcr.io/linanwx/nagobot`, amd64/arm64) ships python3 with document libs, poppler, node, and ripgrep, so the agent's exec tool is fully capable in the container. Add a `SILICONFLOW_API_KEY` or `OPENROUTER_API_KEY` to `~/.nagobot/.env` to enable semantic pre-think (recommended — it powers destructive-action detection).

### Native (macOS / Linux)

```bash
curl -fsSL https://nagobot.com/install.sh | bash
```

Windows (PowerShell):
```powershell
irm https://nagobot.com/install.ps1 | iex
```

Then run the setup wizard:
```bash
nagobot onboard
```

This handles provider selection, API keys, channel configuration, and service installation. Re-run after updating.

Start chatting:
```bash
nagobot cli
```

## What it does

- **Multi-provider** — DeepSeek, Gemini, Anthropic, OpenAI, OpenRouter, Moonshot, Minimax, Zhipu
- **Multi-channel** — Telegram, Discord, Feishu, Web, CLI
- **Multi-agent** — Custom agent templates with async thread spawning
- **Always on** — Cron scheduling, auto-restart, three-tier context compression
- **38+ skills** — Web search, code execution, file management, and more

## Build from source

```bash
go build -o nagobot .
```

## Docs

- [Providers](docs/provider.md)
- [Channels](docs/channels.md)
