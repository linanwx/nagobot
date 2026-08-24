---
name: session-ops
description: Use when you need to recall something from a past conversation that is not in your current context — "when did I last ...", "what did we decide about ...", "have I told you about ..." — or to recall a preference, rule, or correction the user established in ANOTHER session ("didn't I tell you to ...", "how do I like ... done"), or to review past conversations and search session memory across sessions. Also for context usage/compression stats, inspecting which model/provider a session is using (model resolution chain), session metadata, and session settings (switch agent, set timezone), including "what model am I using" and debugging model routing.
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

Set or update a session's **topic label** — despite the command name, this is a
classification, not a summary.

```
exec: {{WORKSPACE}}/bin/nagobot set-summary <key> <summary>
```

- `<key>`: Session key
- `<summary>`: What the session is about, in the fewest words that let a reader
  pick it out of a list — a topic, optionally narrowed by who or what it
  concerns (`nagobot web 客户端`, `与 Nansen 的日常对话`). **No status, progress,
  version numbers, or open questions**: this text sits in every agent's system
  prompt and in the web UI as the session's name, and detail put here goes stale
  silently in both. ≤200 characters, usually far fewer. Must be a single line —
  it renders as one `- <key>: <summary>` row, so a line break splits the row.

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
  - `resolved_model`: Final model identifier (e.g. `gpt-5.4`, `minimax/minimax-m3`)
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

## Recalling something from a past conversation

There is no search command. Past conversations are searched with the ordinary
`grep` tool over the memory files on disk. Read this whole section before you
start guessing keywords — the layout is what makes the search cheap.

### Where the memory lives

Every session keeps one dated file per compression:

```
{{WORKSPACE}}/sessions/<session key, ":" replaced by "/">/memory/YYYY-MM-DD.md
```

So `discord:1474429571540582463` is `{{WORKSPACE}}/sessions/discord/1474429571540582463/memory/`,
and `cli` is `{{WORKSPACE}}/sessions/cli/memory/`. Every session that has ever
been compressed has such a directory, so `{{WORKSPACE}}/sessions` is the whole
searchable past of every channel and every person, in one tree.

### What one file contains

YAML frontmatter with a one-line `summary`, then the compression report:

```
---
summary: <one line, ~200 chars, the whole file in a sentence>
---

## Compression <HH:MM>

# Session Compression — <session key>
Coverage: <start> ~ <end> <timezone>
Participants: <who>

## 1. Primary intent        <- what this stretch of conversation was about
## 2. Decisions & constraints <- rules, preferences, settled facts, config values
## 3. Files & paths         <- every path / ID / command touched
## 4. Errors & fixes
## 5. All user messages     <- near-verbatim user turns, grouped by date
## 6. Current work
## 7. Next step
```

Section 5 is the one that matters most for recall: it keeps the user's own
sentences, close to verbatim, with dates. A fact the user stated once — a
date, a number, a name, a decision — is in section 5 of some file if it is
anywhere.

### Why you must grep, not rely on your prompt

Your `memory_index` section lists only **this session's** memory files, and only
the most recent 20, and only those that already have a `summary`. Anything from
another session, from further back, or not yet summarized is absent from your
prompt entirely. Absence there says nothing about whether the fact exists. If
the user asks about something that is not in your context, grep before you
conclude it was never said. The global equivalent of that index is one grep
away — see the next section.

### The global index: grep the summary lines

The `summary` frontmatter line is the whole file in one sentence, so grepping
*only those lines* gives you an index of every session's every day — one line
per file, across the entire tree. This is the global counterpart to the
`memory_index` in your prompt, which covers this session alone:

```
grep(pattern: "^summary:.*(<topic>|<synonym>)", path: "{{WORKSPACE}}/sessions",
     include: "20??-??-??.md", max_results: 60)
```

Each hit is `<path>:2:summary: …`, and the path names the session and the date.
Drop the topic filter (`pattern: "^summary:"`, `max_results: 200`) to read the
whole landscape instead — on a mature workspace that is on the order of a
hundred lines covering ~20 sessions, which is a real but bounded cost. Do that
only when you need the lay of the land, not to answer one question.

**Use the index to find WHERE, never to find WHAT.** A summary is ~200
characters standing in for a day of conversation; a date, a number, a name the
user said once is almost never in it. So an empty index sweep is not evidence
the fact is absent — it only means no *day* was mostly about that topic. Facts
live in the body, which is the content search below. When the index does hit,
you have narrowed the content search from the whole tree to one session.

### How to search the content

Use the `grep` tool. Restrict to memory files by filename, not by path — memory
files are the only `.md` files named `YYYY-MM-DD`:

```
grep(pattern: "<regex>", path: "{{WORKSPACE}}/sessions", include: "20??-??-??.md",
     context_lines: 2, max_results: 60)
```

Rules that make the difference between one call and fifteen:

