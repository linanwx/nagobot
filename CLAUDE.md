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

Wake payloads use YAML frontmatter + markdown body with per-source visibility:
- User messages (telegram/discord/cli/etc): `visibility: user-visible`
- System messages (child_completed/cron/sleep/heartbeat/etc): `visibility: assistant-only`

Action hints for assistant-only sources explicitly tell the AI to include content in its response.

### Agent Templates (`agent/`)

Agents are markdown templates in `{workspace}/agents/{name}.md` with `{{PLACEHOLDER}}` syntax. Variables set via `agent.Set(key, value)` before `Build()`. Runtime vars (TOOLS, SKILLS, USER) are set per-turn in `thread/run.go`. `{{DATE}}` and `{{CALENDAR}}` are auto-resolved in `agent.Build()` at day-level granularity (no minutes/seconds).

**Important**: `{{WORKSPACE}}` is resolved in both `agent.Build()` and `use_skill` (`tools/skills.go`). Skills should use `{{WORKSPACE}}/bin/nagobot` for CLI calls.

### Provider Layer (`provider/`)

Each provider implements `Provider.Chat(ctx, *Request) (*Response, error)`. The `ProviderFactory` creates providers on demand, re-reading config each call. Providers enforce model whitelists. `SanitizeMessages()` removes orphaned tool messages before API calls.

### Tools (`tools/`)

Tools implement `Def() ToolDef` + `Run(ctx, args) string`. Registered in a `Registry`, cloned per-thread. Search and fetch tools use `SearchProvider`/`FetchProvider` interfaces with runtime `Available()` checks.

`sleep_thread` is a unified tool registered at thread level. No args = end turn silently; `duration` param = schedule a cron wake-up. During heartbeat turns, passing `duration` is rejected — the heartbeat scheduler manages its own timing. The tool checks `IsHeartbeatWake()` at runtime, not via separate tool definitions (important: a single tool definition keeps the tools array stable for prompt caching).

### Audio Support

Audio recognition follows the same pattern as vision: `AudioModels` registered per provider, `SupportsAudio()` capability check, `<<media:audio/ogg:path>>` markers, and `audioreader` agent delegation for non-audio models.

- **Channel layer**: Telegram Voice/Audio and Discord audio attachments are downloaded to `{workspace}/media/` (same `downloadMedia()` as images).
- **Tool layer**: `DetectFileType` recognizes `FileTypeAudio` via extension + magic bytes. `handleAudio()` returns media marker if `SupportsAudio`, otherwise guides LLM to delegate to `audioreader`.
- **Provider layer**: OpenRouter sends audio markers as `input_audio` content parts. Gemini uses generic `inlineData`. Non-audio providers skip audio markers.
- **Token estimation**: `EstimateAudioTokens()` uses file size + bitrate heuristic, ~32 tokens/sec.
- **audioreader agent**: `specialty: audio`, configured during `onboard` (same flow as imagereader specialty routing).

### Sessions (`session/`)

Conversation history persisted as `{sessionsDir}/{sessionKey}/session.jsonl`. Auto-sanitized on save. Context pressure hooks trigger compression when token budget is exceeded.

## Session vs Thread — Critical Distinction

**Session** = persistent on-disk data (`session.jsonl`, `heartbeat.md`). Survives restarts, lives indefinitely.

**Thread** = transient in-memory execution unit. Created by `Manager.NewThread()`, GC'd after 3h idle. `NewThread()` initializes `lastUserActiveAt = time.Now()` — this is NOT a reliable indicator of when the user was actually last active. For accurate user activity timestamps, always scan `session.jsonl` (via `collectSessions` or `isRealUserSource`), not in-memory thread state.

**Rule**: Any scheduling or timing logic (heartbeat, compression eligibility) that needs `lastUserActiveAt` for sessions that may have been GC'd MUST read from `session.jsonl`, not from `Thread.lastUserActiveAt`. Threads are ephemeral — their state is lost on GC and reset on recreation.

## Heartbeat System (`cmd/heartbeat_scheduler.go`)

The heartbeat makes the bot proactive — monitoring conversations and acting on items between user interactions.

### Architecture

A Go goroutine (`heartbeatScheduler`) scans every 30s and fires heartbeat pulses into user sessions. NOT a cron job — the old cron-based dispatcher was removed.

Three skills collaborate:
- **heartbeat-wake**: thin router — decides reflect or act based on `heartbeat_modified` timestamp
- **heartbeat-reflect**: silent — reviews conversation, updates `heartbeat.md`, calls `sleep_thread()`
- **heartbeat-act**: visible — evaluates items, sends message to user if relevant, or `sleep_thread()`

### Timing

- **Quiet threshold**: 10 min after last user-visible message (`hbQuietMin`)
- **Pulse interval**: 30 min (`hbPulseInterval`)
- **Activity window**: 48h — stops pulsing if no user-visible activity within 48h
- **Schedule**: `lastActive+10m, +40m, +70m, ...` (10 min first pulse, then 30 min gaps)

### Critical Implementation Details

