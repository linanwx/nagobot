---
note: "write_file tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
params:
  content: "The content to write to the file. Always required — the file is overwritten with exactly this text. Pass an empty string to create an empty file."
  path: "The path to the file to write."
---
Write content to a file at the given path. Relative paths are resolved from workspace root. Creates parent directories if needed.
