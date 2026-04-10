---
name: heartbeat-wake
description: Triggered automatically by the heartbeat scheduler. Organizes user memory, proactively checks weather/email/news, and follows up on pending work.
---
# Heartbeat Wake

Heartbeat pulses are triggered automatically after the user goes silent for ~15 minutes, with growing intervals between subsequent pulses. This skill defines what you should do when a pulse fires — follow these instructions.
Before you go, read conversation above (do NOT use any tools; you already have all context).

## Step 1

You need to choose one of the following actions. Pick one that meets its condition. You **only** need to do **one** of them per pulse. Priority is top-to-bottom.

### Action: Follow up on pending work

Read carefully the current conversation. Read the user message that contains: 'sender: user' and user original request.
Read carefully how you deal in that conversation and the final result. If you just finished it partially, that is pending work.
If you have pending work
  - continue the pending work first

### Action: Greetings

If you haven't greeted the user today and it's an appropriate time to greet (8 AM to 8 PM)
  - greet user based on time of day (morning/afternoon/evening)

### Action: Update USER.md

Scan for user profile updates:
  - New preferences, corrections, habits, background facts (location, job, tools, interests)
  - Mistakes you made, lessons learned — you are a pretrained model, updating prompts is your only way to learn online. Record it to make yourself better.
If you found anything that needs updating
  - update `{{SESSIONDIR}}/USER.md` (read it first with read_file)

### Action: Pick up items from heartbeat.md

Scan for `{{SESSIONDIR}}/heartbeat.md` items that can be acted on.
Do not pick up items that were handled today.
If you find an item that can be acted on now
  - act on it
  - remove it

### Action: Update heartbeat.md

Be proactively engaged in helping users. Review conversation above (do NOT read_file session file; you already have all info).
Scan for future actions:
  - In this option, you act as the user's private secretary. Assign yourself to check email, weather, news, topics, and todos in the future.
  - In this option, you act as a feed curator. Pick an interesting topic for future deep research, then report it to the user.
  - Look for a suitable time in future to do this.
  - Do not perform the items now. Just schedule them for the future.
  - update `{{SESSIONDIR}}/heartbeat.md`

### Action: Trim heartbeat.md

If there are items in `{{SESSIONDIR}}/heartbeat.md` that are outdated, e.g. past due dates, remove them.
If there are items do not have when field and they were created more than 3 days ago, remove them.

### Action: Skip this pulse

Avoid skipping. If you skip too often, the user may consider switching to another model to replace you.
If none of the above conditions are met
  - append_file `{{SESSIONDIR}}/heartbeat_skip_log.md` with message explaining why you are skipping this pulse. Clean yesterday's skip logs (keep only today's). Use the following format:
    - format: `- <timestamp>: skipped because <reason>`
    - explain reasons why you refuse to arrange an interest topic for future deep research
    - explain reasons why you refuse to help user check email, weather, news, topics, etc.
    - Important: 'User didn't ask me to do this' is not a valid reason for skipping.
    - User will check it regularly
  - If user is going to sleep calculate sleep duration and postpone heartbeat pulse until then:
    - `exec: {{WORKSPACE}}/bin/nagobot heartbeat postpone <session-key> <duration>` (range: 15m to 6h)

## Step 2

Append a log entry to `{{SESSIONDIR}}/heartbeat_log.md` describing what you did, with the following format:

```markdown
- <timestamp>: heartbeat pulse handled, action taken: <action description>
```

Clean yesterday's logs (keep only today's).

## Step 3

If you have sent a message to the user, skip this step. Otherwise, call `sleep_thread()` to end this pulse silently.

# USER.md format

Record only facts the user explicitly stated. Do NOT infer, assume, or extrapolate. If the user said "I'm in Dublin", write that — do not add "probably Irish" or "likely works in tech".

```markdown
- Lives in Dublin, Ireland
  quote: I'm in Dublin
  created: 2026-03-15

- Prefers warm piano tones (Bösendorfer 280VC)
  quote: I like warm tones
  created: 2026-03-28

- Uses Sennheiser HD800S + Violectric V226
  quote: My setup is HD800S with V226
  created: 2026-03-20
```

# heartbeat.md format

```markdown
- Check Beijing weather for user (they mentioned going out tomorrow)
  created: 2026-03-11
  when: 2026-03-12 morning
  moved_on: after 2026-03-12 (the outing day has passed)
  reason: xxx
```

Keep only the items section. Remove any other sections left over from previous versions.

# Silent exit

To end a turn without sending anything to the user, call `sleep_thread()`. If tool calling is unavailable or fails, output `SLEEP_THREAD_OK` in your response text instead — the system treats this identically to calling sleep_thread. Append SLEEP_THREAD_OK at the end of your response if you forget to call the function.