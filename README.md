# nagobot

Tired of endless configuration and unstable runtime? Try nagobot.

<p align="center">
  <img src="img/head.png" alt="nagobot head" width="120" />
</p>

`nagobot` is an ultra-light AI assistant built with Go.

Inspired by nanobot (`github.com/HKUDS/nanobot`) and openclaw (`github.com/openclaw`).

This project is evolving rapidly.

## Features

- Providers: `openrouter`, `anthropic`, `deepseek`, `moonshot-cn`, `moonshot-global`
- Tools
- Skills
- Agent
- Cron
- Async
- Multi Thread

- Web search

## Supported Providers and Model Types

`nagobot` enforces a model whitelist. Only validated provider/model pairs are supported:

Default recommendation (unless you need a specific vendor): `provider=deepseek`, `modelType=deepseek-reasoner`.

- `deepseek`: `deepseek-reasoner`, `deepseek-chat` (recommended default)
- `openrouter`: `moonshotai/kimi-k2.5`
- `anthropic`: `claude-sonnet-4-5`, `claude-opus-4-6`
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
If you don't have a provider preference, start with `deepseek-reasoner` (it offers good performance and low cost, with a low startup charge starting from about 2 dollars).

### Provider Config Examples

DeepSeek config example:

```yaml
agents:
  defaults:
    provider: deepseek
    modelType: deepseek-reasoner

providers:
  deepseek:
    apiKey: sk-xxx # get via platform.deepseek.com
    # apiBase: https://api.deepseek.com # optional
```

Other provider config examples: [docs/provider.md](docs/provider.md)

3. Start service:

```bash
./nagobot serve
```

## Channels (`serve`)

```bash
# CLI (default)
./nagobot serve

# Enable all configured channels, including Telegram
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

## Play

Don't know how to use it? Try these example prompts:

```
Create a job that runs at 9am, 12pm, and 6pm every day. Based on my conversation history, search news for me.
```

```
I want you to search for recent stock market topics and do it in async mode.
```
