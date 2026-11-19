---
note: "check_session tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
params:
  session_key: "Target session key (e.g. 'cli:threads:bg-check', 'telegram:123456')."
---
Inspect a session by key. Reports whether the session exists on disk, whether a thread is currently loaded for it, and the thread's runtime state (iterations / current tool / pending) when a thread is active. Useful for checking the actual state of a child session you dispatched ({{TASK_ID_TARGETS}}) by its resolved session_key, e.g. whether it is still running after its end-of-turn notice. Do NOT poll it while waiting for a child: its end-of-turn notice arrives on its own as a `progress` wake.
