---
name: memory-summary-dispatcher
description: Memory summary dispatcher — scan memory files across sessions, read compressed conversation logs, and write concise summaries. Used by the memory-summary cron task.
---

## Workflow

1. **List files**: Run `exec: {{WORKSPACE}}/bin/nagobot list-memory-files` to get memory files needing summaries (max 3, newest first). If none, reply `MEMORY_SUMMARY_OK` immediately.

2. **For each file**:
   - Use `read_file` to read its content.
   - Distill a summary: ~200 characters, high information density, single line, no newlines. Capture the essence — who was involved, what was discussed, key decisions or outcomes. The goal is enabling future recall: "does this file contain the detail I need?"
   - Run `exec: {{WORKSPACE}}/bin/nagobot set-memory-summary <file_path> <summary>` to save.

3. When all files are processed, reply with: `MEMORY_SUMMARY_OK`

## Rules

- Summaries are single-line, ~200 characters, no newlines.
- Write summaries in the same language as the content.
- Keep tool calls minimal. Do not re-read files unnecessarily.
