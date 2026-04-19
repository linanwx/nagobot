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

- **`caller`** — reply to whoever woke this thread (same as the default sink). Fields: `body`.
- **`subagent`** — spawn a new subagent thread, or wake the existing one at the same `task_id`. Fields: `agent` (optional — falls back to session default), `task_id` (required, `[a-z0-9_-]+`), `body`.
- **`fork`** — branch the current session as a new agent thread with stripped history inherited, or wake the existing one at the same `task_id`. Fields: `agent` (optional), `task_id`, `body`.
- **`session`** — wake an existing session by key. Fields: `session_key`, `body`.

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

### sleep_thread

Suppress output and optionally schedule a delayed wake-up. Still available for duration-based scheduling; for pure silent termination prefer `dispatch({})`.

```
tool_call: sleep_thread(duration="30m", message="Check back on user", skip=false)
```

- `duration` (optional): Go duration format — `"30m"`, `"2h"`, `"1h30m"`. Default `"2m"`, max `24h`.
- `message` (optional): Memo included when waking up. Default: "Sleep timer expired."
- `skip` (optional): `true` = suppress output only, no scheduled wake. Use when the message is not directed at you.

**Note:** When called during a heartbeat turn (source: `heartbeat`), `sleep_thread` ignores all parameters and acts as a simple terminate+suppress. The heartbeat scheduler handles the next pulse automatically.

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

### Ignore irrelevant message (with no wake scheduled)
```
sleep_thread(skip=true)
```

### Scheduled check-in later
```
sleep_thread(duration="1h", message="Check if user responded")
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
