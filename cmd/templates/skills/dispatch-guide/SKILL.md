---
name: dispatch-guide
description: Use when about to call the `dispatch` tool and unsure which `to` form to pick, when unsure whether plain reply text will reach your human, when a dispatch validation error reports a mismatch between asserted caller kind and actual caller, or when handling a `child_completed` wake from a previously dispatched subagent/fork. Focused decision tree for speaking to your own human (plain text), routing to peers (caller:session / session), spawning (subagent / fork), receiving child results, and silent termination.
---

# Dispatch Routing Guide

**Speaking to your own human is NOT a dispatch.** There is no `to=user` target.
To say something to the human on your session's channel, write it as your
ordinary reply text. `dispatch` exists to reach OTHER agents and sessions, and
to end a turn silently.

**dispatch is asynchronous.** Every send goes out and your turn continues: the
tool returns immediately, never waits for the target, and never ends your turn.
After handing work off, tell your human what you did in plain reply text and
finish the turn normally. `dispatch({})` with no sends is the one exception: it
ends the turn silently.

Whether your reply text actually reaches the human is decided by the server from
the wake source; you cannot change it by phrasing. This skill is the decision
tree for that, and for picking the right `to` value when you do dispatch.

## The 30-second decision

Look at the wake YAML frontmatter of the current turn. Three fields determine the answer:

1. **`caller_session_key`**: present? caller is another session.
2. **`source`**: decides whether your plain text reaches your human (see table).
3. **session key**: `telegram:` / `discord:` / `cli` / `web` / `feishu:` / `wecom:` with no `:threads:` / `:fork:` in it? this session is user-facing. A key containing `:threads:` / `:fork:` is a dispatched child: your final reply text is your result, and the session that dispatched you reads it (see "When you are the dispatched thread").

Possible `source` values you may see in the wake YAML:

| source | meaning | caller kind |
|---|---|---|
| `telegram` / `discord` / `cli` / `web` / `feishu` / `wecom` | channel user message | user |
| `session` | another session woke you (a peer, or the session that dispatched you) | session |
| `progress` | a thread you dispatched reports in: a running-progress note, or `event: turn_ended` when its turn ended | system |
| `cron` | scheduled cron job fired (may be `--direct-wake` self-wake) | system (no caller) |
| `heartbeat` (also `heartbeat_wake` / `heartbeat_reflect` in older sessions) | heartbeat scheduler pulse | system (no caller) |
| `compression` | context compression wake | system (no caller) |
| `resume` | internal re-processing | system (no caller) |

**Does my plain reply text reach my human?** (user-facing sessions only):

| source | plain text reaches your human? |
|---|---|
| channel user message | yes, this is a normal reply |
| `session` | yes, it goes to your human, NOT to the caller |
| `cron` | yes |
| `progress` | yes |
| `heartbeat*` / `compression` | **no, nothing you write reaches anyone** |

Then:

| caller kind | this session | reply to caller | reach your own human |
|---|---|---|---|
| user (channel wake) | user-facing | plain text | plain text |
| session (cross-session) | user-facing | `caller:session` | plain text |
| session (cross-session) | dispatched child | plain text result, or `caller:session` | no human |
| session (cross-session) | no human, not a child (e.g. `cron:`) | `caller:session` (required) | no human |
| system (cron) | user-facing | no caller | plain text |
| system (heartbeat/compression) | any | no caller | impossible, end with `dispatch({})` |

## The five `to` forms

### `caller:session`: reply to caller, asserting caller is another session
- **Use when**: `caller_session_key` is present in the wake YAML.
- **Don't silently drop cross-session wakes**: if you think a peer session sent something to the wrong recipient, reply with an explanation via `caller:session`, never `dispatch({})`. The peer needs to learn about the misroute.
- **Fields**: `body`.

### `session`: wake any existing session by key
- **Use when**: cross-session notification ("ping telegram:12345 that the report is ready").
- **Self-reference is rejected**: `session_key` cannot equal current session.
- **Recursion**: target's `dispatch(to=caller:session)` routes back to YOU, not to its channel user. Two sessions can ping-pong until one stops replying.
- **Fields**: `body` + `params`: either `{session_key}` (existing session) or `{channel, user_id}` (channel endpoint, created if missing).

### `subagent`: spawn (or wake existing) child thread
- **Use when**: parallel subtasks, delegation to specialty agents (`imagereader` / `audioreader` / `researcher`).
- **Key shape**: `{current}:threads:{task_id}`. Reusing `task_id` wakes the existing child (result note: `resumed`).
- **Async**: the child runs independently. When its turn ends you get a `progress` wake marked `event: turn_ended` (see "Receiving child results").
- **Fields**: `body` + `params`: `task_id` (required, `[a-z0-9_-]+`), `agent` (optional, falls back to session default), `provider`+`model` (optional model override).

