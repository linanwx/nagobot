---
name: thread-ops
description: Use when you need to interact with other threads or manage thread lifecycle. Covers dispatching work to subagents / forks / existing sessions via a single primitive, inspecting session state by key, scheduling a delayed self-wake (via the manage-cron set-at --direct-wake job), and running health diagnostics across all active threads.
---
# Thread Operations

Threads are execution units that bind a session to an agent. Each thread has an inbox (wake message queue), runs one turn at a time, and delivers output to a sink (Telegram, Discord, etc.).

## Tools Reference

### dispatch

The single routing primitive for reaching other threads and sessions. It is asynchronous: every send goes out, the tool returns immediately, and your turn continues. It never waits for the target. Each entry in `sends` has a `to` field selecting the target:

- **`caller:session`** — reply to the caller AND assert the caller is another session (cross-session wake; `caller_session_key` is present in the wake YAML). Fields: `body`.
- **`subagent`** — spawn a new subagent thread, or wake the existing one at the same `task_id`. Fields: `body` + `params`: `agent` (optional — falls back to session default), `task_id` (required, `[a-z0-9_-]+`).
- **`subagent_fork`** — a `subagent` variant: branch the current session as a new agent thread with stripped history inherited, or wake the existing one at the same `task_id`. Fields: `body` + `params`: `agent` (optional), `task_id`.
- **`session`** — wake another session's AI. The body becomes that session's wake message, processed by **its** AI (own agent / persona / history) — it is NOT delivered verbatim to that session's human user; the target AI decides what, if anything, to say to its own human (by writing its reply text). Addressing lives in `params`, two mutually exclusive forms: `params.session_key` (exact key — the session must already exist) or `params.channel` + `params.user_id` (channel endpoint — the session is **created if missing**; use this to initiate contact with a user who may never have talked to the bot, e.g. `params: {channel: "wecom", user_id: "ZhaoJing"}`; groups follow the channel's own convention, e.g. `user_id: "group:<chatid>"`). Either way the target's `dispatch(to=caller:session)` routes back to **your** session (not the target's channel user). The exchange recurses until one side stops replying.

### Replying to whoever woke you: plain text vs `caller:session`

There is no `to=user` target. Speaking to your own human is not a dispatch at
all — you just write your reply and end the turn. Read `caller_session_key` in
the wake YAML:
- **Present** → caller is another session → use `caller:session`.
- **Absent** AND this session is user-facing → caller is the channel user → just write your reply, no dispatch.
- System sources (cron / heartbeat / compression) have no caller to reply to. On a user-facing session, cron turns still reach your human via plain text; heartbeat and compression turns reach nobody no matter what you write. Use `dispatch({})` to end silently.

The `caller:session` kind assertion is validated: asserting it when the caller is actually the channel user (or a system source) is a cheap validation error (turn continues; fix and re-call), not a silent misroute. The tool result on success reports `delivered_to` so you can confirm who received the reply.

