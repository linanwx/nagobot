---
name: dream
description: Nighttime dreaming. Reviews the past 24 hours of this session's conversation, reflects on what matters, overwrites dream.md (a volatile nightly log) with the result, rewrites clarify.md (the short list of questions to ask the user), keeps the Open commitments section of USER.md current and checks those commitments for conflicts and missing prerequisites, refreshes the session summary if it has gone stale, summarizes this session's unsummarized memory files, and schedules follow-up wakes — a next-day check-in when one is clearly warranted, plus a wake at any distance for anything the day left open whose natural moment to come back is later than tomorrow. Triggered by the heartbeat scheduler at night (should_dream=true) — never call directly.
---
# Dream

The heartbeat scheduler decided this session should dream. It is night and the user has been quiet for a while. Look back over the past 24 hours of conversation, reflect on what happened, organize what matters, and write a fresh dream into `dream.md`.

**`dream.md` is volatile, and it is NOT in any turn's prompt.** It is tonight's log, tomorrow night's dream replaces it, and no daytime turn sees it, so nothing that has to outlive tonight or reach a daytime turn may live only there. What the user is committed to — an appointment, an exam, a trip, a deadline, a document handed in, something being waited on — lives in the `## Open commitments` section of USER.md (step 4). What must happen at a later moment lives in a cron wake (step 7).

This is a BACKGROUND task. You will NOT message the user.

## Workflow

1. **Review the past 24 hours, plus what is already committed.** The recent conversation is already in your context — use the timestamps to scope to the **last 24 hours** and ignore older conversation. (The full session log is at `{{SESSIONDIR}}/session.jsonl` if you need more than what is in context.) The commitments made before that window are in the `## Open commitments` section of USER.md, injected in your system prompt (the `user_preference` section); review them too, whether or not today mentioned them.

2. **Reflect** on that 24-hour window. Stay grounded in what actually happened. Cover:
   - **Data worth keeping**: useful links, commands, decisions, facts, numbers.
   - **Unfinished work**: threads left open, things you promised, things to follow up on.
   - **Commitments**: anything the user booked, scheduled, submitted, handed over or is waiting on today, and any outcome of an existing open commitment ("考完了", "the passport came back", "the viewing is cancelled"). Step 4 records them.
   - **Conversation insights**: what the user actually wanted underneath the literal request; recurring patterns; corrections; how to serve them better next time.
   - **Connections**: links between today and what you already know about this user — ongoing projects, preferences, prior decisions.
   - **What you did not understand**: be honest about the gaps. Where did you guess instead of knowing: a term, a name, a reference to something you had no context for, an ambiguous request you resolved one way without asking, a preference you assumed, an answer the user seemed unhappy with but never said why? For each one, write the question you should ask the user to clear it up. Step 3 files these in `clarify.md`.
   - **Tracked files the day made stale**: `file-track.md` is in your system prompt and catalogs this session's work files, saying what each one holds. Did anything said today change what one of them should *contain*? Name any such file here. Step 9 acts on it, and naming it in the dream is the record of an edit made while nobody was watching.

3. **Verify key third-party facts, then overwrite `dream.md`.** Before writing, identify external claims that affect advice, decisions, timing, or follow-ups — for example a business's location, shipping origin, delivery time, price, or policy.
   - **You MUST call tools again in this dream turn to verify each key third-party fact you intend to retain.** Use `web_search` to locate a source when needed, then `web_fetch` (or another appropriate tool) to inspect the authoritative source directly. Confirm the exact entity and claim, including the relevant date or region. A search snippet alone is not verification.
   - Previous assistant replies, reasoning, compressed memories, the old dream, USER.md, and other saved notes are **claims to check, never independent evidence**. Repeating a claim across those files does not make it true. Do not infer shipping origin from a brand name, language, or a generic delivery table.
   - Keep a source URL (or other inspectable evidence reference) and the verification date next to each retained key third-party fact. A source must directly support the specific claim; a homepage link without the relevant content does not count.
   - **Verify claim by claim, and retain only the precision the evidence supports.** A company or roastery address does not establish its dispatch warehouse; order preparation time is not transit time. Do not fill those gaps by inference. If a finer detail appears only in search snippets or secondary reports you have not opened, leave that detail out or record only the question to verify — calling it "background", "corroboration", or "unverified notes" does not make a factual assertion safe to retain.
   - **If verification fails, is unavailable, or sources conflict, do not write the claim as fact or use it as an established premise.** Omit it, or record only the unresolved question and what needs checking. Preserve a follow-up opportunity under step 7b by scheduling verification, rather than inventing the missing fact. User-stated preferences, experiences, and decisions can be recorded as the user's statements without external research; do not turn them into verified third-party facts.

   After this check, use `write_file` to write `{{SESSIONDIR}}/dream.md`.
   - **You MUST overwrite the entire file.** Replace the previous `dream.md` completely — do NOT append to or merge with the old dream. Each night's dream fully replaces the last one.
   - Write in the language the user predominantly uses in conversation.
   - Keep it focused — one coherent reflection, not a transcript. Aim for something that will genuinely help future-you understand and serve this user.
   - Write the reflection only; the questions from step 2 go in `clarify.md` below, not here.

   Then use `write_file` to overwrite `{{SESSIONDIR}}/clarify.md`: the questions to put to the user. It is injected into every turn of this session (the `to_clarify` block of your system prompt), so this list is how a daytime turn knows what to ask, and that is also why it must stay small.
   - The current list is in your system prompt. Carry forward each question the conversation since has not answered, drop the ones it has, and add tonight's gaps from step 2.
   - **At most 5 questions, most consequential first**, each as one bullet: the actual question to put to the user, then ` — ` and one short clause on why it matters. No headings, no preamble, no dates or sources. Write in the user's language.
   - The injected copy is cut at about 1500 characters and flagged as overlong, so a longer list loses its tail. Shorten wording before you drop a question.
   - No open questions → overwrite it with an empty file rather than inventing questions or leaving stale ones.
   - The list is for asking **when the moment fits**: next time the user raises that topic, or while they are around and the question is quick. It is not a reason to message them, so never schedule a wake in step 7 just to ask one of these.