### `subagent_fork`: branch current session as new agent thread
- **Use when**: child must reason over the current conversation (reflection, summarization, scheduling against context).
- **Difference from `subagent`**: `subagent_fork` inherits stripped history; `subagent` starts fresh. Everything else (params, key handling, the end-of-turn notice) is identical.
- **Key shape**: `{current}:fork:{task_id}`. The key infix is `:fork:`, NOT `:subagent_fork:`. The target was renamed; the session key was not.
- **Fields**: same as subagent.

### `dispatch({})`: silent turn termination
- **Use when**: a heartbeat/cron/progress turn where no action is warranted; truly nothing to say AND caller doesn't need to know you finished.
- **Don't use when**: you received a cross-session wake you suspect was misrouted (use `caller:session` to inform the peer). Or when your human is waiting: give them at least a brief reply.
- **Must be alone**: batched with other tool calls, `dispatch({})` is a no-op (nothing sent, turn continues). To actually end the turn silently it must be the only tool call in the message.

## Asking another lifeform (cross-session Q&A)

`to=session` isn't only for one-way notifications. It's the mechanism for **asking another session a question and getting an answer back**, useful when another lifeform holds context, expertise, or material you need.

The full round-trip:

1. **You ask**: `dispatch(to=session, params={session_key: "<peer>"}, body="<your question>")`. Your turn continues; tell your human you asked, then finish.
2. **Peer wakes** with `source: session` and `caller_session_key: <you>` in the YAML. From their side you are "another session"; they reply with `dispatch(to=caller:session, body="<answer>")`.
3. **You wake** with `source: session` and `caller_session_key: <peer>`. The peer's answer is the wake body. Now you handle it like any other turn.

Key points:

- The exchange is **asynchronous**: you do not block and must not poll. Step 3 fires later as a fresh wake.
- The peer's `caller:session` reply does NOT go to the peer's channel user; it routes back to **you**. The recursion is a paired sink, not a broadcast.
- If the peer answers with another question, you'll wake again with it. The chain recurses until one side stops replying to the peer and just answers its own human in plain text, or ends with `dispatch({})`.
- To **avoid runaway ping-pong**, when you have nothing more to ask, simply write your conclusion as plain text (which goes to your channel user, not back to the peer). Don't reflexively reply with `caller:session` if there's nothing substantive to say.
- **Tracking what you asked**: there's no automatic correlation id between the question wake and the answer wake. If you might have multiple Q&A threads in flight, mention the topic in your question body so the answer body can be matched by content (or store correlation in heartbeat.md).

### Quoting what you are answering (`> Re:`)

When you reply back to a cross-session caller via `dispatch(to=caller:session)`,
**prefix the body with a standalone line `> Re: "<excerpt>"` before the reply**.

`<excerpt>` is up to **200 characters** taken from the incoming request body,
with all newlines collapsed to single spaces. Do NOT just quote the first line:
it is often a vague preamble with no information content. Pull from across the
message to capture the actual ask.

This is the correlation mechanism the previous point describes. The caller
session may be juggling many concurrent threads and will not remember which
outbound each inbound reply corresponds to; the excerpt is how it matches your
reply back to its original request.

```
dispatch(sends=[{to: "caller:session",
                  body: "> Re: \"Do you have notes on the Q3 launch timeline?\"\nYes, the timeline moved to Nov 14, checklist attached below."}])
```

Patterns:

```
# Ask peer for material on a topic, then tell your human
dispatch(sends=[{to: "session", params: {session_key: "telegram:42"},
                  body: "Do you have notes on the Q3 launch timeline? Share what you know."}])
I've asked the telegram:42 session for the Q3 timeline; I'll pass it on when it answers.

# (later, you wake with caller_session_key=telegram:42 and the peer's answer)
# To forward it to your own user, just write it as plain text, no dispatch:
Got the timeline from peer: ...
```

**`to=session` vs `to=subagent`**: subagent spawns a *new fresh* worker thread you control (you pick agent, child has no prior context). `to=session` reaches an *existing* lifeform with its own history and identity; use this when the value is in *who they already are* (their session memory, their relationship with their own user, their accumulated context), not in spawning a fresh worker.

## Receiving child results (`progress`, `event: turn_ended`)

A subagent/fork you dispatched does not send its result to you. Its final reply
text stays in its own session. What you get is an **event**: every time the
child's turn ends, you are woken with `source: progress` and a body like:

