---
name: heartbeat-wake
description: Heartbeat wake protocol — read heartbeat.md, evaluate which items are currently relevant, and act on them. Triggered by the heartbeat system, not by users directly.
tags: [heartbeat, internal]
---
# Heartbeat Wake

You have been woken by the heartbeat system to check your attention items and act on any that are currently relevant.

The session directory path is provided in the wake message (e.g. `Session directory: /path/to/sessions/telegram/12345`). Use it to locate `heartbeat.md`.

## Your Task

1. **Read `heartbeat.md`** from the session directory. If it doesn't exist or is empty, there's nothing to do — call `sleep_thread(skip=true)` and stop.

2. **Evaluate each item** against the current context:
   - What time is it now? Does the item's condition match?
   - Is the item still relevant given what's happened in the conversation?
   - Would acting on this item now be helpful and timely for the user?

3. **Act on relevant items** — use your available tools to fulfill the item's intent:
   - Check weather, search for information, read files, etc.
   - Compose a natural response to the user with the results
   - Your response will be delivered to the user through the normal sink

4. **If nothing worth reporting**, or acting now would disturb the user (e.g. sleeping hours, busy context), call `sleep_thread(skip=true)`.

## Decision Guidelines

- **Timing matters**: A "morning" item at 3 AM is not relevant yet. A "2026-03-14" item on March 12 is not due.
- **Don't repeat**: If you already acted on an item recently in this session, skip it unless circumstances changed.
- **Be natural**: When you do act, write as yourself — a natural continuation of the conversation, not a robotic "heartbeat triggered" announcement.
- **One response**: Combine all relevant items into a single cohesive response if multiple items are actionable.

## Important

- If nothing to do: `sleep_thread(skip=true)` — never send empty or pointless messages
- If something to do: respond naturally, then the system handles delivery
- Do NOT modify `heartbeat.md` — that's the reflection skill's job
