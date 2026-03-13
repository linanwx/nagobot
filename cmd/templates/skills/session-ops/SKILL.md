---
name: session-ops
description: Use when the user wants to review past conversations, search session history or memory, check context usage/compression stats, or inspect session metadata.
tags: [session, summary, search, internal]
---
# Session Operations

CLI commands for inspecting and summarizing sessions. All commands output to stdout.

## list-sessions

List all sessions with summary status. Filtered by recent activity.

```
exec: {{WORKSPACE}}/bin/nagobot list-sessions [--days N]
```

- `--days N`: Only show sessions active within N days (default: 2)

Output: JSON with fields per session:
- `key`: Session identifier (e.g. `telegram:12345`, `cli`)
- `timezone`: IANA timezone if configured (e.g. `Asia/Shanghai`), empty if not set
- `updated_at`: Last activity timestamp
- `message_count`: Total messages (including tool messages)
- `summary`: Current summary text (empty if none)
- `summary_at`: When summary was last written
- `changed_since_summary`: `true` if session has new activity since last summary

Also includes `filter`, `total_sessions`, `shown_sessions` metadata.

## read-session

Read filtered chat history with pagination.

```
exec: {{WORKSPACE}}/bin/nagobot read-session <key> [--offset N] [--limit N] [--tail N] [--full]
```

- `<key>`: Session key (e.g. `cli`, `telegram:12345`)
- `--offset N`: Start from Nth filtered message (default: 0)
- `--limit N`: Number of messages to return (default: 20)
- `--tail N`: Show last N messages (overrides offset)
- `--full`: Show full message content without truncation (default: truncated to 500 chars)

Tool messages (`role=tool`), system messages, and tool-call-only assistant messages are filtered out. Output includes pagination info and a `Next:` hint when more messages remain.

## sample-session

Evenly sample filtered messages across the full conversation.

```
exec: {{WORKSPACE}}/bin/nagobot sample-session <key> [--count N]
```

- `<key>`: Session key
- `--count N`: Number of messages to sample (default: 20)

Sampling is **deterministic** (no randomness): messages are picked at evenly spaced intervals. The output header explains the sampling mechanism. Each message shows its original position index `[N]` in the filtered sequence.

## set-summary

Set or update the summary for a session.

```
exec: {{WORKSPACE}}/bin/nagobot set-summary <key> <summary>
```

- `<key>`: Session key
- `<summary>`: Summary text (≤200 characters recommended)

Writes to `system/sessions_summary.json`. Automatically cleans up entries with `summary_at` older than 7 days and reports what was cleaned.

## session-stats

Show context usage stats for a session: token estimates, compression savings, and pressure status.

```
exec: {{WORKSPACE}}/bin/nagobot session-stats <key>
```

- `<key>`: Session key (e.g. `cli`, `telegram:12345`)

Output: JSON with fields:
- `message_count`: Total messages in session
- `role_counts`: Breakdown by role (user, assistant, tool, system)
- `compressed_messages`: Number of messages with Tier 1 compressed content
- `role_tokens`: Per-role token breakdown (user, assistant, tool) using compressed content
- `system_prompt_tokens`: Estimated token count of the system prompt (rebuilt from agent template; approximate because runtime vars like TIME/TOOLS are not injected)
- `raw_tokens`: Token estimate using original content
- `compressed_tokens`: Token estimate using compressed content (what the LLM actually sees)
- `tokens_saved`: Difference (raw - compressed)
- `context_window_tokens`: Configured context window size
- `usage_ratio`: `compressed_tokens / context_window_tokens`
- `warn_ratio`: Configured pressure threshold (default 0.8)
- `pressure_status`: `ok`, `warning` (≥64% of window), or `pressure` (≥80% of window)

## search-memory

Search across all session messages (current + history backups, merged and deduplicated by message ID). Searches original message content for all roles (user, assistant, tool).

```
exec: {{WORKSPACE}}/bin/nagobot search-memory <keyword1> [keyword2] ... [--days N] [--limit N] [--session <key>]
```

- `<keywords>`: One or more search keywords (AND logic — all must match). Case-insensitive.
- `--days N`: Only search sessions active within N days (default: 30)
- `--limit N`: Maximum results to return (default: 20)
- `--session <key>`: Limit search to a specific session key
- `--after <date>`: Only include messages after this date (YYYY-MM-DD or RFC3339)
- `--before <date>`: Only include messages before this date (YYYY-MM-DD or RFC3339)

Output: JSON with fields:
- `query`: Original search query
- `hits[]`: Matching messages, each with `session_key`, `message_id`, `role`, `timestamp`, `snippet`, `score`
- `total`: Total matches found
- `shown`: Number returned (capped by limit)
- `scanned`: Total messages scanned

### Context browse mode

Browse messages around a specific message ID (from search results).

```
exec: {{WORKSPACE}}/bin/nagobot search-memory --context <message-id> [--window N]
```

- `--context <id>`: Message ID to center on (session key is derived from the ID)
- `--window N`: Number of messages before and after the target (default: 5)

Output: JSON with `target_id`, `session_key`, `messages[]` (each with `message_id`, `role`, `timestamp`, `content`, `is_target`), `position`, `total`.

### Workflow

1. Use keyword search to find relevant messages
2. Use `--context` with a message ID from the results to see surrounding conversation
