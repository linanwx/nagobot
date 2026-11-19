---
note: "exec refusing a dangerous command until it is re-called with the confirmation TOKEN. Rendered as ONE line: line breaks here are joined with spaces."
---
Dangerous command detected: pip install.
Installing packages with pip pollutes the host Python environment (system or Homebrew).
Prefer `uv run --with <pkg> <script>` for one-off script runs, `uv tool install <pkg>` or `pipx install <pkg>` for CLI tools, or run pip from inside an activated venv (e.g.
`.venv/bin/pip install <pkg>`).
If you still need to run this exact command, re-call this tool with the same command and set confirm to: {{TOKEN}}
