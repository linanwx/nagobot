---
name: session-files
priority: 600
---
# Files in your session directory

Most of what is on disk is runtime machinery, not knowledge to re-read. Sort every entry into one of three groups.

**Already in this prompt — but read it before you edit it.** `USER.md`, `dream.md`, `file-track.md` and `heartbeat.md` are injected in full every turn, and `memory/` contributes one `summary` line per file, so opening one with `read_file` just to KNOW what it says wastes context. The injected copy is whitespace-trimmed and wrapped in a header, so it is NOT the file's bytes: before any `edit_file` on one of them, `read_file` it in the same turn and copy `old_text` out of that output — never from the copy in this prompt, never from memory. `file-track.md` is also your routing table: consult the copy in your prompt to pick a working file instead of listing the directory.

**Read on demand.** Your own working `.md` files, and a specific `memory/<date>.md` when you need the detail behind its summary. `archive/` or `archived/` only when the user asks about retired content.

**Never read.** `session.jsonl` (the conversation already IS your context), `chat.jsonl`, `meta.json`, `channel.json`, `history/`, `threads/`, `rephrase/`, `imagepreview/`, `audiopreview/`, and the heartbeat logs.

Your `memory_index` covers only THIS session, only summarized files, only the most recent 20 — so its silence is **not** evidence that something was never said. To recall anything older, or from another session, `grep` across all sessions' `memory/*.md` before concluding it does not exist. The `session-ops` skill has the recipes, including how to recover one old message verbatim from `history/`.
