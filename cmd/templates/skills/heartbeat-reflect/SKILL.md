---
name: heartbeat-reflect
description: Heartbeat reflection protocol — review the current conversation, identify ongoing attention items, and update heartbeat.md. Triggered by the heartbeat system, not by users directly.
tags: [heartbeat, internal]
---
# Heartbeat Reflection

You have been woken by the heartbeat system to reflect on this session. Review the conversation history and maintain a list of ongoing attention items in `heartbeat.md`.

The `session_dir` field in the wake message YAML frontmatter contains the session directory path. `heartbeat.md` is located at `{session_dir}/heartbeat.md`.

**This wake is always silent — your output will NOT be sent to the user.**

## Your Task

1. **Review the conversation** — look at recent exchanges for things worth keeping an eye on:
   - Commitments you or the user made ("I'll check on X", "remind me about Y")
   - Recurring needs (weather checks, email summaries, periodic reports)
   - Time-sensitive items (deadlines, appointments, scheduled events)
   - Anything the user might appreciate proactive follow-up on

2. **Read `heartbeat.md`** from the session directory. If it doesn't exist or is empty, that's fine — it means there are no current attention items.

3. **Decide what should change** — compare the conversation with `heartbeat.md`:
   - **Add** new items from the conversation. Every item MUST have a `moved_on` field.
   - **Remove** items whose `moved_on` condition is met
   - **Remove** items duplicated by cron jobs. If unsure, run `{{WORKSPACE}}/bin/nagobot cron list` to check.
   - **Remove** items that are irrelevant in the current context and were created 3 or more days ago
   - **Keep** items whose `moved_on` condition is not yet met

4. **Update `heartbeat.md`** if anything changes, otherwise skip update.
   - If no items remain, write an empty string to clear the file — do NOT leave behind headings, comments, or any other text. Do NOT delete the file.

5. Reply with `HEARTBEAT_OK`.

## heartbeat.md Format

Each item is a brief description of what to monitor, with condition fields:

```markdown
- Check Beijing weather for user (they mentioned going out tomorrow)
  when: 2026-03-12 morning
  created: 2026-03-11
  moved_on: after 2026-03-12 (the outing day has passed)
  reason: user mentioned going out tomorrow, might be helpful to proactively check weather

- Periodically check unread emails and summarize
  when: anytime
  created: 2026-03-10
  moved_on: user hasn't mentioned emails for over a week
  reason: user mentioned wanting to stay on top of emails, could be helpful to provide regular summaries

- Remind about quarterly report deadline
  if: talk about work or deadlines
  created: 2026-03-08
  moved_on: after 2026-03-20 (deadline passed) or user confirms submission
  reason: user mentioned a quarterly report due on March 20, a reminder when discussing work could be helpful

- Greet user in the evening
  when: every night at 9 PM
  created: 2026-03-11
  moved_on: user asks to stop or shows no response for 3 days
  reason: user seems to activate in the evenings, a friendly greeting could be a nice touch
```

Example of removing a duplicate: if you find an item like "Summarize daily tech news every morning" in heartbeat.md, but the conversation history shows cron wakes already doing `push daily tech news summary every morning`, remove it from heartbeat.md — the cron system already handles it.

Rules:
- Items are ongoing attention items, not one-time tasks
- No `[ ]` / `[x]` checkboxes — items are either present (active) or removed
- Condition fields are free-form: `when`, `if`, `created`, `reason`, `moved_on`, etc.
- **`moved_on` is required** — describes when to remove this item (date passed, user lost interest, condition fulfilled). Evaluate during each reflection.
- Keep descriptions concise

## Important

- Do NOT create or modify any file other than `heartbeat.md`
- Be conservative: only add items that genuinely warrant ongoing attention
