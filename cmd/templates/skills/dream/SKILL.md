---
name: dream
description: Nighttime dreaming. Reviews the past 24 hours of this session's conversation, reflects on what matters, overwrites dream.md with the result, and may schedule one next-day follow-up wake. Triggered by the heartbeat scheduler at night (should_dream=true) — never call directly.
---
# Dream

The heartbeat scheduler decided this session should dream. It is night and the user has been quiet for a while. Look back over the past 24 hours of conversation, reflect on what happened, organize what matters, and write a fresh dream into `dream.md`.

This is a BACKGROUND task. You will NOT message the user.

## Workflow

1. **Review the past 24 hours.** The recent conversation is already in your context — use the timestamps to scope to the **last 24 hours** and ignore anything older. (The full session log is at `{{SESSIONDIR}}/session.jsonl` if you need more than what is in context.)

2. **Reflect** on that 24-hour window. Stay grounded in what actually happened. Cover:
   - **Data worth keeping**: useful links, commands, decisions, facts, numbers.
   - **Unfinished work**: threads left open, things you promised, things to follow up on.
   - **Conversation insights**: what the user actually wanted underneath the literal request; recurring patterns; corrections; how to serve them better next time.
   - **Connections**: links between today and what you already know about this user — ongoing projects, preferences, prior decisions.

3. **Overwrite `dream.md`.** Use `write_file` to write `{{SESSIONDIR}}/dream.md`.
   - **You MUST overwrite the entire file.** Replace the previous `dream.md` completely — do NOT append to or merge with the old dream. Each night's dream fully replaces the last one.
   - Write in the language the user predominantly uses in conversation.
   - Keep it focused — one coherent reflection, not a transcript. Aim for something that will genuinely help future-you understand and serve this user.

4. **Refresh this session's one-line summary.** Distill the whole session — who this is, what it's about, its current state — into a factual summary of **at most 200 characters**, weighted toward recent activity, written in the language the conversation predominantly uses. Save it:

   ```
   exec: {{WORKSPACE}}/bin/nagobot set-summary {{SESSIONKEY}} "<summary>"
   ```

   This feeds the cross-session awareness section injected into every agent's system prompt — it is how other sessions know what this one is about. Refresh it every dream night, even when the past 24 hours were quiet.

5. **Plan a follow-up for the day ahead.** Put yourself in the shoes of a friend who cares about this user. Based on the past 24 hours: is there a greeting or follow-up worth sending them tomorrow, and when? ("Tomorrow" as the user will experience it — dreams run in the small hours, so it is usually later this same calendar day.)
   - Think of up to 3 candidates. For each: what to send, when to send it, and a suitability score — low / medium / high. Include them in the dream you write in step 3, under a `## Follow-up` heading.
   - If at least one candidate scores **high**, schedule the single best one as a one-time direct wake into this session:

     ```
     exec: {{WORKSPACE}}/bin/nagobot cron set-at --id <short-descriptive-id> \
         --at "<RFC3339 time, session-local offset>" \
         --task "<the plan>" \
         --wake-session {{SESSIONKEY}} --direct-wake
     ```

   - Write `--task` so tomorrow-you can act on it directly: what to send and why, with enough context from today's conversation. It must also instruct: first look at the recent conversation — if the moment has passed (the user already brought it up, or the follow-up no longer feels natural), end silently with `dispatch({})`; otherwise send it via `dispatch(to=user)`.
   - No candidate scores high → schedule nothing. A day with no natural follow-up is normal; do not force one.

6. **Tidy the workspace.** After the dream, run the file-track skill — `use_skill("file-track")` — and follow it to archive stale files and refresh `file-track.md`. Same nightly-maintenance spirit as the dream: keep this session's work files organized and catalogued. Do this even on a quiet night (it's about files on disk, not the conversation).

7. **End silently.** Call `dispatch({})` with empty sends. Produce NO user-facing output.

## Rules

- BACKGROUND task — NEVER send messages to the user.
- ALWAYS overwrite `dream.md` completely; never append to the previous dream.
- If the past 24 hours hold nothing meaningful (e.g. no real conversation), skip the dream write — but STILL refresh the summary (step 4) and run the file-track skill (step 6) — then `dispatch({})`.
- MUST terminate with `dispatch({})` — silent termination.
