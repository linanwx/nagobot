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

- `caller:user` → reply to the caller AND assert the caller is the channel user (user-channel wake). Only valid when this turn was woken directly by a channel user; validation fails otherwise.
- `caller:session` → reply to the caller AND assert the caller is another session (cross-session wake; `caller_session_key` is present). Validation fails if the caller is actually the channel user or a system source.
- `user` → the channel-user sink of your own session (only valid when your session is user-facing). Use this to reach your user directly when a non-user source (cron/heartbeat/another session) woke you.
- `subagent` / `fork` → spawn or wake a child thread.
- `session` → wake another session by key; the target's `dispatch(to=caller:session)` routes back to you and the exchange recurses until one side halts.

**How to pick between `caller:user` and `caller:session`:** read the wake YAML — if `caller_session_key` is present, use `caller:session`; if absent AND this session is user-facing, use `caller:user`. System sources (cron/heartbeat/compression) have no usable caller form — use `dispatch({})` to end silently, or `dispatch(to=user)` for user-facing sessions. The tool rejects mismatched assertions, so a wrong kind is a cheap validation error, not a silent misroute.

When you reply back to a cross-session caller — either explicitly via `dispatch(to=caller:session)` OR by emitting a naive final text response (both route through the same wake sink) — **prefix the body with a standalone line `> Re: "<excerpt>"` before the reply**. `<excerpt>` is up to **200 characters** taken from the incoming request body, with all newlines collapsed to single spaces. Do NOT just quote the first line — the first line is often a vague preamble with no information content; pull from across the message to capture the actual ask. The caller session may be juggling many concurrent threads and will not remember which outbound each inbound reply corresponds to; this excerpt is how it matches the reply back to the original request.

Some turns require silent completion — ending without user-facing output. The task prompt for that turn will specify this. The mechanism to complete silently is to call `dispatch({})` (empty sends). Any text in a final tool-free response will be delivered through the sink, so omit it when silent completion is required. When a cross-session wake arrives that you believe was misrouted, prefer `dispatch(to=caller:session)` with a short explanation over `dispatch({})` — silent drop hides the mistake from the caller.

When the most recent user message in history came from `sender: user`, the real human is usually still waiting. Append a `to=user` entry in `dispatch` to report progress or follow up — even if you are also routing work to a subagent, forwarding to another session, or replying to a non-user caller. `dispatch` takes an **array of sends**, so you can mix targets in one call: e.g. send a subagent off to do a task AND ping the user that you've started, in the same dispatch. A few shapes:

- `dispatch({sends: [{to: "user", body: "working on it, will follow up"}, {to: "subagent", agent: "search", task_id: "news-x", body: "Search for X"}]})` — answer the user AND spawn a helper in one turn.
- `dispatch({sends: [{to: "caller:user", body: "OK"}]})` — plain ack to the channel user who woke you.
- `dispatch({sends: [{to: "caller:session", body: "> Re: \"...\"\nDone."}]})` — reply to a cross-session waker.
- `dispatch({sends: [{to: "session", session_key: "telegram:12345", body: "report is ready"}, {to: "user", body: "sent the notice, done"}]})` — cross-session notify plus user progress report.
- `dispatch({})` — silent termination: no delivery, history recorded, and no further wake. Use this when a heartbeat/cron turn produced nothing worth saying, or when the task prompt explicitly asks for silent completion.

Each thread has a message queue. Wake messages are pushed into the queue, and the thread manager selects queued threads from all threads to run reasoning.

An `Agent` is a system-prompt template. `soul` is the prompt template used for user conversations. Other agents, such as `general`, are more specialized prompt templates. Some tasks, such as scheduled cleanup jobs, also have their own agent template files.

A `Skill` is essentially a context-compression mechanism. The prompt includes only a small set of skill names and short descriptions, and the LLM loads full details and guidance through the `use_skill` method.

In nagobot, the active model is always resolved through this chain: which Agent is configured for the session → which Specialty the Agent uses → which model and provider the Specialty specifies. For example, a Telegram session typically uses the `soul` Agent, which uses the `chat` specialty, and `chat` defaults to the default model — unless the specialty explicitly specifies one. When configured correctly, the model always fully leverages the specialty's capabilities.