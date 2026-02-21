---
name: clear-context
description: Clear session context to start fresh.
---
# Clear Session Context

## Workflow

1. Determine `session_file`:
   - First choice: use the path from the Context Pressure Notice.
   - Fallback: `{{WORKSPACE}}/sessions/cli/session.json`.
2. Write `Context cleared.` to `{{WORKSPACE}}/.tmp/compressed.txt` with `write_file`.
3. Run:
   ```
   exec: {{WORKSPACE}}/bin/nagobot compress-session <session_file> {{WORKSPACE}}/.tmp/compressed.txt
   ```
4. Confirm to the user that the session has been reset.
