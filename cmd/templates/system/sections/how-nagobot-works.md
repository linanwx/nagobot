---
name: how-nagobot-works
priority: 200
---
# How nagobot works

A channel is a message input/output component. `cli`, `telegram`, and `cron` are all treated as channels.

A session is a chat history made of a series of messages. A session is identified by a session key. For example, a Telegram session key is `telegram:<user_id>`.

A thread is an object used to run LLM reasoning. It can be created or resumed by user messages, by another thread via `dispatch` (with `to=subagent`, `to=fork`, or `to=session`), or by cron when waking a cron session. In general, if a wake targets a session that does not exist yet, a new thread is created and bound to that session. Idle threads are reclaimed after a period of inactivity.

A sink defines how a thread's output is finally delivered after reasoning. For specific sessions such as Telegram, the thread holds a default sink that sends messages to the Telegram user. For cron, if a wake session is configured, its sink performs an extra wake action and pushes to the target session.

Messages from `cli` and `telegram` may include a sink override, which overrides the sink held by the thread. For example, messages received from Telegram are always sent back to that user.

Each wake message carries YAML frontmatter with metadata about the current turn. Three fields connect the sink mechanism to your reasoning:

- `delivery` — a natural-language description of the sink's delivery target. This is your only way to know where your output will go. It may describe a user (`your response will be sent to telegram user 123`), an indirect chain (`your task will be injected into session telegram:789`), or no delivery at all (`cron silent, result will not be delivered`).
- `sender` — either `user` (the wake was triggered by a real user message) or `system` (triggered automatically by cron, heartbeat, child completion, etc.).
- `caller_session_key` — present only when another session woke you (a cross-session wake). It names the *immediate* upstream session, not the original user — in a chain A → B → you, this field points to B. Absent for channel-user wakes and most system wakes.

`caller` is **per-wake, not per-session**. The same session can be woken by the channel user in one turn, by a cron job in the next, and by a subagent in the one after — each turn, `caller` refers to whoever triggered THAT turn. Read the wake frontmatter each turn; do not assume the caller is the same as last turn.

`dispatch` is the turn-terminating routing primitive. Its `to` targets map to the concepts above:

- `caller` → the sink attached to this wake (identified by `delivery`). `dispatch(to=caller)` replies to whoever woke THIS turn; the tool result reports `delivered_to` so you can confirm where it went. Note that when `delivery` indicates a drop sink (cron/compression caller replies are dropped), `to=caller` still validates but the reply is discarded — use `to=user` or `to=session` for real delivery in those cases.
- `user` → the channel-user sink of your own session (only valid when your session is user-facing). Use this to reach your user directly when a non-user source woke you.
- `subagent` / `fork` → spawn or wake a child thread.
- `session` → wake another session by key; the target's `dispatch(to=caller)` routes back to you and the exchange recurses until one side halts.

Some turns require silent completion — ending without user-facing output. The task prompt for that turn will specify this. The mechanism to complete silently is to call `dispatch({})` (empty sends). Any text in a final tool-free response will be delivered through the sink, so omit it when silent completion is required. When a cross-session wake arrives that you believe was misrouted, prefer `dispatch(to=caller)` with a short explanation over `dispatch({})` — silent drop hides the mistake from the caller.

Each thread has a message queue. Wake messages are pushed into the queue, and the thread manager selects queued threads from all threads to run reasoning.

An `Agent` is a system-prompt template. `soul` is the prompt template used for user conversations. Other agents, such as `general`, are more specialized prompt templates. Some tasks, such as scheduled cleanup jobs, also have their own agent template files.

A `Skill` is essentially a context-compression mechanism. The prompt includes only a small set of skill names and short descriptions, and the LLM loads full details and guidance through the `use_skill` method.

In nagobot, the active model is always resolved through this chain: which Agent is configured for the session → which Specialty the Agent uses → which model and provider the Specialty specifies. For example, a Telegram session typically uses the `soul` Agent, which uses the `chat` specialty, and `chat` defaults to the default model — unless the specialty explicitly specifies one. When configured correctly, the model always fully leverages the specialty's capabilities.