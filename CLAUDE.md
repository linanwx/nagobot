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

Wake payloads use YAML frontmatter + markdown body with per-source sender:
- User messages (telegram/discord/cli/etc): `sender: user`
- System messages (child_completed/cron/sleep/heartbeat/etc): `sender: system`

Action hints for system sources explicitly tell the AI to include content in its response.

### Pre-Think (`thread/prethink*.go`, `embedding/`)

Every user message is analyzed before the main model sees it, producing the **action hint** in the wake payload. This used to be a blocking LLM call (a `fast` model, 10s timeout, 10 XML fields) into a `{sessionKey}:prethink` sibling session. **It is now local: regex + a local Ollama embedding model. No LLM call, no sibling session.** Warm cost is ~150ms.

Five of the ten fields survived; the other five were deleted:

| Field | How | Notes |
|---|---|---|
| `has_web_url` | regex | Strictly *more* accurate than the LLM, which missed real `https://` links |
| `is_include_investigator` | regex, mask-then-match | |
| `search` | regex gates → embedding prototype → regex fallback | |
| `destructive` | precision gates → regex ∪ embedding, **reads recent chat** | "执行吧" is only dangerous because of the turn above it |
| `skills` | embedding retrieval over skill descriptions | Cannot hallucinate a slug that doesn't exist |
| ~~`tone`~~ | deleted | 83% constant, copied from USER.md |
| ~~`is_multi_step`~~ | deleted | The LLM's verdict was effectively `len(msg) > 160`; an embedding classifier scored *below* an always-false baseline |
| ~~`hallucination`~~ ~~`needs_verification`~~ ~~`confusing_terminology`~~ | deleted | By decision |

**Ollama is a hard requirement for `destructive`, not an optimization.** Without it that field falls back to a verb table that scores 0/15 on held-out phrasings — and its failure direction is "irreversible action proceeds unconfirmed". Every other field degrades gracefully. Install `qwen3-embedding:0.6b` (~640MB); `embedding.NewLocal()` auto-detects and honors `OLLAMA_HOST`, caching probe failures for a minute so a machine without Ollama pays one connection refusal per minute.

`preThinkAction` runs the three embedding-touching classifiers **concurrently** under a 2s budget (`preThinkBudget`). Serially their timeouts would sum to 15s — worse than the LLM they replace. Regex-only verdicts are computed first and stand as the fallback if the budget blows, so a timeout degrades to a weaker answer, never to `false` on `destructive`. `WarmLocalPreThink()` builds the three anchor indexes at daemon start so the first message doesn't pay ~1.5s for them.

**Anchor tuning is measured, never guessed.** `scratchpad/label_prethink.py` labels a corpus with the real pre-think prompt to get ground truth; `thread/prethink_labeled_corpus_test.go` scores each detector against it. **Agreement with the LLM is not correctness** — that file's header documents two cases where the detector is right and the LLM's label is wrong.

### Agent Templates (`agent/`)

Agents are markdown templates in `{workspace}/agents/{name}.md` with `{{PLACEHOLDER}}` syntax. Variables set via `agent.Set(key, value)` before `Build()`. Runtime vars (TOOLS, SKILLS, USER) are set per-turn in `thread/run.go`. `{{DATE}}` and `{{CALENDAR}}` are auto-resolved in `agent.Build()` at day-level granularity (no minutes/seconds).

**Important**: `{{WORKSPACE}}` is resolved in both `agent.Build()` and `use_skill` (`tools/skills.go`). Skills should use `{{WORKSPACE}}/bin/nagobot` for CLI calls.

### Model Resolution (`config/models.go`, `thread/run.go:resolvedModelConfig`)

Each turn resolves its provider+model from `thread.models` in config.yaml — a typed **list** of `ModelRule{Type, Name, Provider, ModelType}` where `Type` is `session | agent | specialty`. Precedence is by type, highest first:

