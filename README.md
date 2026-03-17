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
