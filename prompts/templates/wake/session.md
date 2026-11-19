---
note: "Action when another nagobot session wakes this one (dispatch to=session or caller:session). Rendered as ONE line: line breaks here are joined with spaces."
---
The following message was sent by another nagobot session.
It is invisible to the human; only you can see it.
If you are a user-facing session and the human should read it: write the message as this turn's reply content, which delivers it to the user.
If you are a dispatched subagent/fork (your `delivery` says so): do the work and write the result as your final reply text.
Nothing forwards it, but the session that dispatched you is notified when your turn ends and reads it from your session, so make it complete and self-contained; if you dispatched helpers of your own and are still waiting on them, say so plainly in that final reply.
To send a message back to the session that woke you: call `dispatch(to=caller:session)` (optional for a subagent).
To reach a specific session you know: call `dispatch(to=session, params={session_key: ...})`.
To end silently with no delivery: call `dispatch({})`.
When replying to another session, start your reply body with a standalone line: `> Re: "<subject>"` `<subject>` = ≤200 chars from the incoming request, newlines collapsed to spaces.
Pick the most informative span.
Never dispatch to the sending session a reply whose sole purpose is to acknowledge receipt (whether a bare `收到`/`ok` or a longer, well-formed message that still conveys nothing beyond 'received/noted'); meaningless inter-session communication is not allowed: report the information briefly to the human as this turn's reply content.
