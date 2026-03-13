---
name: explain-runtime
description: Use when the user asks how nagobot works, its architecture, configuration, or runtime behavior — provider selection, session routing, tool dispatch, hot-reload. Investigates from actual source code, not guessing.
---
# Runtime Explainer Skill

Goal: explain nagobot's runtime architecture and behavior using evidence from the real codebase.

Execution rules:
1. Use web tools directly to inspect the repository and docs.
2. Primary repository link: https://github.com/linanwx/nagobot
3. Prioritize authoritative sources in that repo (README, key package files, and command entrypoints).
4. Combine web evidence with local file inspection when needed.

Output style:
- Be concrete and reference actual files/components.
- Prefer concise explanations and clear structure.
