---
name: heartbeat-wake
description: Heartbeat pulse handler — decide whether to reflect (update heartbeat.md) or act (evaluate items and respond). Triggered automatically by the heartbeat scheduler.
tags: [heartbeat, internal]
---
# Heartbeat Wake

You are handling a heartbeat pulse. The wake message contains heartbeat.md content, `heartbeat_modified` (last update time), and `next_pulse` (next automatic pulse time).

## Decide: reflect, act, or skip?

- If heartbeat.md doesn't exist, is empty, or the context contains new information since `heartbeat_modified` → call `use_skill("heartbeat-reflect")`
- If heartbeat.md has items that may need attention → call `use_skill("heartbeat-act")`
- If nothing to do (no new info, no actionable items) → call `sleep_thread()` to skip this pulse. If this keeps repeating, postpone: `exec: nagobot heartbeat postpone <session-key> <duration>` (session key from wake frontmatter `session:` field, duration 15m-6h) then `sleep_thread()`

The scheduler will fire another pulse in 10 minutes after reflect.
