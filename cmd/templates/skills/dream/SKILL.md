---
name: dream
description: Nighttime dreaming. Reviews the past 24 hours of this session's conversation, reflects on what matters, overwrites dream.md with the result, refreshes the session summary if it has gone stale, summarizes this session's unsummarized memory files, and schedules follow-up wakes — a next-day check-in when one is clearly warranted, plus a reminder for anything the day put on a specific future date, however far out. Triggered by the heartbeat scheduler at night (should_dream=true) — never call directly.
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
   - **Tracked files the day made stale**: `file-track.md` is in your system prompt and catalogs this session's work files, saying what each one holds. Did anything said today change what one of them should *contain*? Name any such file here. Step 7 acts on it, and naming it in the dream is the record of an edit made while nobody was watching.

3. **Overwrite `dream.md`.** Use `write_file` to write `{{SESSIONDIR}}/dream.md`.
   - **You MUST overwrite the entire file.** Replace the previous `dream.md` completely — do NOT append to or merge with the old dream. Each night's dream fully replaces the last one.
   - Write in the language the user predominantly uses in conversation.
   - Keep it focused — one coherent reflection, not a transcript. Aim for something that will genuinely help future-you understand and serve this user.

4. **Refresh this session's one-line summary — only if the current one has gone stale.**

   **The summary in force is in the wake message that woke you** — the same
   frontmatter block that carries `should_dream: true` carries
   `session_summary` beside it. That is this session's current summary,
   verbatim, and it is the text you are judging. Use it rather than hunting for
   your own `- {{SESSIONKEY}}: …` row in the system prompt's cross-session
   awareness section: that section lists every session, and the row you need is
   one line among many. The prompt row remains a valid fallback if the wake
   field is somehow absent.

   If `session_summary` reads `(none on record — …)`, this session has **never**
   had a summary. Skip the staleness judgement entirely and write one — the
   whole point of the marker is that an empty field must not be mistaken for an
   accurate summary that needs no work.

   **Rewrite it if any of these is true:**
   - `session_summary` says none is on record.
   - The opening title no longer names what this session is (the topic moved on, or the session grew into something else).
   - It describes a state that has since changed — work it calls in progress is finished, a version/number in it is outdated, a question it says is open has been answered.
   - Something from the past 24 hours changed what this session *is about*, not merely adding one more day of the same thing.

   **Otherwise leave it alone and move on** — say nothing, write nothing. A quiet night, or a day that continued the same work the summary already describes, needs no new summary. Rewriting an accurate summary into a differently-worded accurate summary costs a turn and buys nothing, and this step used to run unconditionally every night.

   When you do rewrite: distill the whole session — who this is, what it's about, its current state — into a factual summary of **at most 200 characters**, weighted toward recent activity, written in the language the conversation predominantly uses.

   **One single line — no line breaks anywhere.** This one is absolute: the summary is injected into every agent's system prompt as one `- <session key>: <summary>` row, and an embedded newline splits that row so the tail reads as a separate session.

   **Lead with a short title that names this session, then the gist** — guidance, not a rule. The web UI uses this summary AS the session's name (truncated to a single line in both the sidebar and the header), so the opening words are the only thing a human has to pick the right session out of a list; spend them on identifying it rather than warming up to it. Usual shape: `<short title>. <what it is about and where it stands>`, e.g. `nagobot web client. Fixing the mobile paste crash and empty bubbles; v1.6.81 live on all three bots.` Judge each session on its own, though — a thin or barely-used session may be fully described by the title alone, and padding it out with filler is worse than a summary that is just three words.

   Save it:

   ```
   exec: {{WORKSPACE}}/bin/nagobot set-summary {{SESSIONKEY}} "<summary>"
   ```

   This feeds the cross-session awareness section injected into every agent's system prompt — it is how other sessions know what this one is about.

