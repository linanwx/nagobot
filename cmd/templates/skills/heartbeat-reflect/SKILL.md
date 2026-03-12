---
name: heartbeat-reflect
description: Heartbeat reflection protocol — review the current conversation, identify ongoing attention items, and update heartbeat.md. Triggered by the heartbeat system, not by users directly.
tags: [heartbeat, internal]
---
# Heartbeat Reflection

You have been woken by the heartbeat system to reflect on this session. Review the conversation history and maintain a list of ongoing attention items in `heartbeat.md`.

The session directory path is provided in the wake message (e.g. `Session directory: /path/to/sessions/telegram/12345`). Use it to locate `heartbeat.md`.

**This wake is always silent — your output will NOT be sent to the user.**

## Your Task

1. **Review the conversation** — look at recent exchanges for things worth keeping an eye on:
   - Commitments you or the user made ("I'll check on X", "remind me about Y")
   - Recurring needs (weather checks, email summaries, periodic reports)
   - Time-sensitive items (deadlines, appointments, scheduled events)
   - Anything the user might appreciate proactive follow-up on

2. **Read `heartbeat.md`** from the session directory. If it doesn't exist or is empty, that's fine — it means there are no current attention items.

3. **Update `heartbeat.md`** based on your review:
   - **Add** new items you identified from the conversation
   - **Remove** items that are no longer relevant (resolved, outdated, user moved on)
   - **Keep** items that are still active and worth monitoring
   - If no items remain, write an empty string to clear the file — do NOT leave behind headings, comments, or any other text. Do NOT delete the file.

4. Reply with `HEARTBEAT_OK`.

## heartbeat.md Format

Each item is a brief description of what to monitor, with an optional condition line:

```markdown
- Check Beijing weather for user (they mentioned going out tomorrow)
  when: 2026-03-12 morning
  reason: user mentioned going out tomorrow, might be helpful to proactively check weather

- Periodically check unread emails and summarize
  when: anytime
  reason: user mentioned wanting to stay on top of emails, could be helpful to provide regular summaries

- Remind about quarterly report deadline
  if: talk about work or deadlines
  reason: user mentioned a quarterly report due on March 20, a reminder when discussing work could be helpful

- Greet user in the evening
  when: every night at 9 PM
  reason: user seems to activate in the evenings, a friendly greeting could be a nice touch
```

Rules:
- Items are **ongoing attention/monitoring items**, not one-time tasks
- No `[ ]` / `[x]` checkboxes — items are either present (active) or deleted (resolved)
- Condition fields are free-form: `when`, `where`, `if`, `reason`, or any other condition the LLM can interpret
- Keep descriptions concise but informative

## Important

- Do NOT create or modify any file other than `heartbeat.md`
- Be conservative: only add items that genuinely warrant ongoing attention
