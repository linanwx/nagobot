---
name: dispatch
priority: 220
---
# Reaching other agents

`dispatch` routes to OTHER agents and sessions, never to your own human. It is asynchronous: every send goes out and your turn continues. It never waits for the target, and it never ends your turn.

- `caller:session` → send to the session that woke you, asserting the caller IS another session. Use it when `caller_session_key` is present; when absent, the caller is your channel user or a system source, so do not dispatch, just write your reply. A mismatched assertion is rejected, so a wrong guess costs a validation error, not a silent misroute.
- `subagent` / `subagent_fork` → spawn a child thread, or wake an existing one by reusing its `task_id`. `subagent_fork` is the same spawn with your history inherited and stripped.
- `session` → wake another session by key, or by `channel` + `user_id` to reach a person you have not met yet. The body is a wake message for that session's AI, not text delivered to its human; that AI reaches its own human by writing its reply.

**After handing work off, tell your human what you did in your reply text and finish the turn.** Do not wait for the child and do not poll it. When a child's turn ends you get a `progress` wake marked `event: turn_ended`: an event, not a verdict. Read the child's last output from its `session_file`, then keep waiting, send it back to fix something (same `task_id`), or use the result.

**When you are the dispatched child**, your final reply text is your result: the session that dispatched you is notified when your turn ends and reads it. You do not need to dispatch it back.

`dispatch({})` with no sends is the one form that ends the turn: silent, no delivery, history recorded. It only takes effect when it is the sole tool call in your message. Use it when a heartbeat, cron, or progress turn produced nothing worth saying. If a cross-session wake looks misrouted, answer the caller briefly instead; a silent drop hides the mistake from them.

The `dispatch-guide` skill carries the cross-session reply protocol and worked examples.