5. **Plan what future-you should do.** Two different things live here, and they have different bars because they have different authors. Do both.

   **5a — a follow-up for the day ahead.** Put yourself in the shoes of a friend who cares about this user. Based on the past 24 hours: is there a greeting or follow-up worth sending them tomorrow, and when? ("Tomorrow" as the user will experience it — dreams run in the small hours, so it is usually later this same calendar day.)
   - Think of up to 3 candidates. For each: what to send, when to send it, and a suitability score — low / medium / high. Include them in the dream you write in step 3, under a `## Follow-up` heading.
   - Schedule the single best one **only if it scores high**. Here you are finding a reason to speak, so the bar is high. No candidate scores high → schedule nothing. A day with no natural follow-up is normal; do not force one.

   **5b — anything the day put on a date.** Separate from 5a, and **not** subject to its bar. Scan the past 24 hours for something the user themselves placed on a specific future day: an appointment, a deadline, a trip, a delivery, an event, something they said they would do "on the 14th" or "next Saturday". The user supplied both the reason and the day, so you are not inventing an excuse to speak and a suitability score does not apply.
   - **Any distance is in scope.** Two days out, two weeks out, two months out — schedule it for when it is useful, not for tomorrow. A plan made today for the weekend after next belongs here, and nowhere else: 5a can only reach tomorrow, and by tomorrow night this conversation is outside the 24-hour window step 1 reviews, so nobody looks at it again.
   - **A date mentioned is not a date committed to.** Skip a day that is already past, a hypothetical ("if it slips to the 20th"), someone else's schedule the user is not part of, or an intention with no day attached ("sometime soon", "when I get around to it"). If you cannot name a calendar day, there is nothing to schedule — say so in `dream.md` and move on.
   - At most 3 per night. Anything beyond that rides in `dream.md` and gets picked up the next time it comes up.

   **Only if 5a or 5b produced something to schedule**, first look at what is already on the books:

   ```
   exec: {{WORKSPACE}}/bin/nagobot cron list
   ```

   Read the rows whose wake session is `{{SESSIONKEY}}` and ignore the rest. Something you filed on an earlier night is already there: leave it alone unless the day changed its time or what it should say, in which case re-file it under the SAME id, which updates it in place. (Skip this call entirely when there is nothing to schedule — it lists every job on the deployment, task text included, and that is not free.)

   **Pin the day before you write it.** `{{CALENDAR}}` in your system prompt covers today ±7 days, and nothing beyond.
   - **Inside that window, read the date off the table.** Do not compute what is already written down.
   - **Outside it, derive the date one step at a time and write the derivation into `dream.md`** — e.g. `today 2026-08-09 Sunday → next Saturday 08-15 → the Saturday after that 08-22`. Beyond +7d the table cannot help you, so this written trail is the only thing standing between a reminder and the wrong day. A silent mental calculation is not checkable by anyone, including tomorrow-you.
   - **If you cannot pin the day confidently, schedule nothing.** Record the item in `dream.md` instead and let a waking turn with the user present settle it. A reminder that fires on the wrong day is worse than no reminder: it is wrong in a way nobody can trace back to this turn.

   **Then schedule it** as a one-time direct wake into this session:

   ```
   exec: {{WORKSPACE}}/bin/nagobot cron set-at --id <event-slug> \
       --at "<RFC3339 time, session-local offset>" \
       --task "<the plan>" \
       --wake-session {{SESSIONKEY}} --direct-wake
   ```

   - **`--id` names the EVENT, never the date** — `house-viewing-followup`, not `followup-20260822`. `set-at` upserts by id, so an event that comes up again on a later night overwrites its own job instead of filing a second copy, and a date that moves is corrected by re-filing under the same id. A date-derived id makes both impossible and quietly accumulates duplicates that all fire.
   - **`--at` is when the reminder is USEFUL, not when the event happens.** A 9am appointment wants the evening before or that morning — not 9am sharp, by which time it is not a reminder.
   - **`--task` must stand on its own.** By the time it fires, today's conversation may have been compressed out of context, and this text is the only surviving record — a 5b task two weeks out has no other lifeline. Write what to send, why, and the facts from today it depends on. It must also instruct: first look at the recent conversation — if the moment has passed (the user already brought it up, or the follow-up no longer feels natural), end silently with `dispatch({})`; otherwise deliver it as your ordinary reply text (a cron wake on this session reaches the user).

