---
name: compress-context
description: Compress session context to free up token budget.
---
# Context Compression Skill

## Workflow

1. Determine `session_file`:
   - First choice: use the path from the Context Pressure Notice.
   - Fallback: `{{WORKSPACE}}/sessions/main/session.json`.
2. Write a compressed summary of the conversation so far (see guidance below) to `{{WORKSPACE}}/.tmp/compressed.txt` with `write_file`.
3. Run:
   ```
   {{WORKSPACE}}/bin/nagobot compress-session <session_file> {{WORKSPACE}}/.tmp/compressed.txt
   ```
4. Continue the original task.

## Compression Guidance

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
