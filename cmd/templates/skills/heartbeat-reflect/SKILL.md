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

## Silent exit

To end this turn without sending anything to the user, call `sleep_thread()`. If tool calling is unavailable or fails, output `SLEEP_THREAD_OK` in your response text instead — the system treats this identically to calling sleep_thread.

## What to do

1. The current heartbeat.md content is already in the wake message above. Use it directly.
2. Review conversation above (do NOT read_file session file; you already have all info)
   - Scan for anything you can help with in the background:
     - Any help you can give
     - Weather/news/traffic/interests they may care about
3. Predict user's future
   - Next 2 days, what they might do
4. existing_items = items from heartbeat.md
   new_items = items found in conversation
   cron_items = `nagobot cron list` (check if needed)
   - for each item in existing_items:
     - if item is outdated || item is already handled by cron
       - remove item
     - else if item won't trigger within next 2 days
       - remove from heartbeat.md
       - create one time cron job to handle this instead using heartbeat system
   - if heartbeat.md is nothing, reconsider: am I too passive?
5. heartbeat.md = predict_future + set(existing_items + new_items - removed_items)
6. if no items remain && current file is not empty → write empty string to clear file
7. if heartbeat pause is running too frequently:
   - call `exec` to run: `nagobot heartbeat postpone <this session-key> <duration>`
   - Valid durations: 15m to 6h (e.g., "4h" for nothing interesting until afternoon)
8. Call `sleep_thread()` — this ends the turn silently. Do NOT reply with text.

## Item format

```markdown
# Schedule

- 2026-03-12
   - morning
      - might go to XXX
      - reason: xxx
- 2026-03-13
   - afternoon
      - might work
      - reason: xxx

# Follow Up

- Check Beijing weather for user (they mentioned going out tomorrow)
  created: 2026-03-11
  moved_on: after 2026-03-12 (the outing day has passed)
  reason: xxx

- Periodically check unread emails
  created: 2026-03-10
  moved_on: user hasn't mentioned emails for over a week
  reason: xxx
```