```
🏁 subagent cli:threads:find-x ended its turn · 2m · 14 steps
event: turn_ended
child_session: cli:threads:find-x
session_file: /.../sessions/cli/threads/find-x/session.jsonl

<short report of how the turn ended>

This notice only says the turn ENDED, not that the task is done or done right. ...
```

A `⚠️ ... ended its turn with an error` header means the turn failed.

The notice says the turn ended, nothing more. The short report is a hint, not
the result. Check the actual state yourself:

1. **Read the child's output**: `read_file` the `session_file` and find the last
   `role=assistant` entry. Look at earlier entries or `check_session` only when
   that is not enough.
2. **Decide, one of three**:
   - **The child is still waiting** on threads it dispatched itself (its last
     output says so): keep waiting. End with `dispatch({})`, or give your human a
     one-line status. Another notice arrives when the child's next turn ends.
   - **The child did not do it well** (error, off-topic, incomplete, wrong):
     send it back with `dispatch(to=subagent|subagent_fork)` and the **same
     `task_id`**, saying exactly what to fix. Its next turn end notifies you again.
   - **The result is complete and usable**: deliver it to your human in plain
     text, or continue your own work with it.

Plain text in this turn goes to your human, not the child. Don't
`dispatch(to=caller:session)` here: the caller of a progress wake is the
system, not the child.

**Running-progress notes** (`source: progress`, body starting with `⏳`, no
`event: turn_ended`) are different: the child is still mid-turn and this is not
a result. Relay a one-line update to your human if it is worth it, otherwise
`dispatch({})`.

**Parallel children**: each child sends its own notice when its turn ends. You
can answer as each one lands, or wait for the last; notices already handled are
in your history, so there is no need to keep scratch state.

## When you are the dispatched thread

If your session key contains `:threads:` or `:fork:`, a session dispatched you.

- **Your final reply text is your result.** Write it and let the turn end. The
  dispatching session is notified when your turn ends and reads your last reply
  from your session file. You do not need to dispatch it back.
- `dispatch(to=caller:session)` is optional: use it to push something to the
  dispatching session mid-way (it wakes it immediately), not to deliver the
  final result.
- If you dispatched threads of your own and cannot finish until they report,
  **end the turn with a reply that says plainly the task is not finished yet and
  what you are waiting on**. The dispatching session reads that sentence and
  keeps waiting. When your own child's notice arrives, finish the work; your next
  turn end notifies the dispatching session again.

## Execution semantics & batch rules

### Validation vs execution: two distinct failure modes

`dispatch` runs the batch in two phases:

1. **Validation** (whole batch, atomic): static checks, caller-kind assertions, target existence, dedup. If any send fails validation, **NO sends are executed**; fix and re-call.
2. **Execution** (sequential, per-send): each validated send is dispatched in declaration order. If a send fails at execution (e.g. sink broken, peer session disappeared mid-call), already-executed sends in this batch **cannot be rolled back**; the result carries a `partial-failure` outcome with both delivered and failed lists.

Either way the turn continues. Validation errors are cheap retries; execution errors after partial delivery are observable side-effects you can't undo. Order your batch so the riskiest send is last, if order matters.

### Plain text is the normal way to answer

You don't HAVE to call dispatch. On a user-facing session, plain assistant
content delivers to your human. This is **the normal path** when:

- The channel user woke you and you're just replying.
- A peer session woke you and you want to tell your *human* the outcome (it does NOT go back to the caller).
- A `cron` or `progress` wake fired and you have something worth saying.
- You just dispatched work and are telling your human what you started.

You MUST dispatch when:

- Wake source is `heartbeat*` / `compression` and you want to end cleanly: `dispatch({})`.
- This session has **no human and no dispatcher** (e.g. a `cron:` session woken by a peer). Plain text has no destination there, so the runner rejects a text-only reply until you answer the peer with `caller:session`.
- You need to spawn / wake / fan-out; there's no plain-text equivalent.
- You want the caller-kind assertion safety net; only `caller:session` validates.

### Reaching your human is single-channel, not multi-channel

Your reply text goes to the channel that owns this session key. A `telegram:42`
session reaches telegram only; it cannot redirect to discord. To reach a
different channel, that user must have a separate session there; use `to=session`
with that session's key.

### Batch dedup: at most one caller, distinct keys for spawns

Validation rejects:
- Two or more `caller:session` sends in the same batch (they collapse to a single "caller" target).
- Two `subagent` or `subagent_fork` sends sharing the same `task_id`.
- Two `to=session` sends with the same `session_key`.

Merge the bodies if you need to say multiple things to one target. Use distinct `task_id`s for parallel fan-out.

