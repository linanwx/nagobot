---
name: dispatch-guide
description: Use when about to call the `dispatch` tool and unsure which `to` form to pick, when unsure whether plain reply text will reach your human, when a dispatch validation error reports a mismatch between asserted caller kind and actual caller, or when handling a `child_completed` wake from a previously dispatched subagent/fork. Focused decision tree for speaking to your own human (plain text), routing to peers (caller:session / session), spawning (subagent / fork), receiving child results, and silent termination.
---

# Dispatch Routing Guide

**Speaking to your own human is NOT a dispatch.** There is no `to=user` target.
To say something to the human on your session's channel, write it as your
ordinary reply text and end the turn. `dispatch` exists to reach OTHER agents
and sessions, and to end a turn silently.

Whether your reply text actually reaches the human is decided by the server from
the wake source — you cannot change it by phrasing. This skill is the decision
tree for that, and for picking the right `to` value when you do dispatch.

**Reply-less turn rule**: when the wake came from your human (`sender: user`) and your dispatch is the sole tool call — so the turn ends there — the message MUST also carry your reply text. Routing the work away and ending in silence leaves the person who asked with nothing. The tool rejects such a call: no send executes, the turn continues, and you re-issue the same dispatch with your text added. `dispatch({})` with no sends is exempt — that is the deliberate way to say nothing.

**Solo rule**: dispatch terminates the turn ONLY when it is the sole tool call in your message. Batched alongside other tool calls, every send still delivers but the turn continues — you see the other tools' results and keep working. This is the progress-note pattern: write "Searching, back in a minute..." as your text + `web_search(...)` + a `dispatch(to=subagent, ...)` in one message: the note goes to your human, the search runs, and you get the results to keep reasoning. Deliveries in a batched dispatch are real — never resend them in the final dispatch.

## The 30-second decision

Look at the wake YAML frontmatter of the current turn. Three fields determine the answer:

1. **`caller_session_key`** — present? caller is another session.
2. **`source`** — decides whether your plain text reaches your human (see table).
3. **session key prefix** — `telegram:` / `discord:` / `cli` / `web` / `feishu:` / `wecom:`? this session is user-facing. If not (a subagent / internal session), plain text reaches NOBODY and the runner will make you re-do the turn until you dispatch.

Possible `source` values you may see in the wake YAML:

| source | meaning | caller kind |
|---|---|---|
| `telegram` / `discord` / `cli` / `web` / `feishu` / `wecom` | channel user message | user |
| `WakeSession` | another session woke you (cross-session) | session |
| `child_completed` | a subagent finished and is reporting back | session |
| `cron` | scheduled cron job fired (may be `--direct-wake` self-wake) | system (no caller) |
| `heartbeat` (also `heartbeat_wake` / `heartbeat_reflect` in older sessions) | heartbeat scheduler pulse | system (no caller) |
| `compression` | context compression wake | system (no caller) |
| `resume` | internal re-processing | system (no caller) |

**Does my plain reply text reach my human?** (user-facing sessions only):

| source | plain text reaches your human? |
|---|---|
| channel user message | yes — this is a normal reply |
| `WakeSession` / `child_completed` | yes — it goes to your human, NOT to the caller |
| `cron` | yes |
| `progress` | yes |
| `heartbeat*` / `compression` | **no — nothing you write reaches anyone** |

Then:

| caller kind | this session is user-facing | reply to caller | reach your own human |
|---|---|---|---|
| user (channel wake) | yes | plain text | plain text |
| session (cross-session) | yes | `caller:session` | plain text |
| session (cross-session) | no | `caller:session` (required) | — no human |
| system (cron) | yes | — (no caller) | plain text |
| system (heartbeat/compression) | yes | — | — impossible, end with `dispatch({})` |
| system | no | — | `dispatch({})` |

## The five `to` forms

### `caller:session` — reply to caller, asserting caller is another session
- **Use when**: `caller_session_key` is present in the wake YAML.
- **Don't silently drop cross-session wakes**: if you think a peer session sent something to the wrong recipient, reply with an explanation via `caller:session`, never `dispatch({})`. The peer needs to learn about the misroute.
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