**Trigger timeline**: The pulse schedule is derived from `lastActive` (user's last message), NOT from `lastPulse`. `latestDueTrigger(lastActive, interval, now)` computes trigger points: `lastActive+10m, +40m, +70m, ...`. A pulse fires only when the latest trigger point > `lastPulse`. This means `lastPulse` is purely a dedup guard — it prevents re-firing within the same cycle but never determines when the next pulse should be.

**State persistence**: `lastPulse` is persisted to `{workspace}/system/heartbeat-state.json`. State survives restarts — no cold-start alignment logic needed.

**User activity source**: The scheduler uses `collectSessions()` (scans `session.jsonl` for `isRealUserSource`) to get accurate `lastUserActiveAt`. It does NOT use `Thread.lastUserActiveAt` because threads initialize this to `time.Now()` on creation, which would make heartbeat-created threads appear "just active."

**`heartbeat status` RPC**: The CLI command calls `heartbeat.status` RPC which reads the scheduler's persisted state (`lastPulse`, computed intervals). It does NOT compute independently — it reflects real scheduler state.

### CLI Commands

- `nagobot heartbeat status` — show real next pulse times from live scheduler (via RPC)
- `nagobot heartbeat postpone <session-key> <duration>` — delay pulses for a session

## Compression (`thread/compress.go`)

### Tier 1 — Mechanical (idle ≥5 min, always runs)

- Tool result compression (use_skill → header-only if outdated/old)
- Wake payload compression (strip redundant YAML fields)
- Body compression (large assistant-only content → head+tail)
- **Heartbeat turn trim**: marks entire heartbeat turns for removal if `isHeartbeatSkipTurn` returns true:
  - Requires `sleep_thread` was called (turn was deliberately silent)
  - AND no non-safe tools were called (safe: `sleep_thread`, `use_skill`, `read_file`, `write_file`)
  - Turns that sent a message to the user (no sleep_thread) are PRESERVED
- Reasoning trim (>2h old reasoning content excluded at send-time)

### Tier 2 — AI-driven (idle ≥30 min, tokens >65%)

Wakes thread with `WakeCompression` source, loads `context-ops` skill to summarize.

### Source Matching

Heartbeat source matching uses `strings.HasPrefix(source, "heartbeat")` to cover both new (`"heartbeat"`) and old (`"heartbeat_reflect"`, `"heartbeat_wake"`) source strings in existing sessions.

## Key Patterns

- **Hot-reload config**: Provider keys use `KeyFn` closures that call `config.Load()` each invocation. `Available()` checks at call time, not registration time. Channels (Telegram/Discord/Feishu) are hot-reloaded every 10s — adding a token to config auto-starts the channel.
- **Per-wake sink**: Each WakeMessage carries its own Sink callback for response delivery. Zero Sink falls back to thread default.
- **Agent override**: `WakeMessage.AgentName` overrides the thread's agent for that turn only.
- **Async child threads**: `SpawnChild()` is fully async. Child completion wakes parent via Sink → Enqueue.
- **Template workspace**: Canonical templates live in `cmd/templates/`. `onboard --sync` copies to `~/.nagobot/workspace/`. `cleanAndCopyEmbeddedDir` removes deleted templates. Never edit workspace files directly.
- **Default cron seeds**: Only `tidyup` (4am daily) + `session-summary` (every 6h). Heartbeat is NOT a cron job.
- **Prompt caching requires deterministic serialization**: All LLM providers use prefix-based prompt caching (tools → system → messages). Go map iteration is non-deterministic, so any map-derived output that ends up in the LLM request MUST be sorted. Currently sorted: `tools.Registry.Defs()`, `skills.Registry.List()`, `skills.Registry.SkillNames()`, `agent.buildSessionsSummary()`. When adding new map-iterated content to the system prompt or tools array, always sort the output.
- **Cache monitoring**: `provider.Usage.CachedTokens` flows through `Runner.totalUsage` → `monitor.TurnRecord` → `nagobot monitor --metrics` (per-provider `cacheHitRate`). All providers fill this field from their respective API response (OpenRouter/Moonshot/Zhipu/Minimax: `PromptTokensDetails.CachedTokens`; DeepSeek: `PromptCacheHitTokens`; Anthropic: `CacheReadInputTokens`; OpenAI/Gemini: not available).

## Common Pitfalls

- **Don't trust `Thread.lastUserActiveAt` for scheduling**: It's initialized to `time.Now()` on thread creation, not actual user activity. Use `collectSessions()` → `LastUserActiveAt` from `session.jsonl` scan.
- **Don't use `logger.Debug` for things you need to see**: Heartbeat scheduler activity, error conditions — use `Info` or `Warn`. Debug is invisible at default log level.
- **Heartbeat state is persisted**: `lastPulse` is saved to `heartbeat-state.json` after each pulse. Restarts reload this state — no cold-start special-casing needed.
- **`collectSessions` loads full session data**: Every call parses entire `session.jsonl` for all matching sessions. Don't call it in tight loops. The scheduler calls it every 30s — acceptable for small deployments.
- **`{{WORKSPACE}}` resolves in both agents and skills**: `agent.Build()` and `use_skill` (`tools/skills.go`) both replace `{{WORKSPACE}}`. Skills should use `{{WORKSPACE}}/bin/nagobot` for CLI calls.
- **Heartbeat turns suppress via LLM, not code**: The old `WakeHeartbeatReflect` had code-level `SetSuppressSink()`. Now both reflect and act use `WakeHeartbeat` — suppression relies on the LLM calling `sleep_thread()`. If the LLM forgets, output leaks to the user.
- **`applyDefaults()` only adds, never prunes**: If a cron seed is removed from `defaultCronSeeds()`, old entries in `config.yaml` persist. Manual cleanup may be needed after upgrades.

## Deployment

Install: `curl -fsSL https://nagobot.com/install.sh | bash` (all platforms). Update: `nagobot update`.

Service managed via launchd (macOS), systemd (Linux), or Task Scheduler (Windows). Logs at `~/.nagobot/logs/`.

Release pipeline: push `v*` tag → GitHub Actions builds all platform binaries (linux/darwin/windows) → publishes to GitHub Releases.
