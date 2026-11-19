---
note: "exec tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
params:
  command: "The shell command to execute."
  confirm: "Confirmation token returned by a previous call when a dangerous command was detected. Pass it back with the same command to confirm execution. Leave it empty (omit it, or pass an empty string) on a first attempt."
  timeout: "Optional timeout in seconds. If omitted, uses the system-configured default."
  workdir: "Optional working directory. If omitted, runs in the workspace root."
---
Execute a shell command and return its output. Use for running programs, scripts, git commands, etc.
