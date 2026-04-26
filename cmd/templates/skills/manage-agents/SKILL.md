---
name: manage-agents
description: Create, edit, list, or delete agent templates. Use when the user wants to add a new agent, modify an agent's prompt, change its specialty/sections, audit which agents are available, or remove an agent.
---
# Manage Agents

## Directory layout — write ONLY to `{{WORKSPACE}}/agents/`

- `{{WORKSPACE}}/agents/` — your custom agents. Safe to write. Never auto-cleaned.
- `{{WORKSPACE}}/agents-builtin/` — **read-only**. Wiped and re-copied from the binary embed on every `nagobot update` / `onboard --sync`. Anything you put here is deleted on next update.

Read `agents-builtin/` to inspect built-in agents (`soul`, `coder`, `fallout`, `general`, `tidyup`, …); never write to it. To customize a built-in, copy it into `agents/` and edit the copy — files in `agents/` win when the same name exists in both.

## List Available Agents

```
exec: ls {{WORKSPACE}}/agents/ {{WORKSPACE}}/agents-builtin/
```

## Read Existing Agent

```
read_file: {{WORKSPACE}}/agents/<name>.md          # custom
read_file: {{WORKSPACE}}/agents-builtin/<name>.md  # built-in (read only)
```

## Create / Edit Agent

Write a markdown file at `{{WORKSPACE}}/agents/<name>.md`:

```markdown
---
name: researcher
description: Deep research tasks requiring multi-step web search and structured synthesis.
specialty: toolcall
sections:
  - user_memory_section
  - heartbeat_prompt_section
---

# Research Agent

You are a research agent. Investigate topics thoroughly, cross-reference sources, and produce structured findings.

## Instructions

- Break complex questions into sub-questions.
- Verify claims across multiple sources before reporting.
- Cite sources with URLs when available.
```

The runtime injects sections, tools, skills, and user memory automatically — **do not write `{{CORE_MECHANISM}}`, `{{USER}}`, or any other placeholder in the body**. Just write the prompt.

### Frontmatter

| Field | Required | Notes |
|---|---|---|
| `name` | yes | must match filename without `.md` |
| `description` | yes | routing signal — phrase as "when to pick this agent" |
| `specialty` | recommended | model-routing key (see below) |
| `sections` | optional | per-session injections (see below) |
| `context_window_cap` | optional | clamp window for this agent, e.g. `64k`, `200k`, `1M` |
| `tier_lossy_mode` / `tier_lossy_keep` | optional | compression tuning for high-traffic agents |

### `specialty` — model routing

Specialty is a key into `config.yaml > thread.models`. To see the keys currently configured (and which provider/model each maps to):

```
exec: {{WORKSPACE}}/bin/nagobot set-model --list
```

Common values: `chat`, `art`, `audio`, `image`, `pdf`, `writing`, `toolcall`, `roleplay`. Unknown specialty falls back to the default thread model.

### `sections` — only these three are valid

- `user_memory_section` — appends `{{WORKSPACE}}/USER.md`
- `heartbeat_prompt_section` — appends the session's `heartbeat.md`
- `memory_index_section` — appends a listing of `{{WORKSPACE}}/memory/`

Omit the field entirely if you don't need any of them.

## Delete Agent

```
exec: rm {{WORKSPACE}}/agents/<name>.md
```

**Before deleting**, audit which sessions pin this agent — if any do, thread creation will silently fail and their messages will be dropped:

```
exec: grep -l '"agent": "<name>"' {{WORKSPACE}}/sessions/*/*/meta.json
```

For each pinned session, clear the pin:

```
exec: {{WORKSPACE}}/bin/nagobot set-agent --session <session-key>
```
