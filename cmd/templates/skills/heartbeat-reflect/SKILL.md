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

1. The current heartbeat.md content is already in the wake message above. Use it directly.
2. Review conversation above (do NOT read_file session file; you already have all info)
   - Scan for: anything user would appreciate further actions
3. existing_items = items from heartbeat.md
   new_items = items found in conversation
   cron_items = `nagobot cron list` (check if needed)
   - for each item in existing_items:
      - if item.moved_on condition is met || (item.created older than 3 days && item not mentioned in conversation) || item is already handled by cron
         - remove item
      - else if item won't trigger within next 2 days
         - remove from heartbeat.md
         - create one time cron job to handle this instead using heartbeat system
   - heartbeat.md = set(existing_items + new_items - removed_items)
   - if heartbeat.md is nothing, reconsider: am I too passive?
   - if heartbeat pause is running too frequently:
      - call `exec` to run: `nagobot heartbeat postpone <this session-key> <duration>`
      - Valid durations: 15m to 6h (e.g., "4h" for nothing interesting until afternoon)

4. if no items remain && current file is not empty → write empty string to clear file (don't delete)
5. Call `sleep_thread()` — this ends the turn silently. Do NOT reply with text.



## Item format

```markdown
- Check Beijing weather for user (they mentioned going out tomorrow)
  created: 2026-03-11
  moved_on: after 2026-03-12 (the outing day has passed)
  reason: user mentioned going out tomorrow, might be helpful to proactively check weather

- Periodically check unread emails and summarize
  created: 2026-03-10
  moved_on: user hasn't mentioned emails for over a week
  reason: user mentioned wanting to stay on top of emails

- Remind about quarterly report deadline
  created: 2026-03-08
  moved_on: after 2026-03-20 (deadline passed) or user confirms submission
  reason: user mentioned a quarterly report due on March 20

- Remind user to bring an umbrella
  created: 2026-03-11
  moved_on: user hasn't mentioned outings recently
  reason: user seems to activate in the evenings
```

## Rules

- Only touch `heartbeat.md`, no other files
- Items already handled by cron should be removed (e.g., "Summarize daily tech news" when a cron job already does this)
