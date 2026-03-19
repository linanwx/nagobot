---
name: heartbeat-wake
description: Heartbeat pulse handler — decide whether to reflect (update heartbeat.md) or act (evaluate items and respond). Triggered automatically by the heartbeat scheduler.
tags: [heartbeat, internal]
---
# Heartbeat Wake

You are handling a heartbeat pulse for this session.

## Philosophy

Without heartbeat, you only respond when spoken to. With heartbeat, you anticipate. Your job is to notice the right moment and bring something the user will be glad to hear — or stay silent if there's nothing to say.

## What to do

1. Read the wake message. It contains:
   - `heartbeat_modified`: when heartbeat.md was last updated
   - `next_pulse`: when the next automatic pulse will fire
   - Current heartbeat.md content (or "heartbeat pulse triggered" if empty)

2. **Decide: reflect or act?**
   - If heartbeat.md doesn't exist, is empty, or the conversation contains significant new information since `heartbeat_modified` → **reflect**
   - Otherwise → **act**

3. **If reflecting:**
   - call `use_skill("heartbeat-reflect")` and follow its instructions
   - After reflect completes, you MUST call `sleep_thread()` to end silently
   - The scheduler will automatically fire another pulse in 10 minutes for act

4. **If acting:**
   - call `use_skill("heartbeat-act")` and follow its instructions

## Important

- `sleep_thread()` in heartbeat context takes NO parameters. Just call it to suppress output and end the turn.
- The heartbeat scheduler fires the next pulse automatically — you do NOT need to schedule anything.
- To postpone, use the CLI command (nagobot is on PATH), not sleep_thread duration.
- Do NOT wake just to say nothing. If no items are relevant, end silently.
