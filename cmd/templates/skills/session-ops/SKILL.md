---
name: session-ops
description: List, read, sample, and summarize sessions.
tags: [session, summary, internal]
---
# Session Operations

CLI commands for inspecting and summarizing sessions. All commands output to stdout.

## list-sessions

List all sessions with summary status. Filtered by recent activity.

```
exec: nagobot list-sessions [--days N]
```

- `--days N`: Only show sessions active within N days (default: 2)

Output: JSON with fields per session:
- `key`: Session identifier (e.g. `telegram:12345`, `cli`)
- `updated_at`: Last activity timestamp
- `message_count`: Total messages (including tool messages)
- `summary`: Current summary text (empty if none)
- `summary_at`: When summary was last written
- `changed_since_summary`: `true` if session has new activity since last summary

Also includes `filter`, `total_sessions`, `shown_sessions` metadata.

## read-session

Read filtered chat history with pagination.

```
exec: nagobot read-session <key> [--offset N] [--limit N]
```

- `<key>`: Session key (e.g. `cli`, `telegram:12345`)
- `--offset N`: Start from Nth filtered message (default: 0)
- `--limit N`: Number of messages to return (default: 20)

Tool messages (`role=tool`), system messages, and tool-call-only assistant messages are filtered out. Output includes pagination info and a `Next:` hint when more messages remain.

## sample-session

Evenly sample filtered messages across the full conversation.

```
exec: nagobot sample-session <key> [--count N]
```

- `<key>`: Session key
- `--count N`: Number of messages to sample (default: 20)

Sampling is **deterministic** (no randomness): messages are picked at evenly spaced intervals. The output header explains the sampling mechanism. Each message shows its original position index `[N]` in the filtered sequence.

## set-summary

Set or update the summary for a session.

```
exec: nagobot set-summary <key> <summary>
```

- `<key>`: Session key
- `<summary>`: Summary text (≤200 characters recommended)

Writes to `system/sessions_summary.json`. Automatically cleans up entries with `summary_at` older than 7 days and reports what was cleaned.
