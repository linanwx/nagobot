---
name: heartbeat-wake
description: Heartbeat pulse handler — decide whether to reflect (update heartbeat.md) or act (evaluate items and respond). Triggered automatically by the heartbeat scheduler.
tags: [heartbeat, internal]
---
# Heartbeat Wake

You are handling a heartbeat pulse.

Next heartbeat pulse will fire at next_pulse.

The heartbeat items were last modified at heartbeat_modified.

## Decide: resume, reflect, act, or skip?

- If you have an unfinished task from a previous turn (e.g., a tool call was interrupted, a multi-step plan is partially done, or you explicitly noted something as "to be continued")
  - Resume and finish it now. Do NOT reflect or act on heartbeat items — complete the pending work first.
- Else if heartbeat.md doesn't exist, is empty, or the current context contains new information since `heartbeat_modified`
  - call `use_skill("heartbeat-reflect")`
- Else if heartbeat.md has items that may need attention
  - call `use_skill("heartbeat-act")`
- Else
  - If the heartbeat pulse is too frequent, you can postpone it:
    - `exec: nagobot heartbeat postpone <session-key> <duration>` (range: 15m to 6h)
  - Either way, call `sleep_thread()` to skip this pulse.
