---
name: thread-ops
description: Use when you need to interact with other threads or manage thread lifecycle. Covers dispatching work to subagents / forks / existing sessions via a single primitive, inspecting session state by key, scheduling a delayed self-wake (via the manage-cron set-at --direct-wake job), and running health diagnostics across all active threads.
---
# Thread Operations

Threads are execution units that bind a session to an agent. Each thread has an inbox (wake message queue), runs one turn at a time, and delivers output to a sink (Telegram, Discord, etc.).

## Tools Reference

### dispatch

The single turn-terminating routing primitive. Call it at the end of a turn to declare where your output goes. Each entry in `sends` has a `to` field selecting the target:

- **`caller:session`** — reply to the caller AND assert the caller is another session (cross-session wake; `caller_session_key` is present in the wake YAML). Fields: `body`.
- **`subagent`** — spawn a new subagent thread, or wake the existing one at the same `task_id`. Fields: `body` + `params`: `agent` (optional — falls back to session default), `task_id` (required, `[a-z0-9_-]+`).
- **`fork`** — branch the current session as a new agent thread with stripped history inherited, or wake the existing one at the same `task_id`. Fields: `body` + `params`: `agent` (optional), `task_id`.
- **`session`** — wake another session's AI. The body becomes that session's wake message, processed by **its** AI (own agent / persona / history) — it is NOT delivered verbatim to that session's human user; the target AI decides what, if anything, to say to its own human (by writing its reply text). Addressing lives in `params`, two mutually exclusive forms: `params.session_key` (exact key — the session must already exist) or `params.channel` + `params.user_id` (channel endpoint — the session is **created if missing**; use this to initiate contact with a user who may never have talked to the bot, e.g. `params: {channel: "wecom", user_id: "ZhaoJing"}`; groups follow the channel's own convention, e.g. `user_id: "group:<chatid>"`). Either way the target's `dispatch(to=caller:session)` routes back to **your** session (not the target's channel user). The exchange recurses until one side halts.

### Replying to whoever woke you: plain text vs `caller:session`

There is no `to=user` target. Speaking to your own human is not a dispatch at
all — you just write your reply and end the turn. Read `caller_session_key` in
the wake YAML:
- **Present** → caller is another session → use `caller:session`.
- **Absent** AND this session is user-facing → caller is the channel user → just write your reply, no dispatch.
- System sources (cron / heartbeat / compression) have no caller to reply to. On a user-facing session, cron turns still reach your human via plain text; heartbeat and compression turns reach nobody no matter what you write. Use `dispatch({})` to end silently.

The `caller:session` kind assertion is validated: asserting it when the caller is actually the channel user (or a system source) is a cheap validation error (turn continues; fix and re-call), not a silent misroute. The tool result on success reports `delivered_to` so you can confirm who received the reply.

**A user-woken turn may not end on a bare dispatch.** When the caller is the channel user and your dispatch is the sole tool call, the same message must also carry your reply text — otherwise the turn closes having said nothing to the person who asked, while the work went to a subagent or a peer. The tool rejects that call (no send executes, the turn continues); add the text and re-issue. `dispatch({})` with no sends stays exempt: choosing to say nothing is different from forgetting to.

### Caller is per-wake

Every turn is triggered by a wake; every wake carries a caller identity. The same session can be woken by the user in one turn, by a cron job in the next, and by a subagent in the one after. `dispatch(to=caller:*)` always replies to **the caller of the current turn** — never a fixed identity. Read the wake YAML header each turn to see who woke you; don't assume the caller is the same as last turn.

### Mis-routed wakes — don't silently drop

If you receive a cross-session wake (WakeSession) that you believe was sent to the wrong recipient, DO NOT call `dispatch({})` — that silently drops the message and the caller never learns. Instead `dispatch(to=caller:session)` with an explanation so they can redirect to the correct session.

### Callerless wakes (cron / compression / heartbeat)

These wakes have no caller to reply to, so `caller:session` fails validation.
What your plain text does depends on the source: a **cron** turn on a
user-facing session delivers it to that human, while **heartbeat** and
**compression** turns deliver nothing anywhere — they are maintenance, and no
phrasing makes them reach the user. End with `dispatch({})` to be explicitly
silent, or `dispatch(to=session, params={session_key: ...})` to reach a
different session.

```
tool_call: dispatch(sends=[
  {"to": "caller:session", "body": "I'll look into this and get back to you."},
  {"to": "subagent", "params": {"agent": "search", "task_id": "find-news"}, "body": "Search for recent news about X"},
  {"to": "fork", "params": {"agent": "analyst", "task_id": "hypo-a"}, "body": "Explore hypothesis A from current discussion"},
  {"to": "session", "params": {"session_key": "telegram:12345"}, "body": "Ping: report is ready"}
])
```

