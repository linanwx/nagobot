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
2. Review conversation above (do NOT read_file session file; you already have all info)
   - Scan for: commitments, deadlines, recurring needs, time-sensitive events, advice, user concerns, anything user would appreciate remembering
3. existing_items = items from heartbeat.md
   new_items = items found in conversation
   cron_items = `{{WORKSPACE}}/bin/nagobot cron list` (check if needed)
   - for each item in existing_items:
      - if item.moved_on condition is met || (item.created older than 3 days && item not mentioned in conversation) || item is already handled by cron
         - remove item
      - else if item won't trigger within next 2 days
         - remove from heartbeat.md
         - create cron job to add it back later
   - for each item in new_items:
      - if item not already in existing_items
         - add item
   - for each item in existing_items:
      - if item.when/if is a chat trigger (e.g., "when user mentions about X, do Y")
         - try to fix these chat-condition triggers to be time-based or resource-based triggers
            - catch: if unfixable → remove item
   - if nothing fixed, reconsider: am I too passive?

4. if no items remain && current file is not empty → write empty string to clear file (don't delete)
5. Reply `HEARTBEAT_OK`



## Item format

```markdown
- Check Beijing weather for user (they mentioned going out tomorrow)
  when/if: 2026-03-12 morning
  created: 2026-03-11
  moved_on: after 2026-03-12 (the outing day has passed)
  reason: user mentioned going out tomorrow, might be helpful to proactively check weather

- Periodically check unread emails and summarize
  when/if: daytime
  created: 2026-03-10
  moved_on: user hasn't mentioned emails for over a week
  reason: user mentioned wanting to stay on top of emails

- Remind about quarterly report deadline
  when/if: on the morning two days before deadlines
  created: 2026-03-08
  moved_on: after 2026-03-20 (deadline passed) or user confirms submission
  reason: user mentioned a quarterly report due on March 20

- Remind user to bring an umbrella
  when/if: bad weather forecast and user might go out
  created: 2026-03-11
  moved_on: user hasn't mentioned outings recently
  reason: user seems to activate in the evenings
```

Condition fields are free-form: `when`, `if`, `created`, `reason`, `moved_on`, etc. `moved_on` is required. No checkboxes — items exist or they don't.

when/if is NOT a hook or chat trigger — it does not mean "when the user talks about X, take action." It means: reviewing the conversation from a few hours ago, or the current time meets the criteria, or proactively searching relevant resources — then take action.

## Rules

- Only touch `heartbeat.md`, no other files
- Items already handled by cron should be removed (e.g., "Summarize daily tech news" when a cron job already does this)
