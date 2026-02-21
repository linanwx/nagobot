---
name: manage-agents
description: Switch or clear the agent assigned to a session.
---
# Manage Session Agents

## Switch Agent

Set the agent for a session:
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key> --agent <agent_name>
```

Clear the agent override (revert to default):
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key>
```

## Parameters

- `--session`: session key (required). Examples: `discord:123456`, `telegram:78910`, `cli`.
- `--agent`: agent template name from `agents/*.md`. Omit or empty to clear the override.

## Notes

- The change takes effect on the **next message** in that session (not the current turn).
- The change persists across server restarts (saved to config.yaml).
- To see available agents, list files in `{{WORKSPACE}}/agents/`.
