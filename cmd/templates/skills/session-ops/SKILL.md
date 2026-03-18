---
name: session-ops
description: Use when the user wants to review past conversations, search session history or memory, check context usage/compression stats, inspect which model/provider a session is using (model resolution chain), inspect session metadata, or configure session settings (switch agent, set timezone). Also use when asking "what model am I using" or debugging model routing.
tags: [session, summary, search, internal]
---
# Session Operations

CLI commands for inspecting, summarizing, and configuring sessions. All commands output to stdout.

## list-sessions

List all sessions with summary status. Filtered by recent activity.

```
exec: {{WORKSPACE}}/bin/nagobot list-sessions [--days N] [--user-only] [--changed-only] [--fields f1,f2,...]
```

- `--days N`: Only show sessions active within N days (default: 2)
- `--user-only`: Exclude `cron:*` and `:threads:` sessions (only real user sessions)
- `--changed-only`: Exclude sessions with `changed_since_summary=false` or `message_count=0`
- `--fields f1,f2,...`: Only include specified fields per session (e.g. `key,is_running,has_heartbeat`)

Output: JSON with fields per session:
- `key`: Session identifier (e.g. `telegram:12345`, `cli`)
- `timezone`: IANA timezone if configured (e.g. `Asia/Shanghai`), empty if not set
- `updated_at`: Last activity timestamp
- `message_count`: Total messages (including tool messages)
- `summary`: Current summary text (empty if none)
- `summary_at`: When summary was last written
- `changed_since_summary`: `true` if session has new activity since last summary
- `is_running`: Whether the session's thread is currently executing (only populated via RPC)
- `has_heartbeat`: Whether the session has a non-empty `heartbeat.md`
- `last_user_active_at`: Timestamp of last message from a real user channel (null if no user activity)

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

Sampling is **deterministic** (no randomness): messages are picked at evenly spaced intervals. The output header explains the sampling mechanism. Each message shows its original position index `[N]` in the filtered sequence. YAML frontmatter in messages is automatically stripped. After the sampled messages, the last 5 recent messages not already in the sample are appended.

## set-summary

Set or update the summary for a session.

```
exec: {{WORKSPACE}}/bin/nagobot set-summary <key> <summary>
```

- `<key>`: Session key
- `<summary>`: Summary text (≤200 characters recommended)

Writes to `system/sessions_summary.json`. Automatically cleans up entries with `summary_at` older than 7 days and reports what was cleaned.

## session-stats

Show context usage stats and model resolution chain for a session.

```
exec: {{WORKSPACE}}/bin/nagobot session-stats <key>
```

- `<key>`: Session key (e.g. `cli`, `telegram:12345`)

Output: JSON with fields:
- `model_resolution`: Full model resolution chain for this session
  - `steps[]`: Each step of the resolution chain:
    - `step`: Step name (`session_agent`, `agent_specialty`, `model_routing`)
    - `lookup`: What was queried (e.g. `sessionAgents["discord:123"]`)
    - `found`: Result (empty string if miss)
    - `status`: `hit` or `miss`
    - `fallback`: Value used on miss (only present when status is `miss`)
  - `resolved_provider`: Final provider name (e.g. `openai`, `openrouter`)
  - `resolved_model`: Final model identifier (e.g. `gpt-5.4`, `minimax/minimax-m2.7`)
  - `resolved_context_window`: Context window size for the resolved model
  - `is_default`: `true` if no agent-specific routing was found (using global default)
- `message_count`: Total messages in session
- `role_counts`: Breakdown by role (user, assistant, tool, system)
- `compressed_messages`: Number of messages with Tier 1 compressed content
- `role_tokens`: Per-role token breakdown (user, assistant, tool) using compressed content
- `system_prompt_tokens`: Estimated token count of the system prompt (rebuilt from agent template; approximate because runtime vars like TIME/TOOLS are not injected)
- `raw_tokens`: Token estimate using original content
- `compressed_tokens`: Token estimate using compressed content (what the LLM actually sees)
- `tokens_saved`: Difference (raw - compressed)
- `context_window_tokens`: Context window size for the resolved model (model-aware, not global default)
- `usage_ratio`: `compressed_tokens / context_window_tokens`
- `warn_ratio`: Configured pressure threshold (default 0.8)
- `pressure_status`: `ok`, `warning` (≥64% of window), or `pressure` (≥80% of window)

Use `model_resolution` to determine the exact model a session is using and debug routing issues. The `steps` array shows exactly which config entries were consulted and whether each step hit or fell back to a default.

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

## set-agent

Set or clear the agent for a session.

```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key> --agent <agent_name>
```

Clear the agent override (revert to default):
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key>
```

- `--session`: session key (required). Examples: `discord:123456`, `telegram:78910`, `cli`.
- `--agent`: agent template name from `agents/*.md`. Omit or empty to clear the override.

## set-timezone

Set or clear the IANA timezone for a session.

```
exec: {{WORKSPACE}}/bin/nagobot set-timezone --session <session_key> --timezone <iana_timezone>
```

Clear the timezone (revert to system default):
```
exec: {{WORKSPACE}}/bin/nagobot set-timezone --session <session_key>
```

- `--session`: session key (required). Examples: `discord:123456`, `telegram:78910`, `cli`.
- `--timezone`: IANA timezone name. Examples: `Asia/Shanghai`, `America/New_York`, `Europe/London`. Omit or empty to clear.

**Note**: `set-agent` and `set-timezone` changes take effect on the **next message** in that session. Changes persist across server restarts (saved to config.yaml).

## Per-Session Model Switching

nagobot does not support directly setting a model per session. When a user asks to use a specific model for a session, analyze their intent:

**Case 1: User wants to switch to an existing agent**
- If the user's intent maps to an existing agent (e.g. "use the fallout agent for this session"), simply use `set-agent` above.

**Case 2: User explicitly wants a specific provider/model (e.g. "use openai gpt-5.4 for this session")**
- Create a dedicated agent template for that model:
  1. Create `agents/<model-slug>.md` — a minimal agent whose `specialty` is a unique name (e.g. `specialty: gpt54-dedicated`)
  2. Use `manage-config` skill's `set-model --type <specialty> --provider <provider> --model <model>` to route that specialty to the requested model
  3. Use `set-agent --session <key> --agent <model-slug>` to assign the new agent to the session
- After switching, reply to the user:
  - Confirm the switch is done
  - Explain that per-session model is not natively supported, so the workaround was:
    - Created agent `<model-slug>` with specialty `<specialty>`
    - Routed specialty `<specialty>` → `<provider>/<model>`
    - Assigned agent `<model-slug>` to session `<key>`
