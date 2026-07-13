---
name: dispatch-guide
description: Use when about to call the `dispatch` tool and unsure which `to` form to pick, when a dispatch validation error reports a mismatch between asserted caller kind and actual caller, or when handling a `child_completed` wake from a previously dispatched subagent/fork. Focused decision tree for routing replies (caller:user / caller:session / user / session), spawning (subagent / fork), receiving child results, and silent termination.
---

# Dispatch Routing Guide

`dispatch` is the only turn-terminating routing primitive. Every turn ends with either an explicit `dispatch(...)` call or implicit default-sink delivery. This skill is the decision tree for picking the right `to` value.

## The 30-second decision

Look at the wake YAML frontmatter of the current turn. Three fields determine the answer:

1. **`caller_session_key`** — present? caller is another session.
2. **`source`** — `cron` / `heartbeat*` / `compression`? caller is system (drop sink).
3. **session key prefix** — `telegram:` / `discord:` / `cli` / `web` / `feishu:` / `wecom:`? this session is user-facing.

Possible `source` values you may see in the wake YAML:

| source | meaning | caller kind |
|---|---|---|
| `telegram` / `discord` / `cli` / `web` / `feishu` / `wecom` | channel user message | user |
| `WakeSession` | another session woke you (cross-session) | session |
| `child_completed` | a subagent/fork finished and is reporting back | session |
| `cron` | scheduled cron job fired (may be `--direct-wake` self-wake) | system (drop sink) |
| `heartbeat` (also `heartbeat_wake` / `heartbeat_reflect` in older sessions) | heartbeat scheduler pulse | system (drop sink) |
| `compression` | context compression wake | system (drop sink) |
| `resume` | internal re-processing | system (drop sink) |

Then:

| caller kind | this session is user-facing | reply to caller | proactive to user |
|---|---|---|---|
| user (channel wake) | yes | `caller:user` | (same as caller:user, but `user` also works) |
| session (cross-session) | yes/no | `caller:session` | `user` (only if user-facing) |
| system (cron/heartbeat) | yes | — (no caller) | `user` |
| system | no | — | `dispatch({})` |

## The six `to` forms

### `caller:user` — reply to caller, asserting caller is the channel user
- **Use when**: `caller_session_key` is absent AND the wake source is a user-channel source (telegram/discord/cli/web/feishu/wecom).
- **Why over `user`**: it asserts. If the wake actually came from cron/heartbeat/another session and you slipped, validation fails and the turn continues — much better than silently routing to the wrong place.
- **Fields**: `body`.

### `caller:session` — reply to caller, asserting caller is another session
- **Use when**: `caller_session_key` is present in the wake YAML.
- **Don't silently drop cross-session wakes**: if you think a peer session sent something to the wrong recipient, reply with an explanation via `caller:session`, never `dispatch({})`. The peer needs to learn about the misroute.
- **Fields**: `body`.

### `user` — proactive message to your channel user
- **Use when**: a non-user source (cron / heartbeat / peer session) woke you and you want to reach your human user *instead of* replying to the waker. Or when current session is user-facing and you want a clean direct send regardless of caller.
- **Required**: this session must be user-facing.
- **vs `caller:user`**: same physical destination when caller IS the user, but no caller-kind assertion. Pick `caller:user` when you want the safety net; pick `user` when caller isn't the user.
- **Fields**: `body`.

### `session` — wake any existing session by key
- **Use when**: cross-session notification ("ping telegram:12345 that the report is ready").
- **Self-reference is rejected**: `session_key` cannot equal current session.
- **Recursion**: target's `dispatch(to=caller:session)` routes back to YOU, not to its channel user. Two sessions can ping-pong until one halts.
- **Fields**: `body` + `params`: either `{session_key}` (existing session) or `{channel, user_id}` (channel endpoint, created if missing).

### `subagent` — spawn (or wake existing) child thread
- **Use when**: parallel subtasks, delegation to specialty agents (`imagereader` / `audioreader` / `researcher`).
- **Key shape**: `{current}:threads:{task_id}`. Reusing `task_id` wakes the existing child (result note: `resumed`).
- **Async**: child runs independently; on completion it wakes you with `source: child_completed`.
- **Fields**: `body` + `params`: `task_id` (required, `[a-z0-9_-]+`), `agent` (optional — falls back to session default), `provider`+`model` (optional model override).

