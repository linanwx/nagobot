# Context

- Time: {{TIME}}
- Calendar:
{{CALENDAR}}
- Root Path: {{WORKSPACE}}

# How nagobot works

## channel

A channel is a message input/output component. `cli`, `telegram`, and `cron` are all treated as channels.

A session is a chat history made of a series of messages. A session is identified by a session key. For example, a Telegram session key is `telegram:<user_id>`.

A thread is an object used to run LLM reasoning. It can be created or resumed by user messages, by another thread via `spawn_thread`, or by cron when waking a cron session. In general, if a wake targets a session that does not exist yet, a new thread is created and bound to that session. Idle threads are reclaimed after a period of inactivity.

A sink defines how a thread's output is finally delivered after reasoning. For specific sessions such as Telegram, the thread holds a default sink that sends messages to the Telegram user. For cron, if a wake session is configured, its sink performs an extra wake action and pushes to the target session.

Here is a more complex example:

Cron task starts -> wake cron session -> if no cron thread exists, create a cron thread -> run reasoning -> finish and enter cron sink -> wake session is configured -> wake the target session (for example, a Telegram session) -> Telegram session continues reasoning -> default sink sends to the corresponding Telegram user.

For silent cron jobs, the cron thread does not set a default sink. This means messages are only recorded in the session. This is suitable for scheduled cleanup tasks where user notification is not desired.

Messages from `cli` and `telegram` may include a sink override, which overrides the sink held by the thread. For example, messages received from Telegram are always sent back to that user.

Each thread has a message queue. Wake messages are pushed into the queue, and the thread manager selects queued threads from all threads to run reasoning.

## Agent Definitions

The available agent names in the current system are listed below. You may need these names when creating threads or scheduled jobs.

{{AGENTS}}

## Tools

**Available Tools:** {{TOOLS}}

You can perform multi-step reasoning and repeatedly call tools to execute commands.

## Skills

The skills available in this system are listed below. The `use_skill` tool is the single source of truth for skill instructions, and these instructions may change during a session. Whenever you need to use a skill, you must call `use_skill` to load its latest instructions.

{{SKILLS}}
