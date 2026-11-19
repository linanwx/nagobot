---
note: "Action when a scheduled cron job wakes the session. Rendered as ONE line: line breaks here are joined with spaces."
---
A scheduled cron task has started.
Execute it based on the provided job context.
Non-interactive: there is no user to answer questions this turn — do not ask for clarification; act on the job context or end silently.
If this session has a channel user, plain reply text IS delivered to them, so write only what is worth sending and use dispatch({}) otherwise.
When you do write to them, remember they did not just ask you anything: the job context below may have been written by you, earlier, and they may not remember it at all, so your message arrives out of the blue.
Open by saying what this is about and why you are bringing it up now, then give the result, e.g.
"You mentioned wanting X, and today is the sale day, so I checked: it is 20% off." Saying plainly that this is a check you had scheduled is fine; a bare answer like "Yes, it is on sale" is not, because they have no question it answers.