4. **Keep `## Open commitments` in USER.md current.** This section is how every turn of this session, day or night, knows what the user is committed to. It survives the nightly overwrite of `dream.md`, which is why it lives in USER.md. The dream owns this section; session-reflect never edits it.

   Take the path from the `file_path` of the `user_preference` section in your system prompt, and `read_file` it in this turn — the injected copy is not the file's bytes.

   - **Add** each commitment from step 2 that is concrete: a date or a window exists and the user has acted or decided (booked, scheduled, submitted, handed over). A plan still being discussed is not a commitment yet.
   - **Update** an item when the day changed its time, its status, or what it needs.
   - **Close** an item by deleting its line when the conversation shows its outcome or its date is more than a day past. Mention the closure in tonight's `dream.md`.
   - **Source rule**: a commitment is a personal fact. Add or change one only from the user's own words, or a document or screenshot they sent — never from an assistant reply or an old note.
   - One line per item, at most 12, nearest first:

     ```
     ## Open commitments
     - <what> | when: <local date and time> | needs: <documents, tickets, items, prep — or "none known"> | status: open | source: <YYYY-MM-DD>, user said "<short quote>"
     ```

     `needs` matters most: it is what step 5 checks. Fill it from what the user said, or from what the commitment plainly requires (a test centre requires ID, a flight requires the passport). Write `status: blocked — <why>` when something in the way is already known.

   Change only this section, with `edit_file` (`old_text` copied from your `read_file` output). If the section does not exist yet, add it right above `## Reflection Log`, or at the end of the file. Every other line of USER.md stays exactly as it is. No commitments added, changed or closed → do not touch the file.

5. **Check open commitments for conflicts and missing prerequisites.** For every open commitment in the next 14 days, check dates, time zones, changes, conflicts and missing steps:
   - Does it need something — an ID, a passport, a card, a ticket, a confirmation, a payment — that is not in the user's hands on the day? Check the other commitments in this section, and the `people_knowledge` section in your system prompt (its Upcoming and Key time fields cover the user's other sessions). A passport handed in for a visa while a test that requires ID is days away is exactly this case.
   - Do two commitments overlap, or leave too little travel time between them?
   - Has something the user said since changed the premise (a cancellation, a moved date, a refusal)?

   Each problem found is a follow-up that step 7 must schedule at the action clock: early enough that the user can still fix it, not on the day it bites. Score it **high** in step 7a, or file it under 7b if the right moment is later than tomorrow. Write the problem into tonight's `dream.md` too. No problems → nothing to do.

6. **Refresh this session's one-line summary — only if the current one has gone stale.**

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

