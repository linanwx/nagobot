---
name: file-track
description: Organize this session's workspace files and (re)build file-track.md — the catalog of what files exist and when to use each. Archives stale/superseded/finished files into archive/, sorts the active ones into categories. Use after you create, change, or finish with files, or whenever the session dir gets cluttered and you want an up-to-date map of it.
---
# File Track

You keep working files in this session's workspace (`{{SESSIONDIR}}`). Over time they pile up: finished outputs, superseded drafts, one-off scratch files. This skill organizes them and writes/refreshes `{{SESSIONDIR}}/file-track.md` — a catalog the agent reads every turn (it is injected as the `file_track_section`), so it always knows what it has on disk and when to use each file.

## What you may touch vs. what you must NOT

**ONLY organize work files you (the agent) created** — reports, generated docs/HTML, exports, data files, drafts, scratch notes, downloaded media, etc.

**NEVER move, archive, rename, or delete these system files/dirs** (the runtime owns them):
- Files: `USER.md`, `session.jsonl`, `chat.jsonl`, `meta.json`, `dream.md`, `heartbeat.md`, `heartbeat_log.md`, `heartbeat_skip_log.md`, `file-track.md`
- Dirs: `memory/`, `threads/`, `fork/`, `prethink/`, `rephrase/`, `history/`, `archive/`

## Workflow

1. **List the workspace.**
   ```
   exec: ls -la {{SESSIONDIR}}
   ```
   Set aside the system files/dirs above. What's left is your work files.

2. **Judge each work file.** Read or recall what it is. Classify as:
   - **Active** — still relevant: referenced recently, an in-progress draft, a reusable input/output, or something the user may ask about again.
   - **Expired** — superseded by a newer version, a one-off output for a task that's clearly finished, or stale scratch no longer referenced.

3. **Archive the expired ones** (recoverable — move, never delete):
   ```
   exec: mkdir -p {{SESSIONDIR}}/archive && mv {{SESSIONDIR}}/<expired-file> {{SESSIONDIR}}/archive/
   ```
   When unsure, keep it active — archiving is reversible, but don't churn files that are obviously still in use.

4. **Write `{{SESSIONDIR}}/file-track.md`** with `write_file` (overwrite the whole file). Group the **active** files into sensible categories, and for each give: the filename, a one-line description of its content, and **when to use it**. End with a short note of what you archived this run. Use the language the user predominantly speaks. Suggested shape:

   ```markdown
   # File Track

   Files in this session's workspace and when to use each.

   ## <Category, e.g. Reports>
   - `report-q2.md` — Q2 sales analysis. Use when the user asks about Q2 numbers or wants to extend it.

   ## <Category, e.g. Drafts>
   - `proposal-draft.md` — in-progress client proposal. Use to continue that work.

   ## Archived this run
   - `old-report-q1.md` — superseded by Q2 → moved to archive/.
   ```

   If there are no work files at all, write a minimal `file-track.md` saying the workspace has no tracked work files yet.

## Rules

- NEVER touch the system files/dirs listed above — only your own work files.
- Archive by **moving** to `archive/` (recoverable); do not `rm`.
- `write_file` MUST overwrite `file-track.md` completely — it is the single current catalog, not an append log.
- Keep descriptions concrete and the "when to use" actionable — this file is read on every turn, so make it earn its tokens.
