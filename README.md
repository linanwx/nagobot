# nagobot

Tired of endless configuration and unstable runtime? Try [nagobot](https://nagobot.com).

<p align="center">
  <img src="img/head.png" alt="nagobot head" width="120" />
</p>

`nagobot` is an ultra-light AI assistant built with Go. One install, all channels.

**[Website](https://nagobot.com)** · **[Releases](https://github.com/linanwx/nagobot/releases)** · **[Documentation](https://nagobot.com)**

Inspired by nanobot (`github.com/HKUDS/nanobot`) and openclaw (`github.com/openclaw`).

This project is evolving rapidly.

## Features

Multi-provider AI assistant with tool calling, async multi-threading, cron scheduling, web search, and context compression — deployable via Telegram, Web, or CLI.

## Supported Providers and Model Types

`nagobot` enforces a model whitelist. Only validated provider/model pairs are supported:

- `openai`: `gpt-5.2` (OAuth or API key)
- `deepseek`: `deepseek-reasoner`, `deepseek-chat` (recommended default)
- `openrouter`: `moonshotai/kimi-k2.5`, `anthropic/claude-sonnet-4.6`, `anthropic/claude-opus-4.6`, `z-ai/glm-5`
- `anthropic`: `claude-sonnet-4-6`, `claude-opus-4-6`
- `moonshot-cn`: `kimi-k2.5`
- `moonshot-global`: `kimi-k2.5`
- `zhipu-cn`: `glm-5`
- `zhipu-global`: `glm-5`
- `minimax-cn`: `minimax-m2.5`
- `minimax-global`: `minimax-m2.5`

## Channels

- Telegram
- Discord
- Web
- CLI

Recommended: deepseek-reasoner for chat, kimi-k2.5 for tool calls.

## Requirements

- Go `1.23.3+`

## Build

```bash
go build -o nagobot .
```

## Install

```bash
curl -fsSL https://nagobot.com/install.sh | bash
```

Windows (PowerShell):
```powershell
irm https://nagobot.com/install.ps1 | iex
```

## Quick Start

1. Run the interactive setup wizard:

```bash
nagobot onboard
```

The wizard will guide you through provider selection, API key setup, and optional Telegram configuration.

The wizard will also install the service, which starts automatically on boot.

The project may change drastically between versions. Please re-run `onboard` after updating.

## Documentation

- [Provider config examples](docs/provider.md)
- [Channels (Telegram, Discord, Web, CLI)](docs/channels.md)

## Play

Don't know how to use it? Try these example prompts:

> Create a job that runs at 9am, 12pm, and 6pm every day. Based on my conversation history, search news for me.

> I want you to search for recent stock market topics, please create 3 threads to search.
