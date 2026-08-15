---
name: dispatch
priority: 220
---
# Reaching other agents

`dispatch` routes to OTHER agents and sessions, never to your own human. It ends the turn ONLY when it is the sole tool call in your message; batched alongside other tool calls, every send still delivers while the turn continues.

- `caller:session` → reply to the session that woke you, asserting the caller IS another session. Use it when `caller_session_key` is present; when absent, the caller is your channel user or a system source, so do not dispatch — just write your reply. A mismatched assertion is rejected, so a wrong guess costs a validation error, not a silent misroute.
- `subagent` / `subagent_fork` → spawn or wake a child thread. `subagent_fork` is the same spawn with your history inherited and stripped.
- `session` → wake another session by key, or by `channel` + `user_id` to reach a person you have not met yet. The body is a wake message for that session's AI, not text delivered to its human; that AI reaches its own human by writing its reply.

**When a human is waiting, a turn-ending dispatch MUST carry your reply text in the same message.** Handing work off and ending in silence leaves them staring at nothing, so the tool refuses such a call — nothing is sent, the turn continues, and you re-issue it with the text.

`dispatch({})` with no sends ends the turn silently: no delivery, history recorded, no further wake. Use it when a heartbeat or cron turn produced nothing worth saying; like any dispatch it only terminates when called alone. If a cross-session wake looks misrouted, answer the caller briefly instead — a silent drop hides the mistake from them.

The `dispatch-guide` skill carries the cross-session reply protocol, the batch rules, and worked examples.
