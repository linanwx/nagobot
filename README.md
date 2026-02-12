# nagobot

Tired of endless configuration and unstable runtime? Try nagobot.

<p align="center">
  <img src="img/head.png" alt="nagobot head" width="120" />
</p>

`nagobot` is an ultra-light AI assistant built with Go.

Inspired by nanobot (`github.com/HKUDS/nanobot`) and openclaw (`github.com/openclaw`).

This project is evolving rapidly.

## Features

- Providers: `openai`, `deepseek`, `openrouter`, `anthropic`, `moonshot-cn`, `moonshot-global`
- Tools
- Skills
  - context compression
- Agent
- Cron
- Async
- Multi Thread
- Web search

## Supported Providers and Model Types

`nagobot` enforces a model whitelist. Only validated provider/model pairs are supported:

- `openai`: `gpt-5.2` (OAuth or API key)
- `deepseek`: `deepseek-reasoner`, `deepseek-chat` (recommended default)
- `openrouter`: `moonshotai/kimi-k2.5`
- `anthropic`: `claude-sonnet-4-5`, `claude-opus-4-6`
- `moonshot-cn`: `kimi-k2.5`
- `moonshot-global`: `kimi-k2.5`

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
- [Channels (Telegram, Feishu, Web, CLI)](docs/channels.md)

## Play

Don't know how to use it? Try these example prompts:

> Create a job that runs at 9am, 12pm, and 6pm every day. Based on my conversation history, search news for me.

> I want you to search for recent stock market topics, please create 3 threads to search.
