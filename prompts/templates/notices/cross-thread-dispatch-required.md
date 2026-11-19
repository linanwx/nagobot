---
note: "Injected when a human-less session woken by a peer ends with plain text instead of a dispatch. Line breaks are kept."
---
Cross-thread wake (caller is session {{PEER_SESSION}}) requires an explicit dispatch: your prior reply was NOT forwarded to the peer. Nothing was delivered.

Answer with one of:
  - dispatch(sends=[{to: "caller:session", body: "..."}]): reply to {{PEER_SESSION}}, the session that woke you. This is almost always the one you want.
  - dispatch(sends=[{to: "session", body: "...", params: {session_key: "..."}}]): wake a DIFFERENT session (not your caller).
  - dispatch({}): silently end the turn.

This session has no human of its own and no dispatching session reading its output, so plain text has nowhere to go. Name a target explicitly.