### `subagent_fork` — branch current session as new agent thread
- **Use when**: child must reason over the current conversation (reflection, summarization, scheduling against context).
- **Difference from `subagent`**: `subagent_fork` inherits stripped history; `subagent` starts fresh. Everything else — params, key handling, async completion — is identical.
- **Key shape**: `{current}:fork:{task_id}` — the key infix is `:fork:`, NOT `:subagent_fork:`. The target was renamed; the session key was not.
- **Fields**: same as subagent.

### `dispatch({})` — silent turn termination
- **Use when**: heartbeat/cron pulse where no action is warranted; truly nothing to say AND caller doesn't need to know you finished.
- **Don't use when**: you received a cross-session wake you suspect was misrouted (use `caller:session` to inform the peer). Or when caller is the user and you have nothing to add — let default sink delivery handle the empty case, or send a brief acknowledgement.
- **Solo rule applies**: batched with other tool calls, `dispatch({})` is a no-op (nothing sent, turn continues). To actually end the turn silently it must be the only tool call in the message.

## Asking another lifeform (cross-session Q&A)

`to=session` isn't only for one-way notifications. It's the mechanism for **asking another session a question and getting an answer back** — useful when another lifeform holds context, expertise, or material you need.

The full round-trip:

1. **You ask** → `dispatch(to=session, params={session_key: "<peer>"}, body="<your question>")`. Your turn ends.
2. **Peer wakes** with `source: WakeSession` and `caller_session_key: <you>` in the YAML. From their side you are "another session" — they reply with `dispatch(to=caller:session, body="<answer>")`.
3. **You wake** with `source: WakeSession` and `caller_session_key: <peer>`. The peer's answer is the wake body. Now you handle it like any other turn — read body, decide, dispatch.

Key points:

- The exchange is **asynchronous**: you do not block. Step 1 ends your turn; step 3 fires later as a fresh wake.
- The peer's `caller:session` reply does NOT go to the peer's channel user — it routes back to **you**. The recursion is paired sink, not a broadcast.
- If the peer answers with another question (sends `caller:session` with a question body), you'll wake again with their question as caller_session_key. The chain recurses until one side halts via `dispatch({})` or stops replying to the peer and just answers its own human in plain text.
- To **avoid runaway ping-pong**, when you have nothing more to ask, end with `dispatch({})` (silent) or simply write your conclusion as plain text (which goes to your channel user, not back to the peer). Don't reflexively reply with `caller:session` if there's nothing substantive to say.
- **Tracking what you asked**: there's no automatic correlation id between the question wake and the answer wake. If you might have multiple Q&A threads in flight, mention the topic in your question body so the answer body can be matched by content (or store correlation in heartbeat.md).

### Quoting what you are answering (`> Re:`)

When you reply back to a cross-session caller — either explicitly via
`dispatch(to=caller:session)` OR by emitting a naive final text response (both
route through the same wake sink) — **prefix the body with a standalone line
`> Re: "<excerpt>"` before the reply**.

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
                  body: "> Re: \"Do you have notes on the Q3 launch timeline?\"\nYes — the timeline moved to Nov 14, checklist attached below."}])
```

Patterns:

```
# Ask peer for material on a topic
dispatch(sends=[{to: "session", params: {session_key: "telegram:42"},
                  body: "Do you have notes on the Q3 launch timeline? Share what you know."}])

# (later, you wake with caller_session_key=telegram:42 and the peer's answer)
# To forward it to your own user, just write it as plain text — no dispatch:
Got the timeline from peer: ...

# Follow-up question to the same peer
dispatch(sends=[{to: "session", params: {session_key: "telegram:42"},
                  body: "Thanks. One more — do you have the launch checklist too?"}])
