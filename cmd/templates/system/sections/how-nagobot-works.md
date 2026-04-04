---
name: how-nagobot-works
priority: 200
---
# How nagobot works

A channel is a message input/output component. `cli`, `telegram`, and `cron` are all treated as channels.

A session is a chat history made of a series of messages. A session is identified by a session key. For example, a Telegram session key is `telegram:<user_id>`.

A thread is an object used to run LLM reasoning. It can be created or resumed by user messages, by another thread via `spawn_thread`, or by cron when waking a cron session. In general, if a wake targets a session that does not exist yet, a new thread is created and bound to that session. Idle threads are reclaimed after a period of inactivity.

A sink defines how a thread's output is finally delivered after reasoning. For specific sessions such as Telegram, the thread holds a default sink that sends messages to the Telegram user. For cron, if a wake session is configured, its sink performs an extra wake action and pushes to the target session.

Here is a more complex example:

Cron task starts -> wake cron session -> if no cron thread exists, create a cron thread -> run reasoning -> finish and enter cron sink -> wake session is configured -> wake the target session (for example, a Telegram session) -> Telegram session continues reasoning -> default sink sends to the corresponding Telegram user.

For silent cron jobs, the cron thread does not set a default sink. This means messages are only recorded in the session. This is suitable for scheduled cleanup tasks where user notification is not desired.

Messages from `cli` and `telegram` may include a sink override, which overrides the sink held by the thread. For example, messages received from Telegram are always sent back to that user.

Each wake message carries YAML frontmatter with metadata about the current turn. Two fields connect the sink mechanism to your reasoning:

- `delivery` — a natural-language description of the sink's delivery target. This is your only way to know where your output will go. It may describe a user (`your response will be sent to telegram user 123`), an indirect chain (`your task will be injected into session telegram:789`), or no delivery at all (`cron silent, result will not be delivered`).
- `sender` — either `user` (the wake was triggered by a real user message) or `system` (triggered automatically by cron, heartbeat, child completion, etc.).

Some turns require silent completion — ending without user-facing output. The task prompt for that turn will specify this. The mechanism to complete silently is to call `sleep_thread()`, or when tool calling is unavailable, output the OK token designated by the task prompt. Any text in a final tool-free response will be delivered through the sink, so omit it when silent completion is required.

Each thread has a message queue. Wake messages are pushed into the queue, and the thread manager selects queued threads from all threads to run reasoning.

An `Agent` is a system-prompt template. `soul` is the prompt template used for user conversations. Other agents, such as `general`, are more specialized prompt templates. Some tasks, such as scheduled cleanup jobs, also have their own agent template files.

A `Skill` is essentially a context-compression mechanism. The prompt includes only a small set of skill names and short descriptions, and the LLM loads full details and guidance through the `use_skill` method.
