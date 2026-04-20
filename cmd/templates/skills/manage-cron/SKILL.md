---
name: manage-cron
description: Use when the user wants to schedule recurring or one-time tasks, set up automated reminders/jobs, or manage existing cron schedules (create, update, remove, list).
---
# Manage Cron Jobs

Cron fires are channel events — caller = cron, not a routable session. Every
cron-triggered turn's caller sink is a **drop sink**: naive final text output
is discarded. The session MUST call `dispatch(to=user)` or
`dispatch(to=session, session_key=...)` explicitly to deliver anywhere.

## Two Modes

### 1. Independent mode (default)

The job runs in its own dedicated cron session `cron:<ID>` with the configured
agent. Results don't go anywhere unless the model explicitly dispatches.

**When to use**: background jobs that process data and produce artifacts
(tidyup, summaries, world-knowledge updates) — then dispatch results to a
user session (cli, telegram:xxx) when done.

Create:
```
exec: {{WORKSPACE}}/bin/nagobot cron set-cron --id <id> --expr "<cron-expr>" \
    --task "<instructions>" --agent <agent-name> [--wake-session <report-target>]
```

- `--agent` (required for independent mode): agent template running in the job's session
- `--wake-session` (optional): appears in the wake's `delivery` field as "dispatch results to this session" — prompt hint only, not a programmatic wire
- Omit `--wake-session` only for truly silent jobs (rare; usually you want to report somewhere)

### 2. Inject mode (DirectWake)

The task is injected as a wake message into an **existing** session. That
session's own agent handles the task. The cron doesn't run its own agent.

**When to use**: scheduled nudges/reminders that should be handled by a user's
existing session context (e.g. "ping telegram user about their deadline").

Create:
```
exec: {{WORKSPACE}}/bin/nagobot cron set-cron --id <id> --expr "<cron-expr>" \
    --task "<message or instructions>" --wake-session <target-session> --direct-wake
```

- `--wake-session` (required): target session that receives the injection
- `--direct-wake` (flag): switches to inject mode
- `--agent`: must be omitted (inject mode preserves the target session's agent)

## One-time jobs

Replace `set-cron` with `set-at` and `--expr` with `--at "<RFC3339>"`.

## Management commands

- **List**: `exec: {{WORKSPACE}}/bin/nagobot cron list`
- **Remove**: `exec: {{WORKSPACE}}/bin/nagobot cron remove <id> [id2...]`
- **Update**: re-run `set-cron` / `set-at` with the same `--id`

## Examples

Independent mode — daily summary, reports to cli:
```
{{WORKSPACE}}/bin/nagobot cron set-cron --id daily-summary --expr "0 9 * * *" \
    --task "Review overnight activity and produce a daily briefing." \
    --agent session-summary --wake-session cli
```

Independent mode — silent background tidyup (no report):
```
{{WORKSPACE}}/bin/nagobot cron set-cron --id tidyup --expr "0 4 * * *" \
    --task 'You must call use_skill("tidyup-dispatcher") and follow its instructions.' \
    --agent tidyup
```

Inject mode — weekday morning nudge to telegram user:
```
{{WORKSPACE}}/bin/nagobot cron set-cron --id morning-nudge --expr "0 8 * * 1-5" \
    --task "Good morning! Any plans for today?" \
    --wake-session telegram:123456 --direct-wake
```

One-time cleanup (independent mode):
```
{{WORKSPACE}}/bin/nagobot cron set-at --id cleanup-2026 --at "2026-02-10T18:30:00+08:00" \
    --task "Clean up stale temp artifacts. Output a short report of deletions." \
    --agent tidyup --wake-session cli
```

## Flag Reference

- `--id`: unique job identifier (required, used for upsert).
- `--expr`: 5-field cron expression (required for `set-cron`).
- `--at`: RFC3339 execution time (required for `set-at`).
- `--task`: instruction text for the target session's LLM. For independent
  mode, write as AI-to-AI task instructions. For inject mode, write as the
  message that will appear in the target session.
- `--agent`: agent template name (independent mode only). Omit for inject mode.
- `--wake-session`: session key. Target session for inject mode; delivery hint
  for independent mode. Examples: `cli`, `telegram:123456`, `discord:xxx`.
- `--direct-wake`: flag that switches to inject mode. When set, `--agent` is
  rejected and `--wake-session` becomes required.

## Cron Expression Notes

Standard 5-field: `min hour day month weekday`
- `0 9 * * *` — every day at 09:00
- `*/15 * * * *` — every 15 minutes
- `0 9 * * 1-5` — weekdays at 09:00

## Why caller is dropped

Cron has no session to reply to. If the model naively outputs final text
without dispatch, that text goes to the drop sink — it is recorded in session
history but not delivered anywhere. This is by design: every delivery path
must be explicit. Always decide: `dispatch(to=user)` (inject mode, user-facing
target), `dispatch(to=session, session_key=...)` (cross-session forward), or
`dispatch({})` (silent intentional skip).
