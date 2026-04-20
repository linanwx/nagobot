---
name: thread-ops
description: Use when you need to interact with other threads or manage thread lifecycle. Covers dispatching work to subagents / forks / existing sessions via a single primitive, inspecting session state by key, sleeping to suppress output or schedule delayed follow-ups, and running health diagnostics across all active threads.
tags: [thread, internal, orchestration]
---
# Thread Operations

Threads are execution units that bind a session to an agent. Each thread has an inbox (wake message queue), runs one turn at a time, and delivers output to a sink (Telegram, Discord, etc.).

## Tools Reference

### dispatch

The single turn-terminating routing primitive. Call it at the end of a turn to declare where your output goes. Each entry in `sends` has a `to` field selecting the target:

- **`caller`** — reply to whoever woke this turn. For real user-message turns this is the user; for cross-session wakes (someone called `dispatch(to=session)` at you) this is the originating session, **not** your channel user. Fields: `body`.
- **`user`** — reply to your channel user via your session's user-channel sink. Only valid for user-facing sessions (`telegram:*` / `discord:*` / `cli` / `web` / `feishu:*` / `wecom:*`). Distinct from `caller`: useful when a non-user source (cron, heartbeat, another session) woke you and you want to proactively message your user instead of replying to the waker. Fields: `body`.
- **`subagent`** — spawn a new subagent thread, or wake the existing one at the same `task_id`. Fields: `agent` (optional — falls back to session default), `task_id` (required, `[a-z0-9_-]+`), `body`.
- **`fork`** — branch the current session as a new agent thread with stripped history inherited, or wake the existing one at the same `task_id`. Fields: `agent` (optional), `task_id`, `body`.
- **`session`** — wake an existing session by key. Fields: `session_key`, `body`. The target receives the body and its own `dispatch(to=caller)` routes back to **your** session (not the target's channel user).

```
tool_call: dispatch(sends=[
  {"to": "caller", "body": "I'll look into this and get back to you."},
  {"to": "subagent", "agent": "search", "task_id": "find-news", "body": "Search for recent news about X"},
  {"to": "fork", "agent": "analyst", "task_id": "hypo-a", "body": "Explore hypothesis A from current discussion"},
  {"to": "session", "session_key": "telegram:12345", "body": "Ping: report is ready"}
])
```

Empty `sends` — `dispatch({})` — silently terminates the turn with no delivery (history still recorded).

On successful dispatch the turn ends; on validation error the turn continues so you can re-call. Subagent / fork generated session keys follow `{current}:threads:{task_id}` and `{current}:fork:{task_id}`. Re-using a task_id from a prior turn wakes the existing session (noted `resumed` in the result); dispatching to a missing-agent or unknown session_key is a validation error.

**When to use which `to`:**
- Parallel subtasks or delegating to a specialized agent (e.g. `imagereader`, `audioreader`, `pdfreader`): **subagent**.
- When the child must reason about the current conversation itself (scheduling, reflection, summarization): **fork**.
- Cross-session notifications ("notify user in telegram:12345"): **session**.
- Just replying to the current user / parent: **caller**.

### check_session

Inspect a session by key. Reports disk state (message count / file size / mtime / agent from meta) plus in-memory thread state when a thread is loaded. Three states are possible:

- `exists=false, thread_active=false` → session never existed or file was removed.
- `exists=true, thread_active=false` → session persisted on disk, no thread currently loaded (will be created on next wake).
- `thread_active=true` → thread is in memory; fields include `thread_state` (`running` / `pending` / `idle`), `thread_iterations`, `thread_current_tool`, `thread_elapsed_sec`.

```
tool_call: check_session(session_key="cli:threads:find-news")
```

Use this after `dispatch(to=subagent|fork)` to follow up on a child session by the resolved `session_key`.

### health

List all active threads and system status.

```
tool_call: health()
```

- Returns `all_threads`: list of every active thread with ID, session key, agent, state, pending count, last activity.
- Also returns provider info, session stats, cron jobs, channel config, memory usage.

## Common Patterns

### Delegate to a subagent and follow up by key
```
1. dispatch(sends=[{to: "subagent", agent: "researcher", task_id: "find-x", body: "Find information about X"}])
2. Your turn ends. The child runs asynchronously.
3. When the child completes, it wakes you with `child_completed` automatically.
4. Optionally check_session(session_key="<current>:threads:find-x") to probe state.
```

### Silent end
```
dispatch({})
→ No delivery. Turn ends silently with history recorded.
```

### Ignore irrelevant message
```
dispatch({})   # silent termination — history recorded, no delivery
```

### Scheduled self check-in later
Use `manage-cron` skill to create a one-time job that wakes this session:
```
bin/nagobot cron set-at --id self-checkin-<uniq> --at <RFC3339> \
    --task "Check if user responded" --wake-session <current-session> --direct-wake
```

### Cross-session notification
```
dispatch(sends=[{to: "session", session_key: "telegram:12345", body: "Notify the user that the report is ready"}])
```

### Parallel fan-out, independent task bodies
```
dispatch(sends=[
  {to: "subagent", agent: "search", task_id: "news-a", body: "Topic A"},
  {to: "subagent", agent: "search", task_id: "news-b", body: "Topic B"}
])
→ Two independent children spawn; each wakes you with child_completed when done.
```