**After dispatching, tell your human what you did.** The turn continues past the dispatch, so write a short reply ("I've started a researcher on X") and let the turn end. Do not wait for the child or poll it.

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
  {"to": "subagent_fork", "params": {"agent": "analyst", "task_id": "hypo-a"}, "body": "Explore hypothesis A from current discussion"},
  {"to": "session", "params": {"session_key": "telegram:12345"}, "body": "Ping: report is ready"}
])
```

Empty `sends`, i.e. `dispatch({})`, silently terminates the turn with no delivery (history still recorded). It is the only form that ends a turn, and only when it is the sole tool call in the message; batched with other tools it is a no-op.

Any other dispatch leaves the turn running, whether it succeeded or failed validation (fix and re-call on a validation error). Generated child session keys follow `{current}:threads:{task_id}` for `subagent` and `{current}:fork:{task_id}` for `subagent_fork` — note the latter's infix is `:fork:`, not `:subagent_fork:`. Re-using a task_id from a prior turn wakes the existing session (noted `resumed` in the result); dispatching to a missing-agent or unknown session_key is a validation error. The `params.channel`+`params.user_id` endpoint form instead creates the missing session (noted `created` in the result) — that is the deliberate path for first contact.

**When to use which `to`:**
- Parallel subtasks or delegating to a specialized agent (e.g. `imagereader`, `audioreader`): **subagent**.
- When the child must reason about the current conversation itself (scheduling, reflection, summarization): **subagent_fork**.
- Cross-session notifications ("notify user in telegram:12345"): **session** with `params.session_key`.
- Proactively contacting a channel user who may have no session yet (e.g. a cron job messaging an employee for the first time): **session** with `params.channel` + `params.user_id`.
- Replying to the current user: plain reply text. Replying to a cross-session caller: **caller:session**.
- Delivering a result as a dispatched child: plain reply text; the dispatching session is notified when your turn ends and reads it.

### check_session

Inspect a session by key. Reports disk state (message count / file size / mtime / agent from meta) plus in-memory thread state when a thread is loaded. Three states are possible:

- `exists=false, thread_active=false` → session never existed or file was removed.
- `exists=true, thread_active=false` → session persisted on disk, no thread currently loaded (will be created on next wake).
- `thread_active=true` → thread is in memory; fields include `thread_state` (`running` / `pending` / `idle`), `thread_iterations`, `thread_current_tool`, `thread_elapsed_sec`.

```
tool_call: check_session(session_key="cli:threads:find-news")
```

You do not need it to learn that a child finished: that arrives on its own as a `progress` wake marked `event: turn_ended`. Do not poll with it. Use it to diagnose a child that seems stuck, or to check whether a `task_id` already exists.

### Stopping a child session (soft stop)

Run via the CLI (not a tool) when a child you spawned is running too long or down a wrong path and you want it to wind down:

```
bin/nagobot stop-session <child-session-key>
```

e.g. `bin/nagobot stop-session cli:threads:find-news`.

This is a **soft** stop. It injects a control message into the child's dedicated inject lane; at the child turn's **next iteration boundary** its LLM is asked to end the turn immediately via `dispatch({})`. Notes:

- **Not instantaneous.** An in-flight tool or LLM call runs to completion first; the stop lands at the next boundary. A child blocked in one very long tool call will not stop until that call returns.
- **No hard cancel.** The child is never killed mid-write; its session history stays valid and will not be wrongly resumed after a restart.
- **End notice still arrives.** The stopped child's turn ends silently with `dispatch({})`, and like any child turn end that produces a `progress` `event: turn_ended` notice to you. Read it as "stopped", not as a result.
- **Errors if not running.** If no thread is loaded for the key (already finished / GC'd / never existed), the command reports that — there is nothing to stop.

To stop a child you spawned, use its resolved key: `<current>:threads:<task_id>` (subagent) or `<current>:fork:<task_id>` (subagent_fork).

### Handling a `source: progress` wake

A `progress` wake comes in two kinds; tell them apart by the body.

**Running progress** (body starts with `⏳`, no `event: turn_ended`): a thread under you has been running a long turn (at least 1 min) and a background scanner summarized what it is doing, about once a minute. This is read-only telemetry, harvested without touching the child, and NOT its result. The child keeps running regardless. End the turn with one of:

- plain reply text: surface a brief progress note to the user if it's worth sharing ("still researching X, found Y so far").
- `dispatch({})`: ignore it silently (the most common choice; these turns are auto-trimmed from your context later).
- If it looks like the child is going wrong (looping, off-track), ask the user whether to stop it, or run `bin/nagobot stop-session <child-session-key>` (see "Stopping a child session" above).

**Turn ended** (body carries `event: turn_ended`, `child_session`, `session_file`): a child you dispatched just finished a turn. This is an event, not a verdict: it says the turn ended, not that the task is done or done right. Check the actual state:

1. `read_file` the `session_file` and find the last `role=assistant` entry: that is the child's output.
2. Then one of three:
   - the child is still waiting on threads it dispatched itself: keep waiting (`dispatch({})` or a one-line status to your human); another notice comes when its next turn ends.
   - the child did not do it well: `dispatch(to=subagent|subagent_fork)` with the **same `task_id`**, saying what to fix.
   - the result is complete: deliver it to your human in plain text, or continue your work with it.

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
2. The turn continues: tell your human "I've started a researcher on X" and let the turn end. The child runs asynchronously.
3. When the child's turn ends, you are woken with `source: progress`, `event: turn_ended`, and its `session_file`.
4. read_file(session_file), take the last assistant entry, then wait / send it back with task_id "find-x" / deliver the result.
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
→ Two independent children spawn; each sends you its own `turn_ended` notice when its turn ends.
```
