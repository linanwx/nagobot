---
name: dream
description: Nighttime dreaming. Reviews the past 24 hours of this session's conversation, reflects and associates freely, then overwrites dream.md with the result. Triggered by the heartbeat scheduler at night (should_dream=true) — never call directly.
---
# Dream

The heartbeat scheduler decided this session should dream. It is night and the user has been quiet for a while. Look back over the past 24 hours of conversation, reflect and associate freely, organize what matters, and write a fresh dream into `dream.md`.

This is a BACKGROUND task. You will NOT message the user.

## Workflow

1. **Review the past 24 hours.** The recent conversation is already in your context — use the timestamps to scope to the **last 24 hours** and ignore anything older. (The full session log is at `{{SESSIONDIR}}/session.jsonl` if you need more than what is in context.)

2. **Reflect and associate** over that 24-hour window. This is a dream, not a status report — let it be reflective and connective, but stay grounded in what actually happened. Cover:
   - **Data worth keeping**: useful links, commands, decisions, facts, numbers.
   - **Unfinished work**: threads left open, things you promised, things to follow up on.
   - **Conversation insights**: what the user actually wanted underneath the literal request; recurring patterns; corrections; how to serve them better next time.
   - **Associations**: connect today's themes to each other and to what you already know about this user.

3. **Overwrite `dream.md`.** Use `write_file` to write `{{SESSIONDIR}}/dream.md`.
   - **You MUST overwrite the entire file.** Replace the previous `dream.md` completely — do NOT append to or merge with the old dream. Each night's dream fully replaces the last one.
   - Write in the language the user predominantly uses in conversation.
   - Keep it focused — one coherent reflection, not a transcript. Aim for something that will genuinely help future-you understand and serve this user.

4. **End silently.** Call `dispatch({})` with empty sends. Produce NO user-facing output.

## Rules

- BACKGROUND task — NEVER send messages to the user.
- ALWAYS overwrite `dream.md` completely; never append to the previous dream.
- If the past 24 hours hold nothing meaningful (e.g. no real conversation), skip the write and go straight to `dispatch({})`.
- MUST terminate with `dispatch({})` — silent termination.