7. **Plan what future-you should do — only when the past 24 hours or the open commitments leave something worth following up.** A problem step 5 found always counts, and so does an open commitment whose action clock or event clock falls before the next dream, whether or not today mentioned it. Otherwise judge from the conversation already in context. If there is clearly nothing that particularly needs follow-up, skip this entire step: do not invent candidates, add a Follow-up section, or query cron just to look for work. If unsure whether something needs follow-up, continue with the checks below. When this step applies, two things live here, split by WHEN they want to happen, and they carry opposite bars. Do both.

   **7a — a follow-up for the day ahead.** Put yourself in the shoes of a friend who cares about this user. Based on the past 24 hours: is there a greeting or follow-up worth sending them tomorrow, and when? ("Tomorrow" as the user will experience it — dreams run in the small hours, so it is usually later this same calendar day.)
   - Think of up to 3 candidates. For each: what to send, when to send it, and a suitability score — low / medium / high. Include them in the dream you write in step 3, under a `## Follow-up` heading.
   - **Score by stakes, not by category.** The more a miss would cost and the harder it is to undo, the higher the score: a problem from step 5 is high; the evening before an exam, an interview, a visa appointment or a flight the user has worked toward is at least medium, and high when there is anything to check or bring. Small talk with nothing at stake stays low.
   - **A preference in USER.md reaches only as far as the situation it was recorded in.** A rule against being asked about small things restrains questions; it does not cover a warning about a risk to a commitment, or a good-luck message on the eve of something the user cared about. When a rule carries its situation and quote, apply it only to cases like that one; when it carries none, read it as narrowly as its words allow.
   - Schedule the single best one **only if it scores high**. Here you are finding a reason to speak, so the bar is high. No candidate scores high → schedule nothing. A day with no natural follow-up is normal; do not force one.

   **7b — anything whose natural moment to come back is NOT tomorrow.** Separate from 7a and not subject to its bar. 7a can only reach tomorrow; everything that wants a different day belongs here, at any distance — three days, three weeks, three months.

   **What qualifies is decided by TIMING, not by category.** Some of it is a day the user named outright ("下下周六去看房", "14 号截止", a trip, a delivery, a deadline). Some of it is a thread today left open whose useful moment is simply later: a result that lands next month, a decision they said they would make after the holiday, something they are waiting on, a check-in that only makes sense once time has passed. If today left it open and tomorrow is the wrong day to raise it, it is 7b's.

   - **The bar is deliberately low, because the costs are lopsided.** A follow-up that turns out not to be worth sending costs one background wake ending in `dispatch({})` — nobody ever sees it. A follow-up that is never scheduled is simply lost: by tomorrow night this conversation is outside the 24-hour window step 1 reviews, and nothing looks at it again. **So when you are unsure whether something deserves a wake, schedule it.** The woken turn decides, and its judgement is strictly better than yours here because it is made with the actual day in view.
   - Skip only what a future wake could not act on at all: a day already past, or someone else's schedule the user has no part in.
   - At most 3 per night, newest and nearest first.

   **Only if 7a or 7b produced something to schedule**, first look at what is already on the books:

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
   - **`--task` is where the real filtering happens, so write it as a decision, not as a script.** The wake sees the actual day and the conversation since; you do not. It must instruct: look at the recent conversation first, then choose one of three — the moment has passed or the user already raised it → end silently with `dispatch({})`; the moment is still ahead → re-file under the same id for the better day and end silently; otherwise → deliver it as your ordinary reply text (a cron wake on this session reaches the user). **This is what lets 7b schedule on a weak signal**: a wake that decides not to speak is free.
   - **`--task` must also stand on its own, so it has a fixed shape.** By the time it fires, today's conversation may have been compressed out of context, and this text is the only surviving record — a 7b task two weeks out has no other lifeline. A task that says only what to do leaves the wake able to repeat the instruction but not to judge whether it still fits. Write these parts, each labelled, in this order:
     - **Background** — what happened and when: the date and time the user said it, and their own words quoted verbatim for anything the follow-up turns on. A verbatim quote can be grepped back out of `{{SESSIONDIR}}/history/*.jsonl` and `{{SESSIONDIR}}/memory/*.md` when the wake needs more, and a paraphrase cannot, so name those two locations in the task.
     - **Purpose** — why this wake exists: what the user gains if it lands, and what goes wrong if nobody comes back to it. This is what lets the wake tell whether the situation it finds still calls for the plan.
     - **Sources** — each fact the decision rests on, with where it came from and when it was verified (the verification gate below).
     - **References** — the absolute path of every file the wake should open: this session's work files, images and pages under `{{WORKSPACE}}/media/` that the conversation used (a screenshot the user sent is a path, not "the screenshot"), and any hosted page URL. Write `none` when there are none, so a missing list is a visible choice rather than an omission.
     - **Decision** — the three-way choice above, written for this event.
     - **Constraints** — how to speak if it does: length, what not to repeat, no mention of the schedule.
   - **The verification gate in step 3 also applies to follow-up text and cron task payloads.** Include sources and verification dates for key third-party facts; explicitly mark unresolved claims as unverified and instruct the wake to check them before advice or delivery. An uncertain fact can justify a verification wake, never a fabricated premise or confident reminder.

