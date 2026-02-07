---
name: manage_cron
description: Use this skill when you need to inspect, read, create, update, or delete scheduled cron jobs, including recurring and one-time tasks, under the project root path.
---
# Manage Cron via File Editing

Use this skill to manage scheduled jobs by editing `{{WORKSPACE}}/cron.yaml`.

## File Schema

`cron.yaml` is a YAML array of jobs:

```yaml
- id: daily-summary
  kind: cron
  expr: "0 9 * * *"
  task: "Prepare a daily operational summary from recent session activity. Include completed items, unresolved blockers, risk signals, and top priorities for the next cycle. Keep it concise, actionable, and reference key files or commands when relevant."
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
- `task`: Detailed instructions (prompt content) describing exactly what the child thread should do. For test scenarios, use explicit low-risk wording such as: "You are running a test task. Do not perform external actions. Output only: task completed." For non-test scenarios, include objective, scope, constraints, expected output format, and completion criteria. Keep the content sufficiently detailed (at least ~100 characters; around ~800 is recommended). Prompts that are too short can cause execution failure.
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

1. Check whether `{{WORKSPACE}}/cron.yaml` exists, and create it if it does not.
2. Edit cron jobs with any suitable tools; `creator_session_key` can be obtained from `health`.
   Using the `echo` command can quickly append new entries.
3. Call `health` to confirm the cron job appears in runtime status; if it does not, investigate and fix it.

## Examples

Add one recurring and one one-time job:

```yaml
- id: daily-summary
  kind: cron
  expr: "0 9 * * *"
  task: "Review recent execution logs and session updates, then produce a daily summary with three sections: (1) completed work, (2) pending actions, and (3) immediate next steps. Highlight blockers, owner assumptions, and any commands or files that require follow-up."
  agent: GENERAL
  creator_session_key: main
  silent: false
  enabled: true
  created_at: 2026-02-07T09:00:00Z

- id: one-shot-cleanup
  kind: at
  at_time: 2026-02-07T18:30:00+08:00
  task: "Run a one-time cleanup for stale temporary artifacts under the project root path. Remove only known temp outputs and cache leftovers, keep source files untouched, and finish with a short report that lists what was deleted and what was skipped."
  agent: GENERAL
  creator_session_key: main
  silent: true
  enabled: true
  created_at: 2026-02-07T10:00:00Z
```