### `fork` — branch current session as new agent thread
- **Use when**: child must reason over the current conversation (reflection, summarization, scheduling against context).
- **Difference from subagent**: fork inherits stripped history; subagent starts fresh.
- **Key shape**: `{current}:fork:{task_id}`.
- **Fields**: same as subagent.

### `dispatch({})` — silent turn termination
- **Use when**: heartbeat/cron pulse where no action is warranted; truly nothing to say AND caller doesn't need to know you finished.
- **Don't use when**: you received a cross-session wake you suspect was misrouted (use `caller:session` to inform the peer). Or when caller is the user and you have nothing to add — let default sink delivery handle the empty case, or send a brief acknowledgement.

## Asking another lifeform (cross-session Q&A)

`to=session` isn't only for one-way notifications. It's the mechanism for **asking another session a question and getting an answer back** — useful when another lifeform holds context, expertise, or material you need.

The full round-trip:

1. **You ask** → `dispatch(to=session, params={session_key: "<peer>"}, body="<your question>")`. Your turn ends.
2. **Peer wakes** with `source: WakeSession` and `caller_session_key: <you>` in the YAML. From their side you are "another session" — they reply with `dispatch(to=caller:session, body="<answer>")`.
3. **You wake** with `source: WakeSession` and `caller_session_key: <peer>`. The peer's answer is the wake body. Now you handle it like any other turn — read body, decide, dispatch.

Key points:

- The exchange is **asynchronous**: you do not block. Step 1 ends your turn; step 3 fires later as a fresh wake.
- The peer's `caller:session` reply does NOT go to the peer's channel user — it routes back to **you**. The recursion is paired sink, not a broadcast.
- If the peer answers with another question (sends `caller:session` with a question body), you'll wake again with their question as caller_session_key. The chain recurses until one side halts via `dispatch({})` or redirects out via `dispatch(to=user)`.
- To **avoid runaway ping-pong**, when you have nothing more to ask, end with `dispatch({})` (silent) or `dispatch(to=user, body="...")` (forward conclusion to your channel user). Don't reflexively reply with `caller:session` if there's nothing substantive to say.
- **Tracking what you asked**: there's no automatic correlation id between the question wake and the answer wake. If you might have multiple Q&A threads in flight, mention the topic in your question body so the answer body can be matched by content (or store correlation in heartbeat.md).

Patterns:

```
# Ask peer for material on a topic
dispatch(sends=[{to: "session", params: {session_key: "telegram:42"},
                  body: "Do you have notes on the Q3 launch timeline? Share what you know."}])

# (later, you wake with caller_session_key=telegram:42 and the peer's answer)
# You then either forward to your user, store, or follow up:
dispatch(sends=[{to: "user", body: "Got the timeline from peer: ..."}])

# Follow-up question to the same peer
dispatch(sends=[{to: "session", params: {session_key: "telegram:42"},
                  body: "Thanks. One more — do you have the launch checklist too?"}])
```

**`to=session` vs `to=subagent`**: subagent spawns a *new fresh* worker thread you control (you pick agent, child has no prior context). `to=session` reaches an *existing* lifeform with its own history and identity — use this when the value is in *who they already are* (their session memory, their relationship with their own user, their accumulated context), not in spawning a fresh worker.

## Receiving child replies (`child_completed`)

When a subagent or fork finishes its work, it wakes you back with `source: child_completed`. The wake YAML carries the child's session_key (e.g. `cli:threads:find-x`) and the child's final output as the body.

This is a normal wake — your turn runs as usual, and you must end with dispatch like any other turn. The caller kind for these wakes is `session` (the child is another session), so the reply form is `caller:session` if you want to reply to the child. But typically you don't reply to the child; you forward / summarize / act on its result for the *original* user. Pattern:

