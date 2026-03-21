---
name: context-ops
description: Manage session context — compress to free token budget, or clear to start fresh. Use when context pressure is high, the session is too long, or the user wants a fresh start.
---
# Context Operations

## Compress Context

Compress the current session to free up token budget while preserving continuity.

### Workflow

1. Determine `session_file`:
   - First choice: use the path from the Context Pressure Notice.
   - Fallback: `{{WORKSPACE}}/sessions/cli/session.jsonl`.
2. Generate a unique temp file path: `{{WORKSPACE}}/.tmp/compressed-<UNIQUE>.txt` where `<UNIQUE>` is derived from the session key (replace `:` and `/` with `-`). This prevents race conditions when multiple sessions compress concurrently.
3. Write a compressed summary of the conversation so far (see guidance below) to the unique temp file with `write_file`.
4. Run:
   ```
   {{WORKSPACE}}/bin/nagobot compress-session <session_file> <temp_file>
   ```
5. Continue the original task.

### Compression Guidance

Write a summary whose purpose is to provide continuity so you can continue making progress in a future context, where the raw conversation history will be replaced with this summary.

Write in the same language as the original conversation. Use plain text, not JSON.

Include:
- Current state of work: what has been completed, what is in progress.
- Next steps and pending actions.
- Key decisions, preferences, and constraints the user expressed.
- Important learnings: mistakes that were fixed, insights discovered.
- Critical references: IDs, file paths, commands, configuration values.

Discard:
- Verbose tool call arguments and raw tool outputs — keep only outcomes.
- Intermediate debugging steps that were already resolved.
- Repetitive or redundant exchanges that don't affect future work.

The longer and more detailed, the better. If the current conversation is long, aim for at least 1,000 words. If the conversation is relatively short, you may target roughly 10% of the current conversation length.

---

## Clear Context

Reset the session to start completely fresh.

### Workflow

1. Determine `session_file`:
   - First choice: use the path from the Context Pressure Notice.
   - Fallback: `{{WORKSPACE}}/sessions/cli/session.jsonl`.
2. Run:
   ```
   exec: {{WORKSPACE}}/bin/nagobot compress-session --clear <session_file>
   ```
3. Confirm to the user that the session has been reset.
