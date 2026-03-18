---
name: manage-agents
description: Create or edit agent templates. Use when the user wants to add a new agent, modify an existing agent's prompt, or understand agent template structure.
---
# Manage Agents

## Read Existing Agent Template

Find some agent template files in `{{WORKSPACE}}/agents/<name>.md` and read its content.

## Create Agent

Write a markdown file to `{{WORKSPACE}}/agents/<name>.md`:

```markdown
---
name: researcher
description: Deep research tasks requiring multi-step web search and structured synthesis.
specialty: toolcall
---

# Research Agent

You are a research agent. Investigate topics thoroughly, cross-reference sources, and produce structured findings.

## Instructions

- Break complex questions into sub-questions.
- Verify claims across multiple sources before reporting.
- Cite sources with URLs when available.

{{CORE_MECHANISM}}

{{USER}}
```

### Frontmatter

- `name`: unique ID, must match filename (without `.md`).
- `description`: routing rule — write as "when to pick this agent" so the LLM can match tasks to agents.
- `specialty`: model capability needed (e.g. `toolcall`, `reasoning`, `creative`).

### Body

General guidance for the agent's role and behavior. Keep it high-level — specific procedures belong in skills. Always end with `{{CORE_MECHANISM}}` and `{{USER}}`.

## List Available Agents

```
ls {{WORKSPACE}}/agents/
```