---
name: manage_cron
description: Use this skill when you need to inspect, read, create, update, or delete scheduled cron jobs by editing cron.yaml directly in the workspace.
---
# Manage Cron via File Editing

Use this skill to manage scheduled jobs by editing `<workspace>/cron.yaml`.

## File Schema

`cron.yaml` is a YAML array of jobs:

```yaml
- id: daily-summary
  kind: cron
  expr: "0 9 * * *"
  task: "Write a daily summary from recent session activity."
  agent: GENERAL
  creator_session_key: main
  silent: false
  enabled: true
  created_at: 2026-02-07T09:00:00Z
```

Fields:
- `id`: unique job id.
- `kind`: `cron` or `at`.
- `expr`: required when `kind=cron`.
- `at_time`: required when `kind=at` (RFC3339 with timezone, e.g. `2026-02-07T15:04:05+08:00`).
- `task`: prompt text executed by the cron thread.
- `agent`: optional agent template name from `agents/*.md`.
- `creator_session_key`: session key to wake when `silent=false`.
- `silent`: `true` means no wake/push; `false` means wake creator session with result.
- `enabled`: enable/disable job.
- `created_at`: creation timestamp in RFC3339.

## Cron Expression Notes

For `kind=cron`, use standard 5-field cron:
- `min hour day month weekday`
- example: `0 9 * * *` (every day 09:00)

## Operating Procedure

1. Read current file first with `read_file`:
   - target: `<workspace>/cron.yaml`
2. If file is missing, create it as a YAML array.
3. Edit jobs in memory, then write full file with `write_file`.
4. Do not append partial fragments; always write a complete valid YAML document.
5. If `creator_session_key` is unknown, call `health` and use the current thread/session info.
6. After writing, wait for cron runtime reload (up to 60 seconds).

## Examples

Add one recurring and one one-time job:

```yaml
- id: daily-summary
  kind: cron
  expr: "0 9 * * *"
  task: "Summarize recent progress and pending actions."
  agent: GENERAL
  creator_session_key: main
  silent: false
  enabled: true
  created_at: 2026-02-07T09:00:00Z

- id: one-shot-cleanup
  kind: at
  at_time: 2026-02-07T18:30:00+08:00
  task: "Clean stale temporary files under workspace."
  agent: GENERAL
  creator_session_key: main
  silent: true
  enabled: true
  created_at: 2026-02-07T10:00:00Z
```