- **`pattern` is a regex — put the alternatives in it.** One call with
  `护照|passport|passeport` beats three calls with one word each. Do the same for
  synonyms and for Chinese/English pairs.
- **Start wide, then narrow.** First call: the bare subject
  (`passport`). Only if that returns too much do you add the qualifier
  (`passport.*(issued|expiry|签发|到期)`).
- **`context_lines` costs budget.** Each context line counts against
  `max_results`, so `context_lines: 2` with `max_results: 60` gives you roughly
  12 matches, not 60. Use `context_lines: 0` for a first sweep to see *which
  files* hold the topic, then read those files.
- **Never loop on failed keywords.** Two or three well-formed patterns that all
  come back empty is a real answer: the fact is not on disk. Say so instead of
  trying a fourth phrasing.
- **`case_insensitive: true`** for anything Latin-script.

### Standing preferences: USER.md

The memory files are what was *said*. `USER.md` is what was *settled* — one per
session, at the session directory's root, written by `session-reflect`:

```
{{WORKSPACE}}/sessions/<session key, ":" replaced by "/">/USER.md
```

It holds preferences, corrections, workflow patterns and a reflection log. So
when the question is "what did they tell me to do / not do", this is the file,
and the dated memory files are the fallback.

**Grep it only for OTHER sessions.** Your own session's `USER.md` is already
injected into your prompt IN FULL — the `type: user_preference` section — so
grepping the tree
for a preference you can already see is wasted budget. What is invisible is
every other session's copy, and a rule the user established on Discord applies
to them on the web too.

```
grep(pattern: "<regex>", path: "{{WORKSPACE}}/sessions", include: "USER.md",
     context_lines: 2, max_results: 40)
```

Two things make this a SEPARATE call, not a widened one:

- **`include` takes one pattern, and the two backends disagree about braces.**
  `include: "{20??-??-??,USER}.md"` works under ripgrep and matches *nothing*
  under plain `grep`, whose `--include` has no brace expansion — silently, with
  no error, which reads exactly like "the fact was never recorded". Do not try
  to fold the two searches into one glob. Widening to `*.md` is the other wrong
  fix: it drags in `heartbeat.md`, `dream.md`, `file-track.md` and every pin.
- **`USER.md` carries no `summary:` frontmatter**, so the global index sweep
  above cannot point at it. It is cheap to sweep directly instead — one file per
  session, a few dozen files, versus 100+ dated memory files.

### Workflow

0. **Preferences and rules → sweep `USER.md` first.** If the question is what
   the user wants, prefers, or has already corrected you about, one `USER.md`
   grep answers it more directly than any number of dated files. For a fact,
   a date or a number, skip to step 1.
1. **Index, when you don't know where to look** — the `^summary:` sweep above,
   to find which session and which period the topic belongs to. Skip this when
   you already know the session, or when you are after one specific fact (a
   summary would not carry it).
2. **Sweep** — one `grep` over `{{WORKSPACE}}/sessions` (or the one session the
   index pointed at) with an alternation pattern and `context_lines: 0`, to find
   which dates hold the topic.
3. **Read** — `read_file` the promising `memory/<date>.md` in full. One file is a
   few hundred lines and gives you the surrounding decisions, not just a line.
4. **Only if you need the exact original wording** — the pre-compression
   snapshots in that session's `history/*.jsonl` hold the raw turns. Never read
   one whole (they are huge); grep them for a distinctive phrase, or for a
   message ID quoted in a `[compressed — …]` marker.

To scope a search to one person or channel, point `path` at that session's
directory instead of `{{WORKSPACE}}/sessions` — orders of magnitude less to scan.
Use `list-sessions` above if you need to find the right session key first.

## set-agent

Set or clear the agent for a session.

```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key> --agent <agent_name>
```

Pin a specific provider/model to a session (writes a `type:session` rule into `thread.models`):
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key> --provider <provider> --model <model>
```

Clear the override — both the agent assignment AND the session model rule (revert to default):
```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session_key>
```

- `--session`: session key (required). Examples: `discord:123456`, `telegram:78910`, `cli`.
- `--agent`: agent template name from `agents/*.md`. Omit or empty to clear the assignment. Writes the session→agent mapping to `meta.json`.
- `--provider` / `--model`: pin a model to this session. Writes a `type:session` rule to `config.yaml > thread.models` — this **overrides** the agent's specialty routing for this session. No agent file is created.

`--agent` and `--provider/--model` are independent and can be combined (session→agent in meta.json + session→model in the rule list). A session model rule has the highest resolution precedence (session > agent > specialty > default).

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

When a user asks to use a specific model for a session:

**Case 1: User wants to switch to an existing agent** — use `set-agent --session <key> --agent <name>`.

**Case 2: User wants a specific provider/model** — use `set-agent --session <key> --provider <provider> --model <model>`. This writes a `type:session` rule that pins the model for that session only, overriding specialty routing. No agent file is created, no other config touched.
