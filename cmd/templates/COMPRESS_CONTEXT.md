---
name: compress_context
description: Compress session context with simple backup and atomic replace.
---
# Context Compression Skill

Required workflow:
1. Determine `session_file`:
   - First choice: use the runtime notice value.
   - Fallback: `{{WORKSPACE}}/sessions/main/session.json` (`{{WORKSPACE}}` is the configured project root path).
   - If neither exists, stop and ask for an explicit path.
2. Count lines and read content if needed.
3. Set `session_dir = dirname(session_file)`.
4. Generate `timestamp` in format `<unix>_<local-time>`, where:
   - `<unix>` is Unix seconds (for global monotonic ordering),
   - `<local-time>` is local timezone time `YYYYMMDDTHHMMSS±ZZZZ` (for readability),
   - example: `1738926930_20260207T191530+0800`.
5. Backup original file to `<session_dir>/history/<timestamp>.json`.
6. Write compressed result to `<session_dir>/.tmp/session.next.json`.
7. Validate temp file:
   - valid JSON
   - has `key`, `messages`, `created_at`, `updated_at`
   - `messages` is an array
8. If validation passes, atomically replace `<session_dir>/session.json` with `session.next.json`.
9. If replacement fails, delete leftover `session.next.json`.
10. Continue the original task.

Compression content guidance:
- Keep top-level structure unchanged.
- Summarize in the same language as the original conversation.
- Preserve high-value context only:
  - user preferences and constraints
  - active goals and decisions
  - unresolved issues and pending actions
  - critical IDs, paths, commands, and references
