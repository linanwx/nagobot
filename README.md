# nagobot

Tired of endless configuration and unstable runtime? Try nagobot.

<p align="center">
  <img src="img/head.png" alt="nagobot head" width="120" />
</p>

`nagobot` is an ultra-light AI assistant built with Go.

Inspired by nanobot (`github.com/HKUDS/nanobot`) and openclaw (`github.com/openclaw`).

This project is evolving rapidly.

## Features

Multi-provider AI assistant with tool calling, async multi-threading, cron scheduling, web search, and context compression — deployable via Telegram, Web, or CLI.

## Supported Providers and Model Types

`nagobot` enforces a model whitelist. Only validated provider/model pairs are supported:

- `openai`: `gpt-5.2` (OAuth or API key)
- `deepseek`: `deepseek-reasoner`, `deepseek-chat` (recommended default)
- `openrouter`: `moonshotai/kimi-k2.5`
- `anthropic`: `claude-sonnet-4-5`, `claude-opus-4-6`
- `moonshot-cn`: `kimi-k2.5`
- `moonshot-global`: `kimi-k2.5`
- `zhipu-cn`: `glm-5`
- `zhipu-global`: `glm-5`
- `minimax-cn`: `minimax-m2.5`
- `minimax-global`: `minimax-m2.5`

### Developer notes

- **deepseek**: Incredibly good. No image support and reasoning speed could be better, but otherwise excellent.
- **gpt-5.2**: Fast and pleasant, but the model is argumentative — it prefers debating details over executing tasks.
- **kimi-k2.5**: Fast, but occasionally hallucinates tool calls. Not fully stable.
- **glm-5**: Average speed. Occasional hallucinated tool calls.
- **minimax-m2.5**: Very poor real-world experience — frequent hallucinated tool calls and fabricated results. Not recommended.
- **anthropic**: Great at everything, except the price.

## Requirements

- Go `1.23.3+`

## Build

```bash
go build -o nagobot .
```

## Quick Start

1. Run the interactive setup wizard:

```bash
./nagobot onboard
```

The wizard will guide you through provider selection, API key setup, and optional Telegram configuration.

The project may change drastically between versions. Please re-run `onboard` after updating.

2. Start the service:

```bash
./nagobot serve
```

## Documentation

- [Provider config examples](docs/provider.md)
- [Channels (Telegram, Web, CLI)](docs/channels.md)

## Play

Don't know how to use it? Try these example prompts:

> Create a job that runs at 9am, 12pm, and 6pm every day. Based on my conversation history, search news for me.

> I want you to search for recent stock market topics, please create 3 threads to search.
