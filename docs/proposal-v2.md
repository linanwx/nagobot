# Nagobot V2 Proposal: Self-Healing, Proactive Orchestration, and Durability

## Background

Nagobot currently operates as a reactive system: channels push messages, threads process them, responses flow back. This works well for simple Q&A but falls short for sustained agentic workflows. Three gaps have been identified:

1. **Message durability** — User messages are only persisted after the full turn completes. A crash mid-turn loses the message.
2. **Self-healing** — Errors propagate directly to users as `[Error]`. No retries, no failover, no recovery.
3. **Proactivity** — The system is purely reactive. Threads never wake themselves; no periodic review of work.

## Proposal Overview

```
Phase 1: Hardening (self-healing + durability)
Phase 2: Per-agent model assignment
Phase 3: Proactive orchestrator
```

---

## Phase 1: Hardening

### 1.1 Goroutine Panic Recovery (Critical)

**Problem:** A panic in `RunOnce` permanently kills a thread (stuck in `threadRunning`, semaphore leaked).

**Fix:** Wrap goroutine in `defer/recover` in `manager.go:scheduleReady`.

**Effort:** ~10 lines.

### 1.2 Write-Ahead User Message (Small)

**Problem:** User messages exist only in memory until end-of-turn. Crash during LLM call or tool loop = message lost.

**Fix:** In `run()`, save user message(s) to session immediately after construction (before calling LLM). Adjust end-of-turn save to not double-write.

**Effort:** ~15 lines in `thread/run.go`.

### 1.3 Classified Provider Error Retry (Medium)

**Problem:** All provider errors treated identically — rate limit, auth failure, context overflow all abort the turn.

**Fix:**
- Define `RetryableError` interface on provider errors
- Classify: 429/5xx = retryable; 400/401/403 = non-retryable; context overflow = special
- Retry retryable errors with exponential backoff (base 1s, max 30s, 3 attempts)
- Context overflow triggers auto-truncation (drop oldest non-system messages, retry)

**Effort:** ~100 lines across `provider/` (error types) and `thread/runner.go`.

### 1.4 Max Tool Call Guard (Small)

**Problem:** `runner.go` `for {}` loop has no iteration limit. Model stuck in tool-call loop = infinite execution.

**Fix:** Add `maxToolRounds = 50` counter. Exceed = abort with error.

**Effort:** ~5 lines in `thread/runner.go`.

### 1.5 Sink Delivery Retry (Small)

**Problem:** Failed response delivery (Telegram down, network blip) = response permanently lost.

**Fix:** Retry up to 3 times with 1s/2s/4s backoff. Log to dead-letter file on exhaustion.

**Effort:** ~20 lines in `thread/wake.go`.

### Phase 1 Summary

| Item | Effort | Impact |
|------|--------|--------|
| Panic recovery | Small | Prevents permanent thread death |
| Write-ahead user message | Small | Crash-safe message persistence |
| Classified retry + backoff | Medium | Resilience to transient failures |
| Max tool call guard | Small | Prevents runaway loops |
| Sink delivery retry | Small | Response delivery durability |

---

## Phase 2: Per-Agent Model Assignment

### Problem

Every thread uses the single global `thread.provider` / `thread.modelType`. Users cannot assign different models to different agents or sessions.

### Design

**Three-tier provider resolution** (highest priority first):

1. `WakeMessage.ProviderName` / `WakeMessage.ModelName` — runtime override per wake
2. Agent template front matter `provider` / `model` — agent-level default
3. Global config `thread.provider` / `thread.modelType` — system fallback

### Changes

1. **`agent/template_meta.go`** — Add `Provider` and `Model` fields to front matter:
   ```yaml
   ---
   name: researcher
   description: Deep research agent
   provider: deepseek
   model: deepseek-reasoner
   ---
   ```

2. **`thread/types.go`** — Store `*provider.Factory` in ThreadConfig (not just a single Provider).

3. **`thread/run.go`** — Resolve provider per-turn: check active agent metadata → fallback to default.

4. **`thread/wake.go`** — Add `ProviderName`/`ModelName` to `WakeMessage` for runtime override.

### Use Cases

- `soul.md` uses `deepseek` for daily conversations (cheap, reliable tool calling)
- `researcher.md` uses `anthropic` for deep analysis tasks (expensive but high quality)
- Orchestrator can switch a stuck thread to a more capable model via WakeMessage override
- Cron jobs can specify model per job

### Effort

Medium. ~50-80 lines across 4-5 files. No new dependencies.

---

## Phase 3: Proactive Orchestrator

### Vision

