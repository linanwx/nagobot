---
name: manage-cron
description: Manage scheduled cron jobs (create, update, remove, list).
---
# Manage Cron Jobs

## Workflow

1. **Add/update a recurring job**:
   ```
   exec: {{WORKSPACE}}/bin/nagobot cron set-cron --id <id> --expr "<cron-expr>" --task "<task>" [--agent <name>] [--wake-session <key>] [--silent]
   ```
2. **Add/update a one-time job**:
   ```
   exec: {{WORKSPACE}}/bin/nagobot cron set-at --id <id> --at "<RFC3339>" --task "<task>" [--agent <name>] [--wake-session <key>] [--silent]
   ```
3. **Remove jobs**:
   ```
   exec: {{WORKSPACE}}/bin/nagobot cron remove <id1> [id2...]
   ```
4. **List jobs**:
   ```
   exec: {{WORKSPACE}}/bin/nagobot cron list
   ```

Using the same `--id` with `set-cron` or `set-at` will update (upsert) the existing job.

## Flag Reference

- `--id`: unique job identifier (required).
- `--expr`: 5-field cron expression, e.g. `"0 9 * * *"` (required for set-cron).
- `--at`: execution time in RFC3339, e.g. `"2026-02-07T18:30:00+08:00"` (required for set-at).
- `--task`: detailed instructions injected into a newly created cron thread that wakes and executes the task. Include objective, scope, constraints, and expected output. ~100–800 characters recommended. Wrap in double quotes; escape inner double quotes with `\"`.
- `--agent`: optional agent template name from `agents/*.md`.
- `--wake-session`: session to receive the execution result. The result is injected into this session, waking it to run inference and deliver to the user. Defaults to `cli`. Use `telegram:<userID>` to target a specific Telegram user (e.g. `telegram:123456`).
- `--silent`: suppress result delivery entirely.

## Examples

Add a daily summary job at 09:00:
```
{{WORKSPACE}}/bin/nagobot cron set-cron --id daily-summary --expr "0 9 * * *" --task "Review recent session activity and produce a daily summary: completed work, pending actions, immediate next steps. Highlight blockers and reference key files." --agent GENERAL --wake-session cli
```

Add a one-time cleanup job:
```
{{WORKSPACE}}/bin/nagobot cron set-at --id one-shot-cleanup --at "2026-02-10T18:30:00+08:00" --task "Clean up stale temp artifacts under the project root. Remove only known temp outputs and cache leftovers, keep source files untouched. Output a short report of what was deleted." --agent GENERAL --silent
```

Update an existing job (same `--id` overwrites):
```
{{WORKSPACE}}/bin/nagobot cron set-cron --id daily-summary --expr "0 8 * * 1-5" --task "Weekday morning briefing: summarize overnight changes, open issues, and today's priorities." --agent GENERAL --wake-session cli
```

Remove jobs:
```
{{WORKSPACE}}/bin/nagobot cron remove daily-summary one-shot-cleanup
```

List all jobs:
```
{{WORKSPACE}}/bin/nagobot cron list
```

## Cron Expression Notes

Standard 5-field: `min hour day month weekday`
- `0 9 * * *` — every day at 09:00
- `*/15 * * * *` — every 15 minutes
- `0 9 * * 1-5` — weekdays at 09:00