```

**`to=session` vs `to=subagent`**: subagent spawns a *new fresh* worker thread you control (you pick agent, child has no prior context). `to=session` reaches an *existing* lifeform with its own history and identity — use this when the value is in *who they already are* (their session memory, their relationship with their own user, their accumulated context), not in spawning a fresh worker.

## Receiving child replies (`child_completed`)

When a subagent finishes its work, it wakes you back with `source: child_completed`. The wake YAML carries the child's session_key (e.g. `cli:threads:find-x`) and the child's final output as the body.

This is a normal wake — your turn runs as usual, and you must end with dispatch like any other turn. The caller kind for these wakes is `session` (the child is another session), so the reply form is `caller:session` if you want to reply to the child. But typically you don't reply to the child; you forward / summarize / act on its result for the *original* user. Pattern:

1. Read the child's body from the wake.
2. Decide what to do with it:
   - **Forward to the user who triggered the original task** → just write it as your plain reply text. The channel user wasn't the caller of *this* turn (the child was), but plain text always goes to your own human.
   - **Use the result internally and continue working** → call other tools, then dispatch as appropriate at end of turn.
   - **Spawn a follow-up** → `dispatch(to=subagent, params={task_id: "..."}, body="...")`.
   - **Result was useless / nothing to forward** → `dispatch({})` to end silently.
3. **Plain text goes to your human, not the child** — to send something back to the child you must dispatch `caller:session`. Usually you don't: you forward the child's result to your human, which is exactly what plain text does.
4. **Don't accidentally `dispatch(to=caller:session)` back to the child** unless you genuinely want to send it more work — that re-wakes the child and may cause a ping-pong.

If you spawned multiple subagents in parallel (`task_id: news-a`, `news-b`), each completes independently and wakes you separately. You'll see one `child_completed` wake per child. If you want to wait for all of them before responding to the user, accumulate state in scratch (heartbeat.md or session memory) and stay silent with `dispatch({})` until the last one arrives, then answer in plain text.

## Execution semantics & batch rules

### Validation vs execution: two distinct failure modes

`dispatch` runs the batch in two phases:

1. **Validation** (whole batch, atomic): static checks, caller-kind assertions, target existence, dedup. If any send fails validation, **NO sends are executed** — the turn continues, you can fix and re-call.
2. **Execution** (sequential, per-send): each validated send is dispatched in declaration order. If a send fails at execution (e.g. sink broken, peer session disappeared mid-call), already-executed sends in this batch **cannot be rolled back** — the result carries `partial-failure` outcome with both delivered and failed lists (the turn ends only if dispatch was the sole tool call; see the solo rule).

Implication: validation errors are cheap retries; execution errors after partial delivery are observable side-effects you can't undo. Order your batch so the riskiest send is last, if order matters.

### Skip-dispatch path: plain text is the normal way to answer

You don't HAVE to call dispatch. On a user-facing session, ending the turn with
plain assistant content and no `dispatch` tool_call delivers that content to your
human. This is **the normal path** when:

- The channel user woke you and you're just replying.
- A peer session or child woke you and you want to tell your *human* the outcome (it does NOT go back to the caller).
- A `cron` or `progress` wake fired and you have something worth saying.

You MUST dispatch when:

- Wake source is `heartbeat*` / `compression`. Nothing you write reaches anyone; end with `dispatch({})` so the turn terminates cleanly.
- This session is **not user-facing** (subagent / internal). Plain text has no destination, so the runner rejects a text-only reply and re-iterates until you dispatch — usually `caller:session` to report back.
- You need to spawn / wake / fan-out — there's no plain-text equivalent.
- You want the caller-kind assertion safety net — only `caller:session` validates.

### Reaching your human is single-channel, not multi-channel

Your reply text goes to the channel that owns this session key. A `telegram:42`
session reaches telegram only — it cannot redirect to discord. To reach a
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

- Want to follow up on a running child / hand it more context → reuse the same `task_id`.
- Want a fresh independent child → use a new `task_id`.

If you forget which task_ids are in flight, `check_session(session_key="<current>:threads:<task_id>")` (from `thread-ops`) tells you whether one exists.

## Common confusions

### Narrating in assistant content alongside dispatch
**Do.** Writing your report as assistant content while routing work with dispatch is the normal shape — when you hand work off, tell your own human what you just did.

The two are independent: dispatch delivers each send's `body`, and your content is delivered separately if this turn's wake source allows it. On a turn with no destination for plain content (heartbeat, or a session with no human of its own) the content is not delivered — the tool result says exactly what became of it (`reached nobody` / `DISCARDED` / `not delivered as the reply`), so nothing disappears silently. Anything that must reach someone belongs in a send `body`.

### Caller is per-wake, not per-session
Same session can be woken by user, then cron, then a subagent — caller identity changes each turn. Re-read the wake YAML; don't carry assumptions across turns.

## Validation cheatsheet

dispatch validates the entire batch before executing anything. On validation error: nothing is delivered, turn continues, fix and re-call.

| Symptom | Likely cause |
|---|---|
| `to=caller:session but actual caller is the channel user` | no `caller_session_key` in the wake; the user woke you — drop the dispatch and just reply in plain text |
| `to=caller:session but actual caller is system` | cron/heartbeat/compression wake; use `dispatch({})`, or (cron only) plain reply text |
| `params.task_id is required` / `params.task_id must match [a-z0-9_-]+` | subagent / subagent_fork needs a kebab/snake-case id in `params` |
| `session_key is the current session (self-reference not allowed)` | `to=session` doesn't self-loop; write plain text to reach this session's own human, `caller:session` to reply to a peer, or `subagent_fork` for a branch |
| `unknown params key(s)` / `does not accept params` | a params key landed on the wrong target — the error names where it belongs and, for caller:session, the exact JSON to resend |
| `duplicate target in batch` | two sends resolve to the same target; merge bodies or pick distinct task_ids |
| Result outcome `partial-failure` | some sends delivered, others failed at execution time. Already-delivered messages cannot be unsent — read the executed/failed lists, then on next turn act on what's still pending. |
| Result outcome `delivered-turn-continues` | sends delivered but the turn did NOT end — dispatch was batched with other tool calls (solo rule). Keep working; do not resend the delivered bodies. |
| Result outcome `no-op` | `dispatch({})` was batched with other tool calls, so nothing terminated. Call it alone to end the turn silently. |

## Examples

```
# Replying to user message in telegram:123 — no dispatch at all
Done — here's the summary...