Empty `sends` — `dispatch({})` — silently terminates the turn with no delivery (history still recorded).

On successful dispatch the turn ends; on validation error the turn continues so you can re-call. Subagent / fork generated session keys follow `{current}:threads:{task_id}` and `{current}:fork:{task_id}`. Re-using a task_id from a prior turn wakes the existing session (noted `resumed` in the result); dispatching to a missing-agent or unknown session_key is a validation error. The `params.channel`+`params.user_id` endpoint form instead creates the missing session (noted `created` in the result) — that is the deliberate path for first contact.

**When to use which `to`:**
- Parallel subtasks or delegating to a specialized agent (e.g. `imagereader`, `audioreader`): **subagent**.
- When the child must reason about the current conversation itself (scheduling, reflection, summarization): **fork**.
- Cross-session notifications ("notify user in telegram:12345"): **session** with `params.session_key`.
- Proactively contacting a channel user who may have no session yet (e.g. a cron job messaging an employee for the first time): **session** with `params.channel` + `params.user_id`.
- Replying to the current user / parent: **user** (channel-user wake) or **caller:session** (cross-session wake).

### check_session

Inspect a session by key. Reports disk state (message count / file size / mtime / agent from meta) plus in-memory thread state when a thread is loaded. Three states are possible:

- `exists=false, thread_active=false` → session never existed or file was removed.
- `exists=true, thread_active=false` → session persisted on disk, no thread currently loaded (will be created on next wake).
- `thread_active=true` → thread is in memory; fields include `thread_state` (`running` / `pending` / `idle`), `thread_iterations`, `thread_current_tool`, `thread_elapsed_sec`.

```
tool_call: check_session(session_key="cli:threads:find-news")
```

Use this after `dispatch(to=subagent|fork)` to follow up on a child session by the resolved `session_key`.

### Stopping a child session (soft stop)

Run via the CLI (not a tool) when a child you spawned is running too long or down a wrong path and you want it to wind down:

```
bin/nagobot stop-session <child-session-key>
```

e.g. `bin/nagobot stop-session cli:threads:find-news`.

This is a **soft** stop. It injects a control message into the child's dedicated inject lane; at the child turn's **next iteration boundary** its LLM is asked to end the turn immediately via `dispatch({})`. Notes:

- **Not instantaneous.** An in-flight tool or LLM call runs to completion first; the stop lands at the next boundary. A child blocked in one very long tool call will not stop until that call returns.
- **No hard cancel.** The child is never killed mid-write; its session history stays valid and will not be wrongly resumed after a restart.
- **Silent end.** The stopped child terminates with `dispatch({})` — it does NOT send you a `child_completed`. Confirm it stopped with `check_session(session_key=...)` (expect the thread to no longer be `running`).
- **Errors if not running.** If no thread is loaded for the key (already finished / GC'd / never existed), the command reports that — there is nothing to stop.

To stop a child you spawned, use its resolved key: `<current>:threads:<task_id>` (subagent) or `<current>:fork:<task_id>` (fork).

### Handling a `source: progress` wake

While a subagent/fork you spawned runs long (≥1 min), a background scanner wakes you about once a minute with `source: progress` — a mechanical snapshot of that child (elapsed time, step count, last few tool calls, current tool). The child key is in the body.

This is **NOT** a `child_completed` and **NOT** the child's result — it is read-only telemetry, harvested without touching the child. The child keeps running regardless of what you do. End the turn with one of:

- plain reply text — surface a brief progress note to the user if it's worth sharing ("still researching X, found Y so far"). No dispatch needed.
- `dispatch({})` — ignore it silently (the most common choice; these turns are auto-trimmed from your context later).
- If it looks like the child is going wrong (looping, off-track), ask the user whether to stop it, or run `bin/nagobot stop-session <child-session-key>` (see "Stopping a child session" above) to halt it.

Do not treat the snapshot's tool list as the child's answer — wait for the actual `child_completed` for that.

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
1. dispatch(sends=[{to: "subagent", params: {agent: "researcher", task_id: "find-x"}, body: "Find information about X"}])
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
dispatch(sends=[{to: "session", params: {session_key: "telegram:12345"}, body: "Notify the user that the report is ready"}])
```

### Parallel fan-out, independent task bodies
```
dispatch(sends=[
  {to: "subagent", params: {agent: "search", task_id: "news-a"}, body: "Topic A"},
  {to: "subagent", params: {agent: "search", task_id: "news-b"}, body: "Topic B"}
])
→ Two independent children spawn; each wakes you with child_completed when done.
```