A main "orchestrator" model periodically wakes up, scans all threads, and decides next steps: continue stalled work, switch models for struggling sessions, clean up dead threads, or spawn new tasks proactively.

### Phased Approach

#### Phase 3a: Cron-Based Orchestrator (Proves the Concept)

Use the existing cron system. Zero new infrastructure.

**Implementation:**
1. Create `agents/orchestrator.md` — agent prompt with instructions to scan and decide
2. Add cron entry: fire every 15 minutes, target session `orchestrator:main`
3. Enrich `health` tool with: `lastActiveAt`, session message count, last error info
4. Orchestrator uses existing `wake_thread` tool to act on decisions

**Safeguards:**
- Cooldown per thread (don't re-wake within 10 minutes)
- Max 3 wakes per sweep
- Source tag `"orchestrator"` for tracing
- Skip threads that are running or have pending messages

**Limitations:** Each sweep costs an LLM call even if nothing needs attention.

#### Phase 3b: Hybrid Rule-Based + LLM Escalation (If 3a proves valuable)

Extract orchestrator into a dedicated goroutine. Most sweeps are free (rule-based); LLM called only for ambiguous cases.

**Rule-based triggers (no LLM needed):**
- Thread idle > X minutes with unfinished task marker → wake with continuation
- Thread error count > threshold → switch to more capable model (Phase 2)
- Session approaching context limit → trigger compression
- Session file stale > 3 days → archive

**LLM-escalation triggers:**
- Should this task be broken into subtasks?
- Is this thread stuck or waiting for user input?
- Which model is best for this thread's current task?

**New signals to track:**
- `lastActiveAt` (already exists)
- `lastErrorAt` + `errorCount` (needs adding to Thread)
- Session task markers (presence of "TASK" var, completion indicators)

### Orchestrator Decision Model

```
Collect signals (cheap: file stats, thread states)
    ↓
Apply rules (free: code logic)
    ↓
[If ambiguous] → LLM decision (expensive: one API call)
    ↓
Execute actions (wake threads, switch models, compress, archive)
```

### Infinite Loop Prevention

1. **Per-thread cooldown** — Orchestrator tracks last wake time per thread, refuses re-wake within cooldown
2. **Source tagging** — Threads can check `msg.Source == "orchestrator"` and limit recursive spawning
3. **Wake budget** — Max N wakes per sweep cycle
4. **Idempotency** — Skip threads that are already running or have pending messages

### Effort

- Phase 3a (cron): Small. ~1 agent template + 1 cron entry + tool enrichment
- Phase 3b (hybrid goroutine): Medium-Large. ~200-300 lines for the orchestrator + signal collection

---

## Implementation Priority

```
[Week 1] Phase 1.1 Panic recovery          — Critical safety
[Week 1] Phase 1.4 Max tool call guard      — Critical safety
[Week 1] Phase 1.2 Write-ahead message      — Durability
[Week 2] Phase 1.3 Classified retry         — Reliability
[Week 2] Phase 1.5 Sink delivery retry      — Reliability
[Week 3] Phase 2   Per-agent model          — Flexibility
[Week 4] Phase 3a  Cron orchestrator        — Proactivity (MVP)
[Future] Phase 3b  Hybrid orchestrator      — Proactivity (full)
```

## Architecture After V2

```
                    ┌─────────────────────┐
                    │    Orchestrator      │
                    │  (cron/goroutine)    │
                    │   scan → decide     │
                    │     → wake          │
                    └────────┬────────────┘
                             │ wake_thread
     ┌───────────┬───────────┼───────────┬───────────┐
     │           │           │           │           │
 ┌───┴───┐  ┌───┴───┐  ┌───┴───┐  ┌───┴───┐  ┌───┴───┐
 │Thread  │  │Thread  │  │Thread  │  │Thread  │  │Thread  │
 │deepseek│  │claude  │  │gpt-5.2│  │deepseek│  │claude  │
 │soul    │  │research│  │soul    │  │general │  │soul    │
 └───┬────┘  └───┬────┘  └───┬────┘  └───┬────┘  └───┬────┘
     │           │           │           │           │
     ▼           ▼           ▼           ▼           ▼
  Telegram     Telegram     Web        (child)     Cron
  user:123     user:456     user:789
```

Each thread resolves its own provider per-turn. The orchestrator scans all threads and proactively manages the system.

## Not In Scope

- **Streaming responses** — Separate concern, not related to self-healing/proactivity
- **Multi-provider load balancing** — Over-engineering for current scale
- **Persistent message queue (Redis/NATS)** — File-based WAL is sufficient for single-node
- **Distributed thread management** — Single-process assumption maintained
