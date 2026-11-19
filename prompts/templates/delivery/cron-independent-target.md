---
note: "`delivery` of an independent-mode cron job with a wake_session target. Rendered as ONE line: line breaks here are joined with spaces."
---
you were woken by cron (independent mode).
Caller is cron — output to caller is dropped.
After completing your task, dispatch(to=session, params={session_key: "{{TARGET}}"}) to deliver results.
