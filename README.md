# nagobot

Tired of endless configuration and unstable runtime? Try nagobot.

<p align="center">
  <img src="img/head.png" alt="nagobot head" width="120" />
</p>

`nagobot` is a ultra light AI assistant built with Go.

Inspired by nanobot (`github.com/HKUDS/nanobot`) and openclaw (`github.com/openclaw`).

This project is evolving rapidly.

## Features

- Providers: `openrouter`, `anthropic`, `deepseek`
- Tools
- Skills
- Agent
- Cron

## Supported Providers and Model Types

`nagobot` enforces a model whitelist. Only validated provider/model pairs are supported:

- `openrouter`: `moonshotai/kimi-k2.5`
- `anthropic`: `claude-sonnet-4-5`, `claude-opus-4-6`
- `deepseek`: `deepseek-chat`, `deepseek-reasoner`

For OpenRouter, support is currently **whitelist-only**. Only verified model routes are treated as supported. In particular, reasoning/chain-of-thought behavior and tool-calling are guaranteed only for validated routes.

## Requirements

- Go `1.23.3+`

## Build

```bash
go build -o nagobot .
```

## Quick Start

1. Initialize config and workspace:

```bash
./nagobot onboard
```

2. Edit config (default: `~/.nagobot/config.yaml`) and set your API key.

For example:

```yaml
providers:
  openrouter:
    apiKey: sk-or-v1-xxx
```

You can use default model: **moonshotai/kimi-k2.5**

DeepSeek config example:

```yaml
agents:
  defaults:
    provider: deepseek
    modelType: deepseek-chat # or deepseek-reasoner

providers:
  deepseek:
    apiKey: sk-xxx
    # apiBase: https://api.deepseek.com # optional
```

### Important: Kimi K2.5 + OpenRouter

When using `moonshotai/kimi-k2.5`, you must route to OpenRouter's official `moonshot` provider. If routing falls back to other upstream providers, chain-of-thought and tool-calling are not supported, and tool-calling can fail.

- Required: in OpenRouter routing, allow only `moonshot` as provider for this model.
- Alternative: set `agents.defaults.modelName` to an OpenRouter preset/model alias that already pins routing to Moonshot.
- Runtime reminder: when nagobot detects `openrouter + kimi`, it prints a terminal warning about this requirement.

Example:

```yaml
agents:
  defaults:
    provider: openrouter
    modelType: moonshotai/kimi-k2.5
    modelName: your-openrouter-preset-or-alias
```

3. Start service:

```bash
./nagobot serve
```

## Channels (`serve`)

```bash
# CLI (default)
./nagobot serve

# Enable Telegram
./nagobot serve --telegram

# Enable all configured channels
./nagobot serve --all
```

Telegram config example (token redacted):

```yaml
channels:
  adminUserID: "1234567890" # Optional
  telegram:
    token: "1234567890:AA***************"
    allowedIds:
      - 1234567890
```
