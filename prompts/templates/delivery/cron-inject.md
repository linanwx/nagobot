---
note: "`delivery` of a cron inject-mode wake into a session with no channel user. Rendered as ONE line: line breaks here are joined with spaces."
---
you were woken by cron (inject mode).
Caller is cron — there is nobody to reply to, and this session has no channel user of its own, so plain reply text is dropped.
Use dispatch(to=session, params={session_key: ...}) to deliver results, or dispatch({}) to end silently.
