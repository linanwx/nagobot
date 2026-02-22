---
name: clear-context
description: Clear session context to start fresh.
---
# Clear Session Context

## Workflow

1. Determine `session_file`:
   - First choice: use the path from the Context Pressure Notice.
   - Fallback: `{{WORKSPACE}}/sessions/cli/session.json`.
2. Run:
   ```
   exec: {{WORKSPACE}}/bin/nagobot compress-session --clear <session_file>
   ```
3. Confirm to the user that the session has been reset.
