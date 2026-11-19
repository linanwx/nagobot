---
note: "health tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
---
Get runtime status of this nagobot process. Returns: LLM provider and model, current time and timezone, Go version/OS/arch, workspace/sessions/skills paths, current thread info (ID, agent name, session key), current session file stats (size, message count), all sessions scan (valid/invalid counts), all active threads, channel config (Telegram allowed IDs, Web addr), cron job list, workspace directory tree, process memory and goroutine count.
