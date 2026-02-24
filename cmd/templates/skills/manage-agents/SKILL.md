---
name: manage-agents
description: Switch agent or set timezone for a session.
---
# Manage Session Configuration

## Switch Agent

Set the agent for a session:
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key> --agent <agent_name>
```

Clear the agent override (revert to default):
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key>
```

### Parameters

- `--session`: session key (required). Examples: `discord:123456`, `telegram:78910`, `cli`.
- `--agent`: agent template name from `agents/*.md`. Omit or empty to clear the override.

## Set Timezone

Set the IANA timezone for a session:
```
exec: {{WORKSPACE}}/bin/nagobot set-timezone --session <session_key> --timezone <iana_timezone>
```

Clear the timezone (revert to system default):
```
exec: {{WORKSPACE}}/bin/nagobot set-timezone --session <session_key>
```

### Parameters

- `--session`: session key (required). Examples: `discord:123456`, `telegram:78910`, `cli`.
- `--timezone`: IANA timezone name. Examples: `Asia/Shanghai`, `America/New_York`, `Europe/London`. Omit or empty to clear.

## Notes

- Changes take effect on the **next message** in that session (not the current turn).
- Changes persist across server restarts (saved to config.yaml).
- To see available agents, list files in `{{WORKSPACE}}/agents/`.