1. Read the child's body from the wake.
2. Decide what to do with it:
   - **Forward to the user who triggered the original task** → `dispatch(to=user, body="...")` (proactive — the channel user wasn't the caller of *this* turn, the child was).
   - **Use the result internally and continue working** → call other tools, then dispatch as appropriate at end of turn.
   - **Spawn a follow-up** → `dispatch(to=subagent, params={task_id: "..."}, body="...")`.
   - **Result was useless / nothing to forward** → `dispatch({})` to end silently.
3. **Do NOT use `caller:user`** here — caller is the child session, not the user; that assertion fails validation.
4. **Don't accidentally `dispatch(to=caller:session)` back to the child** unless you genuinely want to send it more work — that re-wakes the child and may cause a ping-pong.

If you spawned multiple subagents in parallel (`task_id: news-a`, `news-b`), each completes independently and wakes you separately. You'll see one `child_completed` wake per child. If you want to wait for all of them before responding to the user, accumulate state in scratch (heartbeat.md or session memory) and only `dispatch(to=user)` when the last one arrives.

## Execution semantics & batch rules

### Validation vs execution: two distinct failure modes

`dispatch` runs the batch in two phases:

1. **Validation** (whole batch, atomic): static checks, caller-kind assertions, target existence, dedup. If any send fails validation, **NO sends are executed** — the turn continues, you can fix and re-call.
2. **Execution** (sequential, per-send): each validated send is dispatched in declaration order. If a send fails at execution (e.g. sink broken, peer session disappeared mid-call), already-executed sends in this batch **cannot be rolled back** — the turn ends in `partial-failure` outcome with both delivered and failed lists in the result.

Implication: validation errors are cheap retries; execution errors after partial delivery are observable side-effects you can't undo. Order your batch so the riskiest send is last, if order matters.

### Skip-dispatch path: when default sink delivery is correct

You don't HAVE to call dispatch. If you end the turn with plain assistant content and no `dispatch` tool_call, the runner forwards that content via the wake's sink (the same path as `caller:user` / `caller:session` use). This is **fine and intended** when:

- The wake source is the channel user (telegram/discord/cli/web/feishu/wecom) and you're just replying with text — equivalent to `caller:user`.
- The wake source is another session (`WakeSession` / `child_completed`) and you're just replying with text — equivalent to `caller:session`.

Do NOT rely on default delivery when:

- Wake source is `cron` / `heartbeat*` / `compression`. These wakes carry a **drop sink** — content silently goes nowhere. You MUST explicitly `dispatch(to=user)` (if user-facing) or `dispatch({})` to acknowledge end-of-turn. Plain content here is invisible.
- You need to spawn / wake / fan-out — there's no default for those.
- You want the caller-kind assertion safety net — only `caller:*` validates.

Rule of thumb: if you need to hit a non-caller target OR you're in a drop-sink wake OR you want assertion safety, use dispatch. Otherwise plain content is fine.

### `to=user` is single-channel, not multi-channel

`to=user` delivers via THIS session's `defaultSink` (the channel that owns the session key). A `telegram:42` session can `to=user` to telegram only — it cannot redirect to discord. To reach a different channel, that user must have a separate session there; use `to=session` with that session's key.

### Batch dedup: at most one caller, at most one user, distinct keys for spawns

Validation rejects:
- Two or more sends to `caller:*` in the same batch (any combination of `caller:user` / `caller:session` collapses to a single "caller" target).
- Two or more `to=user` sends.
- Two `subagent` or `fork` sends sharing the same `task_id`.
- Two `to=session` sends with the same `session_key`.

Merge the bodies if you need to say multiple things to one target. Use distinct `task_id`s for parallel fan-out.

### `task_id` reuse: spawn vs resume

Re-using a `task_id` from a previous turn **wakes the existing child** instead of spawning a new one. The result note will say `resumed`. Practical consequence:

- Want to follow up on a running child / hand it more context → reuse the same `task_id`.
- Want a fresh independent child → use a new `task_id`.

If you forget which task_ids are in flight, `check_session(session_key="<current>:threads:<task_id>")` (from `thread-ops`) tells you whether one exists.

## Common confusions

### Narrating in assistant content alongside dispatch
**Don't.** dispatch only delivers each send's `body`. Plain text in the assistant message alongside the dispatch tool_call has no recipient and the call is rejected as a validation error. Move user-facing text into a send body, or skip dispatch and let default sink delivery handle plain content.

### Caller is per-wake, not per-session
Same session can be woken by user, then cron, then a subagent — caller identity changes each turn. Re-read the wake YAML; don't carry assumptions across turns.

## Validation cheatsheet

dispatch validates the entire batch before executing anything. On validation error: nothing is delivered, turn continues, fix and re-call.

| Symptom | Likely cause |
|---|---|
| `to=caller:user but actual caller is another session` | wake YAML has `caller_session_key`; switch to `caller:session` |
| `to=caller:* but actual caller is system` | cron/heartbeat/compression wake; use `dispatch({})` or `to=user` |
| `current session is not user-facing — to=user is only valid for telegram/...` | this is a subagent/fork/cron session; reply via `caller:*` or `dispatch({})` |
| `params.task_id is required` / `params.task_id must match [a-z0-9_-]+` | subagent/fork needs a kebab/snake-case id in `params` |
| `session_key is the current session (self-reference not allowed)` | `to=session` doesn't self-loop; use `to=user` to message this session's own user, `caller:session` to reply, or `fork` for a branch |
| `unknown params key(s)` / `does not accept params` | a params key landed on the wrong target — the error names where it belongs and, for user/caller:*, the exact JSON to resend |
| `duplicate target in batch` | two sends resolve to the same target; merge bodies or pick distinct task_ids |
| Validation error mentioning non-empty assistant content | move all user-facing text into a send body, or drop the dispatch call |
| Result outcome `partial-failure` | some sends delivered, others failed at execution time. Already-delivered messages cannot be unsent — read the executed/failed lists, then on next turn act on what's still pending. |

## Examples

```
# Replying to user message in telegram:123
dispatch(sends=[{to: "caller:user", body: "Done — here's the summary..."}])

# Cron pulse, nothing to do
dispatch({})

# Cron pulse, want to nudge user
dispatch(sends=[{to: "user", body: "Reminder: meeting in 30 min"}])

# Peer session asked a question
dispatch(sends=[{to: "caller:session", body: "Yes — see attached..."}])

# Delegate research, follow up later
dispatch(sends=[{to: "subagent", params: {agent: "researcher", task_id: "find-x"}, body: "Find X"}])

# Reflect on current conversation
dispatch(sends=[{to: "fork", params: {agent: "reflector", task_id: "reflect-1"}, body: "Summarize what we decided"}])

# Notify another channel
dispatch(sends=[{to: "session", params: {session_key: "telegram:99"}, body: "Build finished"}])

# Parent receiving child_completed — forward result to user
dispatch(sends=[{to: "user", body: "Research done — summary: ..."}])

# Parent receiving child_completed — follow up on the same child (reuse task_id)
dispatch(sends=[{to: "subagent", params: {task_id: "find-x"}, body: "Good start — also check Y angle"}])

# Child reporting result back to parent (parent is caller:session from child's POV)
dispatch(sends=[{to: "caller:session", body: "Done. Findings: ..."}])

# Reply + spawn in one batch
dispatch(sends=[
  {to: "caller:user", body: "On it — checking now."},
  {to: "subagent", params: {agent: "search", task_id: "news-a"}, body: "Search topic A"}
])

# Parallel fan-out — batch investigation across multiple subagents
# Each task_id must be distinct (duplicates fail validation).
# Each child runs independently and wakes you separately with child_completed.
# To respond to the user only after all return, accumulate state in heartbeat.md
# and dispatch(to=user) when the last child arrives.
dispatch(sends=[
  {to: "caller:user", body: "Investigating across 4 angles — will report when complete."},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-pricing"},  body: "Investigate pricing landscape for X"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-competitors"}, body: "List top 5 competitors and their positioning"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-regulation"},  body: "Summarize regulatory constraints in EU/US"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-tech"},        body: "Compare available tech stacks"}
])
```