1. **session** — rule whose `Name` == the turn's session key
2. **source-specialty** — if the turn's wake source matches a key in the agent's `source_specialty` frontmatter map, its specialty list is tried left-to-right (against the same `type:specialty` rule pool); applies only when `t.lastWakeSource` matches
3. **agent** — rule whose `Name` == the active agent name
4. **specialty** — the agent's `specialty` array, tried left-to-right; first specialty with a matching rule wins
5. **default** — top-level `thread.provider`/`thread.modelType`

Every step **cascades**: a declared-but-unconfigured entry (e.g. `source_specialty: {heartbeat: [lowcost]}` with no `type:specialty name:lowcost` rule) falls through to the next step, not to the default. So removing the `lowcost` rule drops heartbeat to the agent rule, then basic specialties, then default — graceful degradation, not an error.

`config.FindModelRule(rules, type, name)` does the lookups (linear scan; the list is small). Returns nil → caller falls back to the default model via `DefaultModelFn`.

Agent `specialty` is an **array** (`specialty: [cron, toolcall]`); parsing is lenient (`agent.StringList` accepts a scalar `specialty: pdf` as `[pdf]`, so hand-edited agent files don't silently mis-route). The cron-runner agents (session-summary/memory-summary/tidyup) carry a leading `cron` specialty so a `type:specialty name:cron` rule can route them.

Agent `source_specialty` is a **map** of wake source → specialty list (`source_specialty: {heartbeat: [lowcost]}`); parsed in `agent.AgentDef.SourceSpecialties`, applied in `resolvedModelConfig` via `t.lastWakeSource`. `soul` ships with `heartbeat: [lowcost]` so heartbeat turns (dream / session-reflect) can route to a value model (e.g. `lowcost` → openai-oauth/gpt-5.5, which is free) without changing the user-facing model. Note: `session-stats`'s `resolveModelChain` has no live wake source, so it shows the **non-source** chain only — source-specialty routing is not reflected there.

CLI writers: `set-model --type X --provider P --model M` upserts a `specialty` rule; `set-agent --session S --provider P --model M` upserts a `session` rule (and `--agent A` independently writes meta.json — session→model and session→agent are separate). Bare `set-agent --session S` clears both. `cmd/session_stats.go:resolveModelChain` mirrors this resolution for `nagobot session-stats`.

**Removed (hard switch, v1.5.63)**: the old `thread.models` *map* (specialty→model), the `fixed-to-*` generated agents, and the implicit `provider/model` / bare-model specialty routes. Per-session pins are now `type:session` rules, not `fixed-to-*` agents. Config is NOT auto-migrated — the old map format fails to load (fail-fast); converting config.yaml is a manual deploy step.

### Provider Layer (`provider/`)

Each provider implements `Provider.Chat(ctx, *Request) (ChatResult, error)`. `ChatResult` has a basic variant (`Wait()` only) and a streaming variant (`StreamChatResult` with `Recv()`, `Wait()`, `Cancel()`). Streaming providers emit `StreamDelta` values (text, tool-call-start) through a channel; the Runner pulls deltas via `Recv()` loop and independently decides whether to forward to sink or fire events. This decouples provider streaming from sink delivery — e.g. Gemini streams at the provider level but content is filtered before user delivery (thinking leak protection). Events (emoji reactions) work for all providers regardless of streaming mode.

The `ProviderFactory` creates providers on demand, re-reading config each call. Providers enforce model whitelists. `SanitizeMessages()` removes orphaned tool messages before API calls.

### Tools (`tools/`)

Tools implement `Def() ToolDef` + `Run(ctx, args) string`. Registered in a `Registry`, cloned per-thread. Search and fetch tools use `SearchProvider`/`FetchProvider` interfaces with runtime `Available()` checks.

`dispatch` is the unified routing tool (6 targets: caller:user / caller:session / user / subagent / fork / session). The caller:* forms assert the actual caller kind — mismatches fail validation so the LLM can't silently misroute. `to=session` has two mutually exclusive addressing forms: `session_key` (must exist on disk — typo protection) and `channel`+`user_id` (endpoint form; channel is enum-validated against `endpointChannels`, the derived `channel:user_id` session is created if missing — the deliberate first-contact path; body is a wake message for the target session's AI, never verbatim text to the human). `dispatch({})` with empty sends ends a turn silently. For delayed self-wakes (replacing the old `sleep_thread(duration=...)`), use the `manage-cron` skill to create a one-time `set-at --direct-wake` job into the current session.

