---
name: explain_runtime
description: Explain how nagobot works by inspecting its codebase with gh when available.
enabled: true
requires:
  - kind: cmd
    value: gh
---
# Runtime Explainer Skill

Goal: explain nagobot's runtime architecture and behavior using evidence from the real codebase.

Execution rules:
1. First check whether the `gh` command is available in the current environment.
2. If `gh` is available, use it (together with local file inspection) to understand and explain the source repository.
3. If `gh` is not available, ignore this skill and continue your original task using other available methods.
4. If the user explicitly asks for this skill's workflow but `gh` is missing, tell the user that `gh` is not installed and suggest installing it.

Output style:
- Be concrete and reference actual files/components.
- Prefer concise explanations and clear structure.
