---
note: "Note appended to every dispatch that delivered something. Rendered as ONE line: line breaks here are joined with spaces."
---
The turn continues: dispatch never waits for the target, and the deliveries above are already sent (do NOT resend them).
A subagent/fork you dispatched notifies you automatically with a `progress` wake when its turn ends; do not wait or poll for it.
Now finish this turn: tell your human what you just did in plain reply text, or call dispatch({}) alone if there is nothing worth saying.
