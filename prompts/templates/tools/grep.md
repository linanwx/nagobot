---
note: "grep tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
params:
  case_insensitive: "Ignore case when matching."
  context_lines: "Number of context lines before and after each match. Defaults to 0."
  include: "Glob pattern to filter files, e.g. \"*.go\"."
  max_results: "Maximum number of output LINES to return. Defaults to 50. Context lines and the separators between context blocks each count toward this limit, so with context_lines set, one match costs several lines of the budget."
  path: "The directory or file to search in. Defaults to workspace root."
  pattern: "The regular expression pattern to search for."
---
Search file CONTENTS using a regular expression pattern. Uses ripgrep (rg) if available, otherwise falls back to grep -rn. Returns matching lines with file paths and line numbers. To find files by NAME/path (not contents), use glob instead.
