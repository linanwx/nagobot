---
name: session-summary-dispatcher
description: Session summary dispatcher — scan active sessions, sample conversations, and write concise summaries via CLI commands. Used by the session-summary cron task.
tags: [session, summary, internal]
---

## Workflow

1. **List sessions**: Run `list-sessions --need-summary` to discover sessions that need summaries. This flag applies smart filtering (excludes actively-chatting sessions, recently-summarized cron/large sessions, and stale child threads) and returns minimal fields (key, message_count, updated_at).

2. **For each qualifying session**:
   - Run `sample-session <key>` to read a conversation. Output shows evenly-spaced messages plus the last 5 recent messages not in the sample. YAML frontmatter in messages is automatically stripped.
   - Figure out:
      - Who, What, Why, When, Where
   - Run `set-summary <key> <summary>` with a concise summary at a high level (≤200 characters).

3. When done (whether or not any sessions were processed), reply with: `SESSION_SUMMARY_OK`

## Rules

- Keep summaries factual and concise. No greetings or filler.
- Write summaries in the same language as the conversation.
- Keep tool calls minimal. Skip sessions early if they don't qualify.
- For `:threads:` sessions, briefly describe the task and outcome.
