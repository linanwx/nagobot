# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o nagobot .          # Build
go test ./...                   # Run all tests
nagobot update                  # Self-update from GitHub Releases
```

Single package test: `go test ./provider -v -run TestSanitize`

## Architecture

nagobot is a Go-based AI bot framework. Messages flow through four layers:

```
Channel (I/O) → Dispatcher (routing) → Thread (execution) → Provider (LLM)
```

### Channel → Dispatcher (`channel/` → `cmd/dispatcher.go`)

Channels are pure I/O (Telegram, Discord, Feishu, Web, CLI, Cron). Each produces `channel.Message` structs. The Dispatcher routes messages to threads by computing a `sessionKey` (e.g., `"telegram:123456"`) and wrapping the message into a `WakeMessage` with Source, Sink, AgentName, and Vars.

The `/init` command is intercepted in the Dispatcher and executed directly via `initCmd.ParseFlags()` + `RunE()` — it does NOT go through the thread/LLM pipeline.

### Thread Manager (`thread/manager.go`)

Schedules up to 16 concurrent threads. `Manager.Wake(sessionKey, msg)` creates a thread if needed and enqueues the message. The Run() loop picks runnable threads and calls `RunOnce()`. Idle threads are GC'd after 3 hours.

### Thread Execution (`thread/run.go`, `thread/wake.go`, `thread/runner.go`)

`RunOnce()` dequeues a WakeMessage, merges consecutive same-source messages, builds the prompt, and runs the agentic loop (LLM call → tool execution → repeat). The `Runner` handles the iteration loop with hooks for streaming, message injection, and halt conditions.

Key: `resolveProvider()` calls `ProviderFactory.Create()` each time (not cached) so config changes from `/init` take effect immediately.

### WakeMessage Format (`thread/wake.go`)

Wake payloads use XML with per-source visibility:
- User messages (telegram/discord/cli/etc): `<message visibility="user-visible">`
- System messages (child_completed/cron/sleep/etc): `<message visibility="assistant-only">`

Action hints for assistant-only sources explicitly tell the AI to include content in its response.

### Agent Templates (`agent/`)

Agents are markdown templates in `{workspace}/agents/{name}.md` with `{{PLACEHOLDER}}` syntax. Variables set via `agent.Set(key, value)` before `Build()`. Runtime vars (TIME, TOOLS, SKILLS, USER) are set per-turn in `thread/run.go`.

### Provider Layer (`provider/`)

Each provider implements `Provider.Chat(ctx, *Request) (*Response, error)`. The `ProviderFactory` creates providers on demand, re-reading config each call. Providers enforce model whitelists. `SanitizeMessages()` removes orphaned tool messages before API calls.

### Tools (`tools/`)

Tools implement `Def() ToolDef` + `Run(ctx, args) string`. Registered in a `Registry`, cloned per-thread. Search and fetch tools use `SearchProvider`/`FetchProvider` interfaces with runtime `Available()` checks.

### Sessions (`session/`)

Conversation history persisted as `{sessionsDir}/{sessionKey}/session.jsonl`. Auto-sanitized on save. Context pressure hooks trigger compression when token budget is exceeded.

## Key Patterns

- **Hot-reload config**: Provider keys use `KeyFn` closures that call `config.Load()` each invocation. `Available()` checks at call time, not registration time. Channels (Telegram/Discord/Feishu) are hot-reloaded every 10s — adding a token to config auto-starts the channel.
- **Per-wake sink**: Each WakeMessage carries its own Sink callback for response delivery. Zero Sink falls back to thread default.
- **Agent override**: `WakeMessage.AgentName` overrides the thread's agent for that turn only.
- **Async child threads**: `SpawnChild()` is fully async. Child completion wakes parent via Sink → Enqueue.
- **Template workspace**: Canonical templates live in `cmd/templates/`. `onboard --sync` copies to `~/.nagobot/workspace/`. Never edit workspace files directly.

## Deployment

Install: `curl -fsSL https://nagobot.com/install.sh | bash` (all platforms). Update: `nagobot update`.

Service managed via launchd (macOS), systemd (Linux), or Task Scheduler (Windows). Logs at `~/.nagobot/logs/`.

Release pipeline: push `v*` tag → GitHub Actions builds all platform binaries (linux/darwin/windows) → publishes to GitHub Releases.
