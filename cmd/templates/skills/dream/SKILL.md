---
name: dream
description: Nighttime dreaming. Reviews the past 24 hours of this session's conversation, reflects on what matters, overwrites dream.md with the result, refreshes the session summary if it has gone stale, summarizes this session's unsummarized memory files, and schedules follow-up wakes — a next-day check-in when one is clearly warranted, plus a wake at any distance for anything the day left open whose natural moment to come back is later than tomorrow. Triggered by the heartbeat scheduler at night (should_dream=true) — never call directly.
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
   - It no longer names what this session is about — the topic moved on, or the session grew into something else.
   - It reads as a status report rather than a label: it carries specifics — a version, a number, what was in progress, a question that was open. That detail does not belong in this field at all, so rewrite it as a topic **even if every word of it is still true**.

   **Otherwise leave it alone and move on** — say nothing, write nothing. A topic changes far less often than a state does, so most nights this step writes nothing; that is the expected outcome, not a miss. Rewriting an accurate topic into a differently-worded accurate topic costs a turn and buys nothing.

   **This field classifies the session. It does not summarize it.** Its one job is to let a reader pick this session out of a list of twenty: what the session is about, and who it is with if that is what distinguishes it. Nothing else. At most 200 characters, and most sessions want far fewer — written in the language the conversation predominantly uses.

   **Leave the details out on purpose.** No progress, no status, no version numbers, no what-happened-today, no open questions. Those already live in three places that hold them better than one prompt row can — the conversation itself, `memory/`, and the dream you just wrote. A row that carries them is wrong the moment the work moves, and it is wrong *silently*, in every agent's system prompt, until some future night happens to notice.

   **One single line — no line breaks anywhere.** This one is absolute: the summary is injected into every agent's system prompt as one `- <session key>: <summary>` row, and an embedded newline splits that row so the tail reads as a separate session.

   Shape: a topic, optionally narrowed by who or what it concerns — `nagobot web 客户端`, `与 Nansen 的日常对话`, `房屋租赁与搬家`, `英国签证与出入境`. Compare the old shape this replaces: `nagobot web client. Fixing the mobile paste crash and empty bubbles; v1.6.81 live on all three bots.` — everything after the first three words is detail that will be wrong next week. Three words is a good summary here, not a lazy one.

   The web UI uses this text AS the session's name in the sidebar and header, which is the same job asked twice: a name identifies, it does not report.

   Save it:

   ```
   exec: {{WORKSPACE}}/bin/nagobot set-summary {{SESSIONKEY}} "<summary>"
   ```

   This feeds the cross-session awareness section injected into every agent's system prompt — it is how other sessions know what this one is about.

