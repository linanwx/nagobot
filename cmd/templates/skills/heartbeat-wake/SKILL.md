---
name: heartbeat-wake
description: Heartbeat pulse handler — decide whether to reflect (update heartbeat.md) or act (evaluate items and respond). Triggered automatically by the heartbeat scheduler.
tags: [heartbeat, internal]
---
# Heartbeat Wake

You are handling a heartbeat pulse. The wake message contains heartbeat.md content, `heartbeat_modified` (last update time), and `next_pulse` (next automatic pulse time).

## Decide: reflect or act?

- If heartbeat.md doesn't exist, is empty, or the context contains new information since `heartbeat_modified` → call `use_skill("heartbeat-reflect")`
- Otherwise → call `use_skill("heartbeat-act")`

The scheduler will fire another pulse in 10 minutes after reflect.
