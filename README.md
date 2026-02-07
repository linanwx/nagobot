# nagobot

Tired of endless configuration and unstable runtime? Try nagobot.

<p align="center">
  <img src="img/head.png" alt="nagobot head" width="120" />
</p>

`nagobot` is a ultra light AI assistant built with Go.

Inspired by nanobot (`github.com/HKUDS/nanobot`) and openclaw (`github.com/openclaw`).

This project is evolving rapidly.

## Features

- Providers: `openrouter`, `anthropic`, `deepseek`, `moonshot-cn`, `moonshot-global`
- Tools
- Skills
- Agent
- Cron

## Supported Providers and Model Types

`nagobot` enforces a model whitelist. Only validated provider/model pairs are supported:

- `openrouter`: `moonshotai/kimi-k2.5`
- `anthropic`: `claude-sonnet-4-5`, `claude-opus-4-6`
- `deepseek`: `deepseek-chat`, `deepseek-reasoner`
- `moonshot-cn`: `kimi-k2.5`
- `moonshot-global`: `kimi-k2.5`

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

### Provider Config Examples

OpenRouter (Kimi K2.5):

```yaml
agents:
  defaults:
    provider: openrouter
    modelType: moonshotai/kimi-k2.5
    # modelName: your-openrouter-preset-or-alias # optional

providers:
  openrouter:
    apiKey: sk-or-v1-xxx
```

When using `moonshotai/kimi-k2.5`, route to OpenRouter's official `moonshot` provider.
If routing falls back to other upstream providers, chain-of-thought and tool-calling can fail.

DeepSeek config example:

```yaml
agents:
  defaults:
    provider: deepseek
    modelType: deepseek-reasoner # or deepseek-chat

providers:
  deepseek:
    apiKey: sk-xxx
    # apiBase: https://api.deepseek.com # optional
```

Anthropic config example:

```yaml
agents:
  defaults:
    provider: anthropic
    modelType: claude-opus-4-6 # or claude-sonnet-4-5

providers:
  anthropic:
    apiKey: sk-ant-xxx
    # apiBase: https://api.anthropic.com # optional
```

Moonshot CN (official) config example:

```yaml
agents:
  defaults:
    provider: moonshot-cn
    modelType: kimi-k2.5

providers:
  moonshotCN:
    apiKey: sk-xxx
    # apiBase: https://api.moonshot.cn/v1 # optional
```

Moonshot Global (official) config example:

```yaml
agents:
  defaults:
    provider: moonshot-global
    modelType: kimi-k2.5

providers:
  moonshotGlobal:
    apiKey: sk-xxx
    # apiBase: https://api.moonshot.ai/v1 # optional
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
  adminUserID: "1234567890" # Optional: open @userinfobot in Telegram, send /start, and paste your user ID here
  telegram:
    token: "1234567890:AA***************" # Open @BotFather in Telegram, run /newbot, and paste the generated token here
    allowedIds:
      - 1234567890 # Open @userinfobot in Telegram, send /start, and paste allowed user IDs here
```
