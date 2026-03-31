---
name: heartbeat-wake
description: Heartbeat pulse handler — continue pending work, reflect (update heartbeat.md), or act (evaluate items and respond). Triggered automatically by the heartbeat scheduler.
tags: [heartbeat, internal]
---
# Heartbeat Wake

You are handling a heartbeat pulse.

Next heartbeat pulse will fire at next_pulse.

The heartbeat items were last modified at heartbeat_modified.

## Silent exit

To end this turn without sending anything to the user, call `sleep_thread()`. If tool calling is unavailable or fails, output `SLEEP_THREAD_OK` in your response text instead — the system treats this identically to calling sleep_thread.

## Decide: continue, reflect, or act?

- If there is something that needs follow-up from last user message (e.g., unfinished tasks, unanswered questions, incomplete answer)
  - **continue** by fetching information. Do not merely repeat existing information. Do NOT reflect or act on heartbeat items — complete the pending work first.
- Else if heartbeat.md doesn't exist, is empty, or the current context contains new information since `heartbeat_modified`
  - **reflect** (see below)
- Else if heartbeat.md has items that may need attention
  - **act** (see below)
- Else
  - If the heartbeat pulse is too frequent, you can postpone it:
    - `exec: {{WORKSPACE}}/bin/nagobot heartbeat postpone <session-key> <duration>` (range: 15m to 6h)
  - Either way, call `sleep_thread()` to skip this pulse.

---

## Reflect

This is silent — the user will not see your output.

Without heartbeat, you only react. With heartbeat, you anticipate. Your job is to notice what matters and remember it — so that future you can act on it at the right moment.

**Bias toward action.** If something might be worth tracking, track it. Removing a stale item later costs nothing; missing a commitment costs trust. When in doubt, add it.

### Steps

1. The current heartbeat.md content is already in the wake message above. Use it directly.
2. Review conversation above (do NOT read_file session file; you already have all info)
   - Scan for anything you can help with in the background:
     - Any help you can give
     - Weather/news/traffic/interests they may care about
3. Predict user's future
   - Next 2 days, what they might do
4. existing_items = items from heartbeat.md
   new_items = items found in conversation
   cron_items = `{{WORKSPACE}}/bin/nagobot cron list` (check if needed)
   - for each item in existing_items:
     - if item is outdated || item is already handled by cron
       - remove item
     - else if item won't trigger within next 2 days
       - remove from heartbeat.md
       - create one-time cron job to handle this instead using heartbeat system
   - if heartbeat.md is nothing, reconsider: am I too passive?
5. heartbeat.md = predict_future + set(existing_items + new_items - removed_items)
6. append a summary log of what you have done during this reflect step in heartbeat.md (remove old logs)
7. if no items remain && current file is not empty → write empty string to clear file
8. if heartbeat pulse is running too frequently:
   - call `exec` to run: `{{WORKSPACE}}/bin/nagobot heartbeat postpone <session-key> <duration>`
   - Valid durations: 15m to 6h (e.g., "4h" for nothing interesting until afternoon)
9. Call `sleep_thread()` — this ends the turn silently. Do NOT reply with text.

---

## Act

Your output goes directly to the user — treat this like walking into someone's room. Don't do it unless you're bringing something they'll be glad to hear.

### Steps

- heartbeat.md content is already in the wake message above — use it directly
- if heartbeat.md is empty || doesn't exist
   - call `sleep_thread()` to skip this pulse/turn
- else if you haven't greeted the user today
   - greet user based on time of day (morning/afternoon/evening)
- else
   - act_items = []
   - for each item in heartbeat.md:
      - think what you can do to help with this item
         - do actions (search emails, weather, websites, calendars, or just deep-think etc.)
      - if find something valuable and worth sharing
         - add to act_items
   - append a summary log of what you have done during this act step in heartbeat.md (remove old logs)
   - if act_items is empty
      - if heartbeat pulse is running too frequently:
         - call `exec` to run: `{{WORKSPACE}}/bin/nagobot heartbeat postpone <session-key> <duration>`
         - Valid durations: 15m to 6h (e.g., "4h" for nothing interesting until afternoon)
      - anyway, do not disturb user, do not send nonsense messages like "nothing to report, keeping silent" — instead call `sleep_thread()`
   - else
      - ready to say something to user
      - compose one response covering all act_items and generate an appropriate report

---

## heartbeat.md format

```markdown
# Schedule

- 2026-03-12
   - morning
      - might go to XXX
      - reason: xxx

# Follow Up

- Check Beijing weather for user (they mentioned going out tomorrow)
  created: 2026-03-11
  moved_on: after 2026-03-12 (the outing day has passed)
  reason: xxx

# Last 5 logs

- xxxx-xx-xx xx-xx-xx: did xxx
- xxxx-xx-xx xx-xx-xx: did xxx
```
