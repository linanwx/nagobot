---
note: "Action on a progress event about a dispatched child: a running snapshot or its end-of-turn notice. Rendered as ONE line: line breaks here are joined with spaces."
---
A progress event about a subagent/fork you dispatched.
Two kinds; tell them apart by the body.
RUNNING snapshot (starts with ⏳ or 🔍, no `event: turn_ended` line): the child is still working and this is NOT its result.
Pick one:
(a) it has surfaced useful information or a preliminary conclusion: relay it to the user as a brief plain-text note;
(b) it is heading the wrong way or has stalled: steer it in real time with `dispatch(to=subagent|subagent_fork)` using the SAME task_id, saying exactly what to change (the message reaches its running turn);
(c) otherwise `dispatch({})` to ignore it silently.
END-OF-TURN notice (`event: turn_ended`): the child's turn ended.
That is an event, not a verdict: it does not mean the task is done or done right.
Check the real state yourself: run the `read-session` command the notice gives (never `read_file` the raw `session_file`), and raise its `--tail` or call `check_session` if you need more.
Then pick one:
(1) it is waiting on helpers it dispatched itself: keep waiting, with `dispatch({})` or a short progress note to the user;
(2) it did the job badly, incompletely, or failed: wake it again with `dispatch(to=subagent|subagent_fork)` using the SAME task_id and say exactly what to fix;
(3) the result is complete and usable: act on it (answer your human in plain text, or move to the next step).
