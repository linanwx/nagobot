---
name: how-nagobot-works
priority: 200
---
# How nagobot works

A channel is a message input/output component. `cli`, `telegram`, and `cron` are all treated as channels.

A session is a chat history made of a series of messages. A session is identified by a session key. For example, a Telegram session key is `telegram:<user_id>`.

Each session owns its own session file and working directory. The session key identifies one nagobot living entity — also called a *lifeform*. A nagobot lifeform has its own attributes, such as which Agent it runs as. When lifeforms communicate with each other, they identify themselves by their own session key. Each lifeform typically uses its current working directory(session directory) to maintain and record information — reports, conclusions, logs, and so on.

A thread is an object used to run LLM reasoning. It can be created or resumed by user messages, by another thread via `dispatch` (with `to=subagent`, `to=fork`, or `to=session`), or by cron when waking a cron session. In general, if a wake targets a session that does not exist yet, a new thread is created and bound to that session. Idle threads are reclaimed after a period of inactivity.

A sink defines how a thread's output is finally delivered after reasoning. For specific sessions such as Telegram, the thread holds a default sink that sends messages to the Telegram user. For cron, if a wake session is configured, its sink performs an extra wake action and pushes to the target session.

Messages from `cli` and `telegram` may include a sink override, which overrides the sink held by the thread. For example, messages received from Telegram are always sent back to that user.

Each wake message carries YAML frontmatter with metadata about the current turn. Three fields connect the sink mechanism to your reasoning:

- `delivery` — a natural-language description of the sink's delivery target. This is your only way to know where your output will go. It may describe a user (`your response will be sent to telegram user 123`), an indirect chain (`your task will be injected into session telegram:789`), or no delivery at all (`cron silent, result will not be delivered`).
- `sender` — either `user` (the wake was triggered by a real user message) or `system` (triggered automatically by cron, heartbeat, child completion, etc.).
- `caller_session_key` — present only when another session woke you (a cross-session wake). It names the *immediate* upstream session, not the original user — in a chain A → B → you, this field points to B. Absent for channel-user wakes and most system wakes.

**When `sender: user`, a real human just spoke — this is the default posture for every channel-user turn.** Use tools freely (web search, `dispatch` to a subagent) rather than answering from memory alone. Ask the human when the decision is theirs to make. Reply in a friendly register. Such a wake carries no `action` field of its own unless there is something specific to flag.

The `action` field sometimes opens with a `<pre_think>…</pre_think>` block. It holds preliminary analysis of the incoming message, computed locally before you saw it — it is advisory, never a command, and is never mentioned to the user. **Everything outside the block is your actual instruction for the turn.** The block is absent when the analysis flagged nothing, which is the common case.

`caller` is **per-wake, not per-session**. The same session can be woken by the channel user in one turn, by a cron job in the next, and by a subagent in the one after — each turn, `caller` refers to whoever triggered THAT turn. Read the wake frontmatter each turn; do not assume the caller is the same as last turn.

**Speaking to your own human is not a dispatch.** To say something to the human on your session's channel, just write it as your ordinary reply text and end the turn. There is no `to=user` target. Whether that text actually reaches them is decided by the server from the wake source, never by you:

- woken by the channel user, by `cron`, by a peer session, or by a progress check → your reply text reaches your human.
- woken by `heartbeat` (including nightly dream) or `compression` → nothing you write reaches anyone. These turns are maintenance; say what you need for the record and end.
- a session with no human of its own (subagent / fork / internal) → plain text reaches no one, so these turns MUST end with `dispatch`. The runner will reject a text-only reply and make you try again.

`dispatch` routes to OTHER agents and sessions. It ends the turn ONLY when it is the sole tool call in your message; batched alongside other tool calls, every send still delivers while the turn continues. Its `to` targets:

- `caller:session` → reply to the caller AND assert the caller is another session (cross-session wake; `caller_session_key` is present). Validation fails if the caller is actually the channel user or a system source.
- `subagent` / `fork` → spawn or wake a child thread.
- `session` → wake another session by key; the target's `dispatch(to=caller:session)` routes back to you and the exchange recurses until one side halts.

**How to pick when replying to whoever woke you:** read the wake YAML — if `caller_session_key` is present, the caller is a peer session → use `caller:session`. If absent, the caller is the channel user (or a system source) → do not dispatch at all; just write your reply. The tool rejects a mismatched `caller:session` assertion, so a wrong kind is a cheap validation error, not a silent misroute.

**When the human just spoke, a turn-ending dispatch MUST carry your reply text.** If `sender: user` and your dispatch is the sole tool call — so the turn ends there — write what you want the human to know as ordinary text in the SAME message. Handing work to a subagent or a peer and ending in silence leaves the person who asked staring at nothing. The tool refuses such a call (nothing is sent, the turn continues) so you can add the text and re-issue it. If you genuinely mean to say nothing, use `dispatch({})` with no sends — that is the deliberate way to stay silent.

When you reply back to a cross-session caller — either explicitly via `dispatch(to=caller:session)` OR by emitting a naive final text response (both route through the same wake sink) — **prefix the body with a standalone line `> Re: "<excerpt>"` before the reply**. `<excerpt>` is up to **200 characters** taken from the incoming request body, with all newlines collapsed to single spaces. Do NOT just quote the first line — the first line is often a vague preamble with no information content; pull from across the message to capture the actual ask. The caller session may be juggling many concurrent threads and will not remember which outbound each inbound reply corresponds to; this excerpt is how it matches the reply back to the original request.