# Cron pulse, nothing to do
dispatch({})

# Cron pulse, want to nudge user — again just plain text
Reminder: meeting in 30 min

# Heartbeat pulse — nothing you write can reach the user; end explicitly
dispatch({})

# Peer session asked a question
dispatch(sends=[{to: "caller:session", body: "Yes — see attached..."}])

# Delegate research, follow up later
dispatch(sends=[{to: "subagent", params: {agent: "researcher", task_id: "find-x"}, body: "Find X"}])

# Reflect on current conversation
dispatch(sends=[{to: "subagent_fork", params: {agent: "reflector", task_id: "reflect-1"}, body: "Summarize what we decided"}])

# Notify another channel
dispatch(sends=[{to: "session", params: {session_key: "telegram:99"}, body: "Build finished"}])

# Parent receiving child_completed — forward result to user (plain text)
Research done — summary: ...

# Parent receiving child_completed — follow up on the same child (reuse task_id)
dispatch(sends=[{to: "subagent", params: {task_id: "find-x"}, body: "Good start — also check Y angle"}])

# Child reporting result back to parent (parent is caller:session from child's POV)
dispatch(sends=[{to: "caller:session", body: "Done. Findings: ..."}])

# Reply + spawn in one message: text goes to your human, dispatch spawns the child
On it — checking now.
dispatch(sends=[
  {to: "subagent", params: {agent: "search", task_id: "news-a"}, body: "Search topic A"}
])

# Parallel fan-out — batch investigation across multiple subagents
# Each task_id must be distinct (duplicates fail validation).
# Each child runs independently and wakes you separately with child_completed.
# To respond to the user only after all return, accumulate state in heartbeat.md
# and answer in plain text when the last child arrives.
Investigating across 4 angles — will report when complete.
dispatch(sends=[
  {to: "subagent", params: {agent: "researcher", task_id: "angle-pricing"},  body: "Investigate pricing landscape for X"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-competitors"}, body: "List top 5 competitors and their positioning"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-regulation"},  body: "Summarize regulatory constraints in EU/US"},
  {to: "subagent", params: {agent: "researcher", task_id: "angle-tech"},        body: "Compare available tech stacks"}
])
```
