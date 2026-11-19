---
note: "glob tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
params:
  path: "Starting directory for the search. Defaults to workspace root."
  pattern: "Glob pattern to match, e.g. \"**/*.go\", \"*.json\", \"cmd/**/*.md\"."
---
Find files by NAME/path matching a glob pattern. Supports ** for recursive directory matching (e.g. "**/*.go", "cmd/**/*.md"). Results are sorted by modification time (most recent first), limited to 200 entries. This does NOT look inside files — to find files CONTAINING certain text, use grep instead.
