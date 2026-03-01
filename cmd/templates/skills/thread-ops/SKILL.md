---
name: thread-ops
description: Use when you need to interact with other threads or manage thread lifecycle. Covers spawning child threads for parallel subtasks, waking other sessions to greet/remind/challenge/delegate, sleeping to suppress output or schedule delayed follow-ups, checking thread status, and running health diagnostics across all active threads.
tags: [thread, internal, orchestration]
---
# Thread Operations

Threads are execution units that bind a session to an agent. Each thread has an inbox (wake message queue), runs one turn at a time, and delivers output to a sink (Telegram, Discord, etc.).

## Tools Reference

### spawn_thread

Create a child thread for a delegated subtask. Asynchronous — returns immediately.

```
tool_call: spawn_thread(task="<instruction for child LLM>", agent="<agent_name>")
```

- `task` (required): Instruction for the child thread's LLM. Write as an AI-to-AI instruction, not user-facing text.
- `agent` (optional): Agent template name. Omit to use session default.
- Returns: child thread ID.
- The child runs independently with its own context. When done, this thread receives a `child_completed` wake message automatically.

**When to use:** Parallel subtasks, delegating to a specialized agent (e.g. `imagereader`), long-running work that shouldn't block the current response.

### check_thread

Inspect a thread's status.

```
tool_call: check_thread(thread_id="<id>")
```

- Returns: thread metadata — ID, session key, agent name, state (idle/running/pending), pending message count, execution metrics.
- Use after `spawn_thread` to check child progress, or with any thread ID from `health`.

### sleep_thread

Suppress output and optionally schedule a delayed wake-up.

```
tool_call: sleep_thread(duration="30m", message="Check back on user", skip=false)
```

- `duration` (optional): Go duration format — `"30m"`, `"2h"`, `"1h30m"`. Default `"2m"`, max `24h`.
- `message` (optional): Memo included when waking up. Default: "Sleep timer expired."
- `skip` (optional): `true` = suppress output only, no scheduled wake. Use when the message is not directed at you.

**Two modes:**
1. **Duration mode** (`skip=false`): Suppress output + schedule wake after duration. Use for "come back later" scenarios.
2. **Skip mode** (`skip=true`): Suppress output only, no wake scheduled. Use when ignoring irrelevant messages in group chats.

### wake_thread

Wake another thread by session key with an injected message. This is a versatile multi-purpose tool — use it to:

- **Greet**: Send a warm greeting to a user session.
- **Remind**: Nudge a thread about unfulfilled commitments.
- **Challenge**: Question or correct a thread's behavior or output.
- **Inquire**: Ask a thread for information or status updates.
- **Delegate**: Assign a task to another session's thread.
- **Coordinate**: Facilitate cross-thread collaboration and data exchange.

```
tool_call: wake_thread(session_key="telegram:12345", message="<instruction for target LLM>")
```

- `session_key` (required): Target session (e.g. `telegram:12345`, `discord:67890`, `cli`). A thread is auto-created if needed.
- `message` (required): Instruction for the target thread's LLM. Write as an AI-to-AI instruction, not user-facing text.
- The target thread runs reasoning and may deliver output to the user via its sink.

### health

List all active threads and system status.

```
tool_call: health()
```

- Returns `all_threads`: list of every active thread with ID, session key, agent, state, pending count, last activity.
- Also returns provider info, session stats, cron jobs, channel config, memory usage.

## Common Patterns

### Delegate and wait
```
1. spawn_thread(agent="researcher", task="Find information about X")
2. Finish your current response normally
3. You will be auto-woken with child_completed containing the result
```

### Ignore irrelevant message
```
sleep_thread(skip=true)
→ Output suppressed, no wake scheduled
```

### Scheduled check-in
```
sleep_thread(duration="1h", message="Check if user responded")
→ Output suppressed, woken in 1 hour
```

### Cross-session notification
```
wake_thread(session_key="telegram:12345", message="Notify user that the report is ready")
→ Target thread runs and delivers message to user
```