### Audio Support

Audio recognition follows the same pattern as vision: `AudioModels` registered per provider, `SupportsAudio()` capability check, `<<media:audio/ogg:path>>` markers, and `audioreader` agent delegation for non-audio models.

- **Channel layer**: Telegram Voice/Audio and Discord audio attachments are downloaded to `{workspace}/media/` (same `downloadMedia()` as images).
- **Tool layer**: `DetectFileType` recognizes `FileTypeAudio` via extension + magic bytes. `handleAudio()` returns media marker if `SupportsAudio`, otherwise guides LLM to delegate to `audioreader`.
- **Provider layer**: OpenRouter sends audio markers as `input_audio` content parts. Gemini uses generic `inlineData`. Non-audio providers skip audio markers.
- **Token estimation**: `EstimateAudioTokens()` uses file size + bitrate heuristic, ~32 tokens/sec.
- **audioreader agent**: `specialty: [audio]`, configured during `onboard` (same flow as imagereader specialty routing).

### Sessions (`session/`)

Conversation history persisted as `{sessionsDir}/{sessionKey}/session.jsonl`. Auto-sanitized on save. Context pressure hooks trigger compression when token budget is exceeded.

## Session vs Thread — Critical Distinction

**Session** = persistent on-disk data (`session.jsonl`, `heartbeat.md`). Survives restarts, lives indefinitely.

**Thread** = transient in-memory execution unit. Created by `Manager.NewThread()`, GC'd after 3h idle. `NewThread()` initializes `lastUserActiveAt = time.Now()` — this is NOT a reliable indicator of when the user was actually last active. For accurate user activity timestamps, always scan `session.jsonl` (via `collectSessions` or `isRealUserSource`), not in-memory thread state.

**Rule**: Any scheduling or timing logic (heartbeat, compression eligibility) that needs `lastUserActiveAt` for sessions that may have been GC'd MUST read from `session.jsonl`, not from `Thread.lastUserActiveAt`. Threads are ephemeral — their state is lost on GC and reset on recreation.

## Heartbeat System (`cmd/heartbeat_scheduler.go`)

The heartbeat runs background maintenance between user interactions. It does NOT message the user proactively — current behavior is limited to two background writes (USER.md via session-reflect, dream.md via dream) plus silent no-op pulses.

### Architecture

A Go goroutine (`heartbeatScheduler`) scans every 30s and fires heartbeat pulses into user sessions. NOT a cron job — the old cron-based dispatcher was removed.

Every pulse runs `use_skill("heartbeat-wake")`, which is a **3-way router** (`cmd/templates/skills/heartbeat-wake/SKILL.md`), not a do-everything skill. The pulse payload carries `pulse_index`, `elapsed_since_user`, `next_pulse`, and (only on dream pulses) `should_dream: true`. Routing, checked in order:

- **`should_dream: true`** → `use_skill("dream")` — review the past 24h, overwrite `dream.md`, then run file-track. The scheduler sets this only at session-local night (02:00–06:00), user quiet long, and ≥4h since the last dream (`shouldDream`, dedup via `dream_log.jsonl`).
- **`pulse_index == 2`** (and not a dream) → `use_skill("session-reflect")` — extract user preferences/corrections/patterns into `USER.md`.
- **anything else** → `dispatch({})`, silent no-op (just the cheap router turn).

All three paths end silently — heartbeat never produces user-facing output. The two context-heavy paths (dream reads 24h, session-reflect reads history) are the source's main cost drivers; dream's input is fresh nightly content, so prompt caching cannot help it.

### Timing

- **Quiet threshold**: 15 min after last user message (`hbQuietMin`)
- **Pulse interval**: 45 min base, +30 min each cycle (`hbPulseInterval`, `hbPulseGrowth`)
- **Activity window**: 48h — stops pulsing if no user activity within 48h (~21 pulses max)
- **Schedule**: `lastActive+15m, +60m, +135m, +240m, ...` (15 min first pulse, then 45/75/105/... growing gaps)

