---
name: context-ops
description: Manage this session's own conversation context — compress the chat history to free up token budget, or clear it to start a fresh conversation. Use when the context window is filling up, the session has grown too long, token pressure is high, or the user wants to wipe the history and start over. This acts on the LLM context window itself, not on a document or passage the user pasted.
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
   The command replaces the session file with only the tail messages and prints your summary back in the output. This summary becomes the permanent context anchor — it will not be trimmed by the system.
5. Reply with COMPRESS_OK after seeing the successful result. Then continue the original task if any.

### Compression Guidance

Write a summary whose purpose is to provide continuity so you can continue making progress in a future context, where the raw conversation history will be replaced with this summary.

Write in the same language as the original conversation. Use plain text, not JSON. Do only the write + compress-session steps for this skill — do not explore other files or start unrelated work.

Use this fixed structure, in order. Keep a section even if brief; never drop one:

1. **Primary intent** — what the user is ultimately trying to achieve this session.
2. **Decisions & constraints** — choices already made and rules to keep following. Copy any security, credential-handling, or "never do X" instructions **word-for-word** — these must survive verbatim so they keep applying after compaction.
3. **Files & paths** — each file/path/ID/command/config value touched, with a one-line note on why it matters. Paths must survive the summary.
4. **Errors & fixes** — mistakes made and how they were resolved, so they are not repeated.
5. **All user messages** — list every user message (not tool results), verbatim or near-verbatim. These capture intent and changing direction and cannot be reconstructed; do not paraphrase them away.
6. **Current work** — what is completed and what is in progress right now.
7. **Next step** — the immediate next action, **quoting the user's most recent instruction verbatim** to avoid drift. Do not invent tangential next steps the user did not ask for.

Discard (these never go in the summary):
- Verbose tool-call arguments and raw tool outputs — keep only outcomes.
- Intermediate debugging steps that were already resolved.
- Repetitive or redundant exchanges that don't affect future work.

Scale to the conversation: if it is long, aim for at least 1,000 words across the sections; if short, roughly 10% of the current conversation length — but always keep sections 2, 5, and 7 complete regardless of length.

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
