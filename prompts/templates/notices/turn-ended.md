---
note: "Closing paragraph of the notice a parent receives when a dispatched child ends its turn. Rendered as ONE line: line breaks here are joined with spaces."
---
This notice only says the turn ENDED, not that the task is done or done right.
Read the child's actual output before acting: `{{NAGOBOT_BIN}} read-session {{CHILD_SESSION}} --tail 1 --full` (raise --tail for more of its history).
Do not read_file session_file: it is the raw log, full of tool payloads and reasoning blobs.
For follow-up tasks, dispatch to the same target (subagent or subagent_fork) with the same task_id to continue the conversation.