### Critical Implementation Details

**Trigger timeline**: The pulse schedule is derived from `lastActive` (user's last message), NOT from `lastPulse`. `latestDueTrigger(lastActive, now)` returns `(trigger, nextInterval)` by iterating growing intervals: `lastActive+15m, +60m, +135m, +240m, ...`. A pulse fires only when the latest trigger point > `lastPulse`. This means `lastPulse` is purely a dedup guard — it prevents re-firing within the same cycle but never determines when the next pulse should be.

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
- Body compression (large system-sender content → head+tail)
- **Heartbeat turn trim**: marks entire heartbeat turns for removal if `isHeartbeatSkipTurn` returns true:
  - Requires `dispatch({})` was called (tool result contains `turn-terminated-silent` outcome)
  - Turns that delivered via dispatch (to=user / to=caller:user / to=caller:session / to=session) are PRESERVED
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
- **Default cron seeds**: `tidyup` (4am daily), `session-summary` (midnight daily), `memory-summary` (midnight daily), `world-knowledge` (midnight daily). Heartbeat is NOT a cron job.
- **Prompt caching requires deterministic serialization**: All LLM providers use prefix-based prompt caching (tools → system → messages). Go map iteration is non-deterministic, so any map-derived output that ends up in the LLM request MUST be sorted. Currently sorted: `tools.Registry.Defs()`, `skills.Registry.List()`, `skills.Registry.SkillNames()`, `agent.buildSessionsSummary()`. When adding new map-iterated content to the system prompt or tools array, always sort the output.
- **Cache monitoring**: `provider.Usage.CachedTokens` flows through `Runner.totalUsage` → `monitor.TurnRecord` → `nagobot monitor --metrics` (per-provider `cacheHitRate`). All providers fill this field from their respective API response (OpenRouter/Moonshot/Zhipu/Minimax/xAI/SiliconFlow: `PromptTokensDetails.CachedTokens`; DeepSeek: `PromptCacheHitTokens`; Anthropic: `CacheReadInputTokens`; OpenAI: `InputTokensDetails.CachedTokens`; Gemini: `CachedContentTokenCount`).

## Common Pitfalls

- **Don't trust `Thread.lastUserActiveAt` for scheduling**: It's initialized to `time.Now()` on thread creation, not actual user activity. Use `collectSessions()` → `LastUserActiveAt` from `session.jsonl` scan.
- **Don't use `logger.Debug` for things you need to see**: Heartbeat scheduler activity, error conditions — use `Info` or `Warn`. Debug is invisible at default log level.
- **Heartbeat state is persisted**: `lastPulse` is saved to `heartbeat-state.json` after each pulse. Restarts reload this state — no cold-start special-casing needed.
- **`collectSessions` loads full session data**: Every call parses entire `session.jsonl` for all matching sessions. Don't call it in tight loops. The scheduler calls it every 30s — acceptable for small deployments.
- **`{{WORKSPACE}}` resolves in both agents and skills**: `agent.Build()` and `use_skill` (`tools/skills.go`) both replace `{{WORKSPACE}}`. Skills should use `{{WORKSPACE}}/bin/nagobot` for CLI calls.
- **Heartbeat turns suppress via LLM, not code**: The old `WakeHeartbeatReflect` had code-level `SetSuppressSink()`. Now both reflect and act use `WakeHeartbeat` — suppression relies on the LLM calling `dispatch({})`. If the LLM forgets, output leaks to the user.
- **`applyDefaults()` only adds, never prunes**: If a cron seed is removed from `defaultCronSeeds()`, old entries in `config.yaml` persist. Manual cleanup may be needed after upgrades.

## Deployment

Install: `curl -fsSL https://nagobot.com/install.sh | bash` (all platforms). Update: `nagobot update`.

Service managed via launchd (macOS), systemd (Linux), or Task Scheduler (Windows). Logs at `~/.nagobot/logs/`.

Release pipeline: push `v*` tag → GitHub Actions builds all platform binaries (linux/darwin/windows) → publishes to GitHub Releases.