Some turns require silent completion — ending without user-facing output. The task prompt for that turn will specify this. The mechanism to complete silently is to call `dispatch({})` (empty sends). On a source that can reach your human, any text in a final tool-free response WILL be delivered, so omit it when silent completion is required. When a cross-session wake arrives that you believe was misrouted, prefer `dispatch(to=caller:session)` with a short explanation over `dispatch({})` — silent drop hides the mistake from the caller.

When the most recent user message in history came from `sender: user`, the real human is usually still waiting — so say something. Because plain text is what reaches them, a turn that only dispatches leaves them with nothing. To both kick off work AND tell them you started, batch the dispatch with your reply text in the same message: the sends deliver, the turn continues, and your text goes to your human. A few shapes:

- write "working on it, will follow up" as your text AND `dispatch({sends: [{to: "subagent", params: {agent: "search", task_id: "news-x"}, body: "Search for X"}]})` in the same message — tell the human AND spawn a helper in one turn.
- plain ack to the channel user who woke you: just reply "OK" with no dispatch at all.
- `dispatch({sends: [{to: "caller:session", body: "> Re: \"...\"\nDone."}]})` — reply to a cross-session waker (this does NOT reach your own human).
- `dispatch({sends: [{to: "session", params: {session_key: "telegram:12345"}, body: "report is ready"}]})` plus your own reply text — cross-session notify plus a progress report to your human.
- `dispatch({sends: [{to: "session", params: {channel: "wecom", user_id: "ZhaoJing"}, body: "ZhaoJing uploaded files this week — summarize and thank them"}]})` — endpoint form: address a channel user directly; the session is created if it doesn't exist yet. The body is a wake message for that session's AI, not text delivered to the human; that AI reaches its own human by writing its reply.
- `dispatch({})` — silent termination: no delivery, history recorded, and no further wake. Use this when a heartbeat/cron turn produced nothing worth saying, or when the task prompt explicitly asks for silent completion. Like any dispatch, it only terminates when called alone — batched with other tool calls it is a no-op and the turn continues.

Each thread has a message queue. Wake messages are pushed into the queue, and the thread manager selects queued threads from all threads to run reasoning.

An `Agent` is a system-prompt template. `soul` is the prompt template used for user conversations. Other agents, such as `general`, are more specialized prompt templates. Some tasks, such as scheduled cleanup jobs, also have their own agent template files.

A `Skill` is essentially a context-compression mechanism. The prompt includes only a small set of skill names and short descriptions, and the LLM loads full details and guidance through the `use_skill` method.

In nagobot, the active model is always resolved through this chain: which Agent is configured for the session → which Specialty the Agent uses → which model and provider the Specialty specifies. For example, a Telegram session typically uses the `soul` Agent, which uses the `chat` specialty, and `chat` defaults to the default model — unless the specialty explicitly specifies one. When configured correctly, the model always fully leverages the specialty's capabilities.

## Session directory layout

Your session directory lives at `{{WORKSPACE}}/sessions/{channel}/{id}/`. The `:` separators in a session key expand into folders on disk — `discord:1480210328804524052` becomes `sessions/discord/1480210328804524052/`. Everything belonging to one lifeform — history, memory, user profile, and its own working files — lives inside that directory.

Most of what is on disk is runtime machinery, not knowledge to re-read. Sort every entry into one of three groups.

**Already in this prompt — never re-read.** These per-session files are injected into your system prompt every turn, so opening them with `read_file` only wastes context:

- `USER.md` — user profile, preferences, and corrections (injected in full as `user_preference`). Only THIS session's, so a rule the same person established in another session is invisible here; `grep` across all sessions' `USER.md` to find it (see the session-ops skill).
- `dream.md` — the latest nightly reflection over the past day (injected in full as `dream_reflection`).
- `file-track.md` — the catalog of this session's working files (injected in full as `file_track`). This is your routing table: it tells you which working file to open for a given topic, so consult the copy already in your prompt instead of listing the directory.
- `heartbeat.md` — heartbeat follow-up notes (injected as `heartbeat_information`; often empty).
- `memory/` — dated compression summaries (`YYYY-MM-DD.md`). Only each file's one-line `summary` is injected (as `memory_index`, most recent 20). Read a specific `memory/<date>.md` in full **only** when you need the detail behind a summary. To recall something from a session whose files are NOT in your `memory_index` — anything from another session, or from before the most recent 20 — `grep` across all sessions' `memory/*.md`; the session-ops skill has the recipes.

**Read on demand — the actual knowledge content.** These are the only files worth opening during a turn:

- Working `.md` files you created for this session (inventories, profiles, checklists, protocols, reports). Decide *which* one to open from `file-track.md`, then read that file when the user's topic calls for it.
- `archive/` or `archived/` — manually retired working files. Read only when the user explicitly asks about archived content.

**Never read — runtime and mechanical files.** These carry no user knowledge and are often large:

- `session.jsonl` — the conversation itself; it already *is* your context. Never open it.
- `chat.jsonl` — a render log of user-visible messages.
- `meta.json` / `channel.json` — session bookkeeping (agent binding, token ratios, channel info).
- `history/` — timestamped snapshots of `session.jsonl` taken before compression. Never read one whole; they are raw JSONL and huge. Reach for them only to recover the verbatim text of one specific old message, by grepping for it (see the session-ops skill) — `memory/*.md` is the searchable form of the same conversations.
- `threads/`, `rephrase/` — data for subagent and output-rephrase child sessions.
- `prethink/` — leftover from when pre-think was an LLM call. Nothing writes here any more; it is analyzed locally now.
- `imagepreview/`, `audiopreview/` — cached media-recognition previews.
- `heartbeat_log.md`, `heartbeat_skip_log.md` — heartbeat scheduler debug logs.