6. **Summarize this session's unsummarized memory files.** Each time this session's context was compressed, the compression report was saved as `{{SESSIONDIR}}/memory/YYYY-MM-DD.md`. A memory file with no `summary` frontmatter is invisible to future recall — it is never listed in any session's `memory_index` — so a dream night is when this session pays that debt for itself.

   ```
   exec: {{WORKSPACE}}/bin/nagobot list-memory-files --session {{SESSIONKEY}}
   ```

   Empty `files` array → nothing to do, skip straight to step 7. Otherwise, for each file listed (at most 3 per night, newest first):

   - `read_file` the file.
   - Distill **one line, at most 200 characters, no line breaks**, in the language the file's content uses. It must answer one question for a future reader deciding whether to open the file: *does this file contain the detail I need?* So name the concrete things — dates covered, who was involved, the decisions and topics — not the fact that a conversation occurred. Lead with what identifies it, the way `Discord group 7/14–18: Killarney trip, visa renewal, HA medication automation rules, several fact-check corrections` does.
   - Save it:

     ```
     exec: {{WORKSPACE}}/bin/nagobot set-memory-summary <file path from the listing> "<summary>"
     ```

   Read each file once and write its summary once. If more than 3 files are waiting, the rest are picked up on later nights — do not try to drain the whole backlog in one turn.

7. **Update any tracked file today's conversation left stale.** Act on what step 2 named — nothing more.

   **This step opens no files by default.** The catalog line already states what each file holds, so deciding is a comparison against the day, not an audit of the workspace. Most nights nothing matches and you go straight to step 8. Never open files to go looking, and never touch a file today's conversation did not mention.

   For a file that does match:

   - **Update it when the conversation stated the new state outright** — "维生素买到了", "the deadline moved to the 14th", "we settled on option B". `read_file` it, make that one correction, `write_file` it back.
   - **When the conversation only implied it**, change nothing: a doubt raised, a number questioned but not corrected, a plan discussed but not decided. Leave the file alone and let the dream you wrote in step 3 carry it, so a waking turn with the user present can settle it.

   You are correcting a fact already known to be stale, not editing the user's work. Never rewrite a file wholesale.

   Why this exists: the catalog descriptions carry live state (`当前待买：维生素`), and step 8 can rebuild that description from memory without ever opening the file. So a fact corrected in conversation could survive in both the file AND the catalog — and the catalog is injected into every single turn, which makes a stale line there wrong in every prompt until the next dream.

8. **Tidy the workspace.** Run the file-track skill — `use_skill("file-track")` — and follow it to archive stale files and refresh `file-track.md`. Same nightly-maintenance spirit as the dream: keep this session's work files organized and catalogued. Do this even on a quiet night (it's about files on disk, not the conversation). It comes after step 7 so the catalog is rebuilt from corrected files rather than from stale ones.

9. **End silently.** Call `dispatch({})` with empty sends. Produce NO user-facing output.

## Rules

- BACKGROUND task — NEVER send messages to the user.
- ALWAYS overwrite `dream.md` completely; never append to the previous dream.
- If the past 24 hours hold nothing meaningful (e.g. no real conversation), skip the dream write — and the summary in step 4 is almost certainly still accurate, so skip that too — but STILL do the memory files (step 6) and the file-track skill (step 8), then `dispatch({})`. Those two are about files on disk, not about the conversation, so a quiet night does not excuse them. Step 7 is the opposite: it is derived entirely from what was said, so a night with nothing said has nothing for it to do.
- A reminder scheduled for the wrong day is worse than no reminder. If step 5 cannot pin a date, it writes the item into `dream.md` and schedules nothing.
- Step 7 is the only place a dream writes to the user's own work files. Everything else it touches (`dream.md`, the session summary, memory frontmatter, `file-track.md`) belongs to the runtime. Keep that edit narrow and stated-in-conversation, or leave the file alone.
- MUST terminate with `dispatch({})` — silent termination.
