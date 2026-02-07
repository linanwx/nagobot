---
name: cron
description: Manage scheduled cron jobs and design autonomous recurring tasks.
---
# Cron Skill

Use this skill when you need scheduled execution.

Available tools:
- `manage_cron` for add/remove/list jobs
- `send_message` to proactively notify a channel
- `wake_thread` to wake an existing session thread (for example `main`)
- `spawn_thread` for delegated sub-tasks inside a cron run

Cron expression formats:
- 5-field expression: `min hour day month weekday` (example: `0 9 * * *`)
- descriptor form: `@every 30m`, `@daily`, `@hourly`

Recommended workflow:
1. Call `manage_cron` with `operation=list` first to inspect existing jobs.
2. For add:
   - choose a stable `id`
   - provide clear `task`
   - optionally set `agent` (template in `agents/*.md`)
3. Cron jobs run silently in independent thread sessions.
4. If users should see output, explicitly use `send_message` or `wake_thread`.
5. Remove obsolete jobs with `operation=remove`.
