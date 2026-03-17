---
name: heartbeat-reflect
description: Heartbeat reflection protocol — review the current conversation, identify ongoing attention items, and update heartbeat.md. Triggered by the heartbeat system, not by users directly.
tags: [heartbeat, internal]
---
# Heartbeat Reflection

You are reflecting on this session. This is silent — the user will not see your output.

## Philosophy

Without heartbeat, you only react. With heartbeat, you anticipate. Your job is to notice what matters and remember it — so that future you can act on it at the right moment.

**Bias toward action.** If something might be worth tracking, track it. Removing a stale item later costs nothing; missing a commitment costs trust. When in doubt, add it.

## What to do

1. Read `{session_dir}/heartbeat.md` (path from wake frontmatter)
2. Look through the conversation above this message for anything worth ongoing attention:
   - Commitments, promises, deadlines
   - Recurring needs or interests
   - Time-sensitive events
   - Anything the user would appreciate you remembering
3. Update `heartbeat.md`:
   - **Add** new items you found
   - **Remove** items whose `moved_on` condition is met, or items older than 3 days that are no longer relevant
   - **Remove** items already handled by cron (run `{{WORKSPACE}}/bin/nagobot cron list` if unsure)
   - If nothing changed, still consider: did you look hard enough?
4. For items that won't trigger within the next 2 days:
   - Remove the item from `heartbeat.md` and create a corresponding cron job to handle it in the future
5. If no items remain, write empty string to clear the file (don't delete it)
6. Reply `HEARTBEAT_OK`

## Item format

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
  reason: user mentioned wanting to stay on top of emails

- Remind about quarterly report deadline
  if: talk about work or deadlines
  created: 2026-03-08
  moved_on: after 2026-03-20 (deadline passed) or user confirms submission
  reason: user mentioned a quarterly report due on March 20

- Greet user in the evening
  when: every night at 9 PM
  created: 2026-03-11
  moved_on: user asks to stop or shows no response for 3 days
  reason: user seems to activate in the evenings
```

Condition fields are free-form: `when`, `if`, `created`, `reason`, `moved_on`, etc. `moved_on` is required. No checkboxes — items exist or they don't.

## Rules

- Only touch `heartbeat.md`, no other files
- Items already handled by cron should be removed (e.g., "Summarize daily tech news" when a cron job already does this)
