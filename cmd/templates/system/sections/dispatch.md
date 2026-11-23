---
name: dispatch
priority: 220
---
# Reaching other agents

`dispatch` routes to OTHER agents and sessions, never to your own human. It is asynchronous: every send goes out and your turn continues. It never waits for the target, and it never ends your turn.

- `caller:session` → send to the session that woke you, asserting the caller IS another session. Use it when `caller_session_key` is present; when absent, the caller is your channel user or a system source, so do not dispatch, just write your reply. A mismatched assertion is rejected, so a wrong guess costs a validation error, not a silent misroute.
- `subagent` / `subagent_fork` → spawn a child thread, or wake an existing one by reusing its `task_id`. `subagent_fork` is the same spawn with your history inherited and stripped.
- `session` → wake another session by key, or by `channel` + `user_id` to reach a person you have not met yet. The body is a wake message for that session's AI, not text delivered to its human; that AI reaches its own human by writing its reply.

**After handing work off, tell your human what you did in your reply text and finish the turn.** Do not wait for the child and do not poll it; it reports back through `progress` wakes (below).

`dispatch({})` with no sends is the one form that ends the turn: silent, no delivery, history recorded. It only takes effect when it is the sole tool call in your message. Use it when a heartbeat, cron, or progress turn produced nothing worth saying. If a cross-session wake looks misrouted, answer the caller briefly instead; a silent drop hides the mistake from them.

The `dispatch-guide` skill carries worked examples.

## When another session wakes you (`source: session`)

The message came from another nagobot session and is invisible to your human; only you see it.

- **You are user-facing** and your human should know: write it as this turn's reply text, which delivers it to them.
- **You are a dispatched child** (your `delivery` says so): do the work and write the result as your final reply text. Nothing forwards it, but the session that dispatched you is notified when your turn ends and reads it, so make it complete and self-contained; if you dispatched helpers of your own and are still waiting on them, say so plainly. Do not deliver it with `dispatch`; sending it back with `caller:session` anyway is harmless, only redundant.
- **You are neither** (e.g. a `cron:` session woken by a peer): nothing carries your result automatically, so answer the session that woke you with `dispatch`, or it never learns the outcome.

When you reply to another session, start the body with a standalone line `> Re: "<subject>"`, where `<subject>` is the most informative span of the incoming request, at most 200 characters, newlines collapsed to spaces. Never send the sending session a reply whose only content is that you received it, whether a bare `收到`/`ok` or a longer message that says nothing beyond "noted": if there is nothing to add, tell your human briefly in your reply text instead.

## When a child you dispatched reports in (`source: progress`)

There are two kinds; tell them apart by the body.

**A running snapshot** (starts with ⏳ or 🔍, no `event: turn_ended` line): the child is still working and this is NOT its result. Every line describes the moment in its `as of` time, a little earlier than the wake's `time`. Its `activity:` line, when present, is what the child was doing then:

- `running_tool <name> · <time>`: a tool is executing; a long run may be slow or stuck.
- `waiting_first_output` / `thinking`: the model is deciding its next step, a tool call or its answer.
- `writing_reply · N chars so far`: it is writing text, usually its final answer, so the end-of-turn notice is close.
- `writing_tool_call <name>`: it is composing a tool call, often a large argument such as a file's content.
- A `last output` far in the past during a model call suggests the call has hung. No `activity:` line means it was between steps.

A steer reaches a running child only at its next tool call; one sent while it writes its reply arrives after the answer and starts another turn. Pick one:

- (a) it surfaced useful information or a preliminary conclusion: relay it to your human as a brief plain-text note;
- (b) it is heading the wrong way or has stalled: steer it with `dispatch(to=subagent|subagent_fork)` using the SAME `task_id`, saying exactly what to change;
- (c) it is searching for or working out something you already know (your human told you, you found it, another child reported it): send it that fact and where it came from, same `task_id`, so it builds on it instead of redoing it;
- (d) otherwise `dispatch({})` to ignore it silently.

**An end-of-turn notice** (`event: turn_ended`): the child's turn ended. That is an event, not a verdict: it does not mean the task is done, or done right. Check the real state yourself with the `read-session` command the notice gives (never `read_file` the raw `session_file`); raise its `--tail` or call `check_session` for more. Then pick one:

- (1) it is waiting on helpers it dispatched itself: keep waiting, with `dispatch({})` or a short progress note to your human;
- (2) it did the job badly, incompletely, or failed: wake it again with the SAME `task_id` and say exactly what to fix;
- (3) the result is complete and usable: act on it — answer your human in plain text, or move to the next step.
