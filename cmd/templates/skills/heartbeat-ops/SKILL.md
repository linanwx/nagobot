---
name: heartbeat-ops
description: Use when you need to trigger heartbeat reflection or wake for a user session. Reflection reviews the conversation and updates heartbeat.md; wake reads heartbeat.md and acts on relevant items.
tags: [heartbeat, internal]
---
# Heartbeat Operations

CLI commands for triggering heartbeat actions on user sessions. Both commands communicate with the running serve process via RPC.

## heartbeat reflect

Trigger heartbeat reflection for a session. The session's agent will review the conversation, identify ongoing attention items, and update heartbeat.md.

```
exec: {{WORKSPACE}}/bin/nagobot heartbeat reflect <session-key>
```

- `<session-key>`: Target session (e.g. `telegram:12345`, `discord:67890`)

## heartbeat wake

Trigger heartbeat wake for a session. The session's agent will read heartbeat.md, decide which items are currently relevant, and act on them.

```
exec: {{WORKSPACE}}/bin/nagobot heartbeat wake <session-key>
```

- `<session-key>`: Target session (e.g. `telegram:12345`, `discord:67890`)
