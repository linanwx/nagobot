# nagobot

<p align="center">
  <img src="img/head.png" alt="nagobot head" width="720" />
</p>

`nagobot` is a ultra light AI assistant built with Go.

Inspired by nanobot (`github.com/HKUDS/nanobot`) and openclaw (`github.com/openclaw`).

This project is evolving rapidly.

## Features

- Providers: `openrouter`, `anthropic`
- Tools
- Skills
- Agent

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

### Important: Kimi K2.5 + OpenRouter

When using `moonshotai/kimi-k2.5`, tool-calling reliability is highly dependent on the upstream provider.

- Recommended: in OpenRouter routing, allow only `moonshot` as provider for this model.
- Alternative: set `agents.defaults.modelName` to an OpenRouter preset/model alias that already pins routing to Moonshot.

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