### `task_id` reuse: spawn vs resume

Re-using a `task_id` from a previous turn **wakes the existing child** instead of spawning a new one. The result note will say `resumed`. Practical consequence:

- Want to follow up on a child / send it back to fix something → reuse the same `task_id`.
- Want a fresh independent child → use a new `task_id`.

If you forget which task_ids exist, `check_session(session_key="<current>:threads:<task_id>")` (from `thread-ops`) tells you whether one exists.

## Common confusions

### Narrating in assistant content alongside dispatch
**Do.** Writing a note to your human as assistant content while routing work with dispatch is the normal shape: when you hand work off, tell your own human what you just did. The two are independent: dispatch delivers each send's `body`, and your content reaches your human if this turn's wake source allows it.

### Waiting for a child
**Don't.** There is nothing to wait on inside a turn: dispatch has already returned and the child runs on its own. Do not poll with `check_session` or `sleep`. Finish the turn; the `turn_ended` notice wakes you.

### Caller is per-wake, not per-session
Same session can be woken by user, then cron, then a progress notice; caller identity changes each turn. Re-read the wake YAML; don't carry assumptions across turns.

## Validation cheatsheet

dispatch validates the entire batch before executing anything. On validation error: nothing is delivered, turn continues, fix and re-call.

| Symptom | Likely cause |
|---|---|
| `to=caller:session but actual caller is the channel user` | no `caller_session_key` in the wake; the user woke you. Drop the dispatch and just reply in plain text |
| `to=caller:session but actual caller is system` | cron/heartbeat/compression/progress wake; use `dispatch({})`, or plain reply text |
| `params.task_id is required` / `params.task_id must match [a-z0-9_-]+` | subagent / subagent_fork needs a kebab/snake-case id in `params` |
| `session_key is the current session (self-reference not allowed)` | `to=session` doesn't self-loop; write plain text to reach this session's own human, `caller:session` to reply to a peer, or `subagent_fork` for a branch |
| `unknown params key(s)` / `does not accept params` | a params key landed on the wrong target; the error names where it belongs and, for caller:session, the exact JSON to resend |
| `duplicate target in batch` | two sends resolve to the same target; merge bodies or pick distinct task_ids |
| Result outcome `delivered` | every send went out and the turn continues. Do not resend; finish the turn |
| Result outcome `partial-failure` | some sends delivered, others failed at execution time. Already-delivered messages cannot be unsent; read the executed/failed lists and act on what's still pending |
| Result outcome `no-op` | `dispatch({})` was batched with other tool calls, so nothing terminated. Call it alone to end the turn silently |

## Examples

```
# Replying to user message in telegram:123: no dispatch at all
Done, here's the summary...

# Cron pulse, nothing to do
dispatch({})

# Cron pulse, want to nudge user: again just plain text
Reminder: meeting in 30 min

# Heartbeat pulse: nothing you write can reach the user; end explicitly
dispatch({})

# Peer session asked a question
dispatch(sends=[{to: "caller:session", body: "> Re: \"...\"\nYes, see attached..."}])

# Delegate research, tell your human, finish the turn
dispatch(sends=[{to: "subagent", params: {agent: "researcher", task_id: "find-x"}, body: "Find X"}])
I've started a researcher on X; I'll report back when it finishes.

# Reflect on current conversation
dispatch(sends=[{to: "subagent_fork", params: {agent: "reflector", task_id: "reflect-1"}, body: "Summarize what we decided"}])

# Notify another channel
dispatch(sends=[{to: "session", params: {session_key: "telegram:99"}, body: "Build finished"}])

# progress turn_ended for find-x: read the result first
read_file(path: "<session_file from the notice>")
# ...its last assistant entry is a complete answer: deliver it
Research done. Summary: ...

# ...its last assistant entry missed the Y angle: send it back, same task_id
dispatch(sends=[{to: "subagent", params: {task_id: "find-x"}, body: "Good start, but you skipped Y. Cover Y too."}])
The first pass missed Y; I've asked for that part too.

# ...its last assistant entry says it is still waiting on its own subagent
dispatch({})

# You ARE the child: just write the result as your reply
Findings: 1) ... 2) ...

# Parallel fan-out: distinct task_ids; each child notifies you separately
dispatch(sends=[
  {to: "subagent", params: {agent: "researcher", task_id: "angle-pricing"},  body: "Investigate pricing landscape for X"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-competitors"}, body: "List top 5 competitors and their positioning"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-regulation"},  body: "Summarize regulatory constraints in EU/US"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-tech"},        body: "Compare available tech stacks"}
])
Investigating across 4 angles; I'll report as they come in.
```