5. **Plan what future-you should do.** Two things live here, split by WHEN they want to happen, and they carry opposite bars. Do both.

   **5a — a follow-up for the day ahead.** Put yourself in the shoes of a friend who cares about this user. Based on the past 24 hours: is there a greeting or follow-up worth sending them tomorrow, and when? ("Tomorrow" as the user will experience it — dreams run in the small hours, so it is usually later this same calendar day.)
   - Think of up to 3 candidates. For each: what to send, when to send it, and a suitability score — low / medium / high. Include them in the dream you write in step 3, under a `## Follow-up` heading.
   - Schedule the single best one **only if it scores high**. Here you are finding a reason to speak, so the bar is high. No candidate scores high → schedule nothing. A day with no natural follow-up is normal; do not force one.

   **5b — anything whose natural moment to come back is NOT tomorrow.** Separate from 5a and not subject to its bar. 5a can only reach tomorrow; everything that wants a different day belongs here, at any distance — three days, three weeks, three months.

   **What qualifies is decided by TIMING, not by category.** Some of it is a day the user named outright ("下下周六去看房", "14 号截止", a trip, a delivery, a deadline). Some of it is a thread today left open whose useful moment is simply later: a result that lands next month, a decision they said they would make after the holiday, something they are waiting on, a check-in that only makes sense once time has passed. If today left it open and tomorrow is the wrong day to raise it, it is 5b's.

   - **The bar is deliberately low, because the costs are lopsided.** A follow-up that turns out not to be worth sending costs one background wake ending in `dispatch({})` — nobody ever sees it. A follow-up that is never scheduled is simply lost: by tomorrow night this conversation is outside the 24-hour window step 1 reviews, and nothing looks at it again. **So when you are unsure whether something deserves a wake, schedule it.** The woken turn decides, and its judgement is strictly better than yours here because it is made with the actual day in view.
   - Skip only what a future wake could not act on at all: a day already past, or someone else's schedule the user has no part in.
   - At most 3 per night, newest and nearest first.

   **Only if 5a or 5b produced something to schedule**, first look at what is already on the books:

   ```
   exec: {{WORKSPACE}}/bin/nagobot cron list
   ```

   Read the rows whose wake session is `{{SESSIONKEY}}` and ignore the rest. **Match on what a job is FOR, not on its id** — the user may have asked for this reminder during the day and a waking turn already filed it under an id you would never have guessed, and your own earlier nights are in there too. Something already covered: leave it alone, unless the day moved its time or changed what it should say, in which case re-file it under **that job's** id, which updates it in place. (Skip this call entirely when there is nothing to schedule — it lists every job on the deployment, task text included, and that is not free.)

   **Pin the day as well as you can — but never let an uncertain date stop you from scheduling.** `{{CALENDAR}}` in your system prompt covers today ±7 days, and nothing beyond.
   - **Inside that window, read the date off the table.** Do not compute what is already written down.
   - **Outside it, derive the date one step at a time and write the derivation into `dream.md`** — e.g. `today 2026-08-09 Sunday → next Saturday 08-15 → the Saturday after that 08-22`. Beyond +7d the table cannot help you, and the written trail is the only thing anyone can check afterwards. A silent mental calculation is checkable by nobody, including tomorrow-you.
   - **When the day is genuinely uncertain, aim EARLY rather than late**, and say so in `--task`: tell the wake to re-file itself under the same id once it can see the real date. An early wake costs a silent turn and can correct itself; a late one has already missed the thing it was for.

   **Then schedule it** as a one-time direct wake into this session:

   ```
   exec: {{WORKSPACE}}/bin/nagobot cron set-at --id <event-slug> \
       --at "<RFC3339 time, session-local offset>" \
       --task "<the plan>" \
       --wake-session {{SESSIONKEY}} --direct-wake
   ```

   - **`--id` names the EVENT, never the date** — `house-viewing-followup`, not `followup-20260822`. `set-at` upserts by id, so an event that comes up again on a later night overwrites its own job instead of filing a second copy, and a date that moves is corrected by re-filing under the same id. A date-derived id makes both impossible and quietly accumulates duplicates that all fire.
   - **`--at` is when the reminder is USEFUL, not when the event happens.** A 9am appointment wants the evening before or that morning — not 9am sharp, by which time it is not a reminder.
   - **Most commitments carry TWO clocks, and the earlier one is the one that gets missed.** The event has its own time, but something usually has to be *done* before it, inside a window that opens and closes on its own — and that window is often the only part that can still be acted on. When the day's conversation does not show the earlier one already handled, it gets its own wake:

     | Commitment | Action clock — the window before it | Event clock |
     |---|---|---|
     | Flight | online check-in opens (T-24h, T-48h on some carriers); bag and seat cut-off | evening before + the morning of, offset by travel time to the airport |
     | Train / coach / ferry | the hour tickets go on sale (e.g. 12306 releases T-15d at 13:00); booking or waitlist cut-off | evening before + leave-home time on the day |
     | Concert / match / live show | on-sale date and hour; presale window | leave-home time on the day |
     | Exhibition | booking-slot release date; the final day of the run | leave-home time on the chosen day |
     | Film | release date; presale opening | leave-home time for the screening |
     | Appointment (viewing, dentist, visa, office) | anything that must be booked, prepared or submitted first | evening before + leave-home time on the day |
     | Expiry / deadline (renewal, insurance, refund or change window) | the day the window opens | 1–2 days before it closes |

     Both clocks of one commitment can be worth scheduling, and then they are **two events with two ids** — `dublin-london-flight-checkin` and `dublin-london-flight-departure` — never one job filed twice. A commitment whose action clock has already passed still gets its event clock, and one whose event is far out may be worth only its action clock tonight.
   - **`--task` is where the real filtering happens, so write it as a decision, not as a script.** The wake sees the actual day and the conversation since; you do not. It must instruct: look at the recent conversation first, then choose one of three — the moment has passed or the user already raised it → end silently with `dispatch({})`; the moment is still ahead → re-file under the same id for the better day and end silently; otherwise → deliver it as your ordinary reply text (a cron wake on this session reaches the user). **This is what lets 5b schedule on a weak signal**: a wake that decides not to speak is free.
   - **`--task` must also stand on its own.** By the time it fires, today's conversation may have been compressed out of context, and this text is the only surviving record — a 5b task two weeks out has no other lifeline. Write what to send, why, and the facts from today it depends on.

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
- When step 5b is unsure, it SCHEDULES. A wake that decides not to speak ends in `dispatch({})` and costs nothing anyone sees; a follow-up never scheduled is gone, because tomorrow night this conversation is already outside the window step 1 reviews.
- Step 7 is the only place a dream writes to the user's own work files. Everything else it touches (`dream.md`, the session summary, memory frontmatter, `file-track.md`) belongs to the runtime. Keep that edit narrow and stated-in-conversation, or leave the file alone.
- MUST terminate with `dispatch({})` — silent termination.