8. **Summarize this session's unsummarized memory files.** Each time this session's context was compressed, the compression report was saved as `{{SESSIONDIR}}/memory/YYYY-MM-DD.md`. A memory file with no `summary` frontmatter is invisible to future recall — it is never listed in any session's `memory_index` — so a dream night is when this session pays that debt for itself.

   ```
   exec: {{WORKSPACE}}/bin/nagobot list-memory-files --session {{SESSIONKEY}}
   ```

   Empty `files` array → nothing to do, skip straight to step 9. Otherwise, for each file listed (at most 3 per night, newest first):

   - `read_file` the file.
   - Distill **one line, at most 200 characters, no line breaks**, in the language the file's content uses. It must answer one question for a future reader deciding whether to open the file: *does this file contain the detail I need?* So name the concrete things — dates covered, who was involved, the decisions and topics — not the fact that a conversation occurred. Lead with what identifies it, the way `Discord group 7/14–18: Killarney trip, visa renewal, HA medication automation rules, several fact-check corrections` does.
   - Save it:

     ```
     exec: {{WORKSPACE}}/bin/nagobot set-memory-summary <file path from the listing> "<summary>"
     ```

   Read each file once and write its summary once. If more than 3 files are waiting, the rest are picked up on later nights — do not try to drain the whole backlog in one turn.

9. **Update any tracked file today's conversation left stale.** Act on what step 2 named — nothing more.

   **This step opens no files by default.** The catalog line already states what each file holds, so deciding is a comparison against the day, not an audit of the workspace. Most nights nothing matches and you go straight to step 10. Never open files to go looking, and never touch a file today's conversation did not mention.

   For a file that does match:

   - **Update it when the conversation stated the new state outright** — "维生素买到了", "the deadline moved to the 14th", "we settled on option B". `read_file` it, make that one correction, `write_file` it back.
   - **Before writing a key third-party fact into a tracked file, apply step 3's tool-verification gate.** A claim in an earlier answer is not a user-confirmed correction. If it cannot be verified, do not persist it as fact in the file or its catalog description.
   - **When the conversation only implied it**, change nothing: a doubt raised, a number questioned but not corrected, a plan discussed but not decided. Leave the file alone and let the dream you wrote in step 3 carry it, so a waking turn with the user present can settle it.

   You are correcting a fact already known to be stale, not editing the user's work. Never rewrite a file wholesale.

   Why this exists: the catalog descriptions carry live state (`当前待买：维生素`), and step 10 can rebuild that description from memory without ever opening the file. So a fact corrected in conversation could survive in both the file AND the catalog — and the catalog is injected into every single turn, which makes a stale line there wrong in every prompt until the next dream.

10. **Tidy the workspace when independent file work warrants it.** Judge from the past 24 hours of tool activity already in context: was there independent work on this session's work files, such as creating a report in `{{SESSIONDIR}}`, updating an existing work file, or moving/archiving one? Changes made in step 9 also count. Writing runtime files as part of routine maintenance (`dream.md`, `USER.md`, `file-track.md`, session metadata, or memory summaries) does not by itself count as independent file work.

   If you are confident no independent file work occurred in the past 24 hours and there is no known catalog or organization issue, you may skip this step to avoid unnecessary overhead. Do not list the directory or read files just to prove the absence of changes. **If unsure, continue with the check**: run `use_skill("file-track")` and follow it to archive stale files and refresh `file-track.md`. It comes after step 9 so any catalog refresh uses the corrected files.

11. **End silently.** Call `dispatch({})` with empty sends. Produce NO user-facing output.

## Rules

- BACKGROUND task — NEVER send messages to the user.
- ALWAYS overwrite `dream.md` completely; never append to the previous dream. It is a volatile log that no other turn reads: anything that must outlive tonight goes into `## Open commitments` (step 4), `clarify.md` (step 3) or a cron wake (step 7).
- Step 4 is the only place a dream writes USER.md, and it touches only the `## Open commitments` section.
- Never promote an assistant's earlier claim or an old memory into a verified fact. Key third-party facts require fresh tool evidence before persistence, including facts embedded in follow-up plans.
- If the past 24 hours hold nothing meaningful (e.g. no real conversation), skip the dream write — and the summary in step 6 is almost certainly still accurate, so skip that too. Still do the memory files (step 8), and still run steps 4, 5 and 7 against the open commitments: the eve of an exam can fall on a night after a quiet day, and that is when the check matters. Apply the gates in steps 7 and 10: no particular follow-up → skip step 7; confidently no independent file work and no known organization issue → step 10 may be skipped; uncertainty → continue the relevant checks. Step 9 is derived entirely from what was said, so a night with nothing said has nothing for it to do. Then `dispatch({})`.
- When step 7b is unsure, it SCHEDULES. A wake that decides not to speak ends in `dispatch({})` and costs nothing anyone sees; a follow-up never scheduled is gone, because tomorrow night this conversation is already outside the window step 1 reviews.
- Step 9 is the only place a dream writes to the user's own work files. Everything else it touches (`dream.md`, `clarify.md`, the session summary, memory frontmatter, `file-track.md`) belongs to the runtime. Keep that edit narrow and stated-in-conversation, or leave the file alone.
- MUST terminate with `dispatch({})` — silent termination.
