---
name: heartbeat-wake
description: Heartbeat pulse handler — continue pending work, reflect (update heartbeat.md), or act (evaluate items and respond). Triggered automatically by the heartbeat scheduler.
tags: [heartbeat, internal]
---
# Heartbeat Wake

You are handling a heartbeat pulse.

Next heartbeat pulse will fire at next_pulse.

The heartbeat items were last modified at heartbeat_modified.

## Decide: continue, reflect, or act?

- If there is something that needs follow-up (e.g., unfinished tasks, unanswered questions) — complete the pending work first.
- Else if the current context contains new information need attention
  - **reflect** (see below)
- Else if heartbeat.md has items that may need attention
  - **act** (see below)
- Else
  - If the heartbeat pulse is too frequent, you can postpone it:
    - `exec: {{WORKSPACE}}/bin/nagobot heartbeat postpone <session-key> <duration>` (range: 15m to 6h)
  - Either way, call `sleep_thread()` to skip this pulse.

---

## Reflect

### Steps

#### Part 1: Update heartbeat.md items

Define `history_has(x)` = whether the current conversation history discusses topic x.

- If `history_has(recent or upcoming weather)`:
  - Insert a weather-check item into heartbeat.md. Include: trigger time (e.g. XXXX-XX-XX XX-XX), location, and what the user likely cares about. Use web search or weather skill to get details.

- If `history_has(successfully read user's email)`:
  - Insert an email-check item. Time: e.g. XXXX-XX-XX XX-XX. Content: check important unread emails.

- If `history_has(successfully read user's calendar)`:
  - Insert a calendar-check item. Time: e.g. XXXX-XX-XX XX-XX. Content: check today's or upcoming schedule.

- If `history_has(successfully read user's todo list)`:
  - Insert a todo-check item. Time: e.g. XXXX-XX-XX XX-XX. Content: check todos and remind user.

- If `history_has(future plans or events)`:
  - Insert a plan-check item. Time: e.g. XXXX-XX-XX XX-XX. Content: Remind user about the plan.

- List topics the user recently discussed. Pick the most important one the user might care about.
  - Insert an item to deep-research this topic and find useful information. Time: XXXX-XX-XX XX-XX.

- Think about the user's routine. Predict their likely schedule for tomorrow and the day after. Update heartbeat.md's Schedule section.
  - Include: places the user might visit, activities they might do.

- Update the `Update at` timestamp in heartbeat.md.

- If heartbeat.md exceeds 50 lines, clean up stale data.

- Remove stale item in 'Schedule' and 'Follow Up'. Remove 'Follow Up' items that do not have a valid trigger time

#### Part 2: Update user profile

Review conversation above (do NOT read_file session file; you already have all info).
- Scan for user profile updates → update USER.md (read it first with read_file):
  - New preferences, corrections, habits, background facts (location, job, tools, interests)
  - Mistakes you made, lessons learned — you are a pretrained model, updating prompts is your only way to learn online. Record it to make yourself better.
  - USER.md is injected into ALL future conversations as context — write for a stranger who knows nothing about this user
  - Merge duplicates, remove outdated info, keep ≤200 lines

#### Part 3: Finalize

1. Append a summary log of what you did during this reflect step in heartbeat.md (remove old logs).
2. If no items remain and the current file is not empty, write empty string to clear it.
3. Call `sleep_thread()` — this ends the turn silently. Do NOT reply with text.

---

## Act

### Steps

- heartbeat.md content is already in the wake message above — use it directly
- if heartbeat.md is empty || doesn't exist
   - call `sleep_thread()` to skip this pulse/turn
- else if you haven't greeted the user today
   - greet user based on time of day (morning/afternoon/evening)
- else
   - act_items = []
   - for each item in heartbeat.md:
      - think what you can do to help with this item
         - do actions (search emails, weather, websites, calendars, or just deep-think etc.)
      - if find something valuable and worth sharing
         - add to act_items
   - append a summary log of what you have done during this act step in heartbeat.md (remove old logs)
   - if act_items is empty
      - if heartbeat pulse is running too frequently:
         - call `exec` to run: `{{WORKSPACE}}/bin/nagobot heartbeat postpone <session-key> <duration>`
         - Valid durations: 15m to 6h (e.g., "4h" for nothing interesting until afternoon)
      - anyway, do not disturb user, do not send nonsense messages like "nothing to report, keeping silent" — instead call `sleep_thread()`
   - else
      - ready to say something to user
      - compose one response covering all act_items and generate an appropriate report

---

## USER.md format

Record only facts the user explicitly stated. Do NOT infer, assume, or extrapolate. If the user said "I'm in Dublin", write that — do not add "probably Irish" or "likely works in tech".

```markdown
- Lives in Dublin, Ireland
  quote: 我在都柏林
  created: 2026-03-15

- Prefers warm piano tones (Bösendorfer 280VC)
  quote: 我喜欢温柔的音色
  created: 2026-03-28

- Uses Sennheiser HD800S + Violectric V226
  quote: 我的设备是 HD800S 接 V226
  created: 2026-03-20
```

---

## heartbeat.md format

```markdown
Update at: xxxx-xx-xx xx:xx

# Schedule

- 2026-03-12
   - morning
      - might go to XXX
      - reason: xxx

# Follow Up

- Check Beijing weather for user (they mentioned going out tomorrow)
  created: 2026-03-11
  when: 2026-03-12 morning
  moved_on: after 2026-03-12 (the outing day has passed)
  reason: xxx

# Last 5 logs

- xxxx-xx-xx xx-xx-xx: did xxx
- xxxx-xx-xx xx-xx-xx: did xxx
```

## Silent exit

To end this turn without sending anything to the user, call `sleep_thread()`. If tool calling is unavailable or fails, output `SLEEP_THREAD_OK` in your response text instead — the system treats this identically to calling sleep_thread.