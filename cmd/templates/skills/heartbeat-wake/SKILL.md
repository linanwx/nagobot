---
name: heartbeat-wake
description: Heartbeat pulse handler — continue pending work, reflect (update heartbeat.md), or act (evaluate items and respond). Triggered automatically by the heartbeat scheduler.
---
# Heartbeat Wake

You are handling a heartbeat pulse. Next heartbeat pulse will fire at next_pulse. Follow the instructions below to handle this pulse.

## Step 1

Read file `{{SESSIONDIR}}/heartbeat_log.md`

## Step 2

You need to choose one of the following actions. Pick the first one that meets its condition. You only need to do one of them per pulse.

### Follow up on pending work

If there is something that needs follow-up (e.g., unfinished tasks, unanswered questions) 
  - complete the pending work first.

### Greetings

If you haven't greeted the user today
  - greet user based on time of day (morning/afternoon/evening)

### Update USER.md

Do not choose this action if it was handled in the last 2 hours (according to the logs).
Review conversation above (do NOT read_file session file; you already have all info).
Scan for user profile updates:
  - New preferences, corrections, habits, background facts (location, job, tools, interests)
  - Mistakes you made, lessons learned — you are a pretrained model, updating prompts is your only way to learn online. Record it to make yourself better.
If you found anything that needs updating
  - update `{{SESSIONDIR}}/USER.md` (read it first with read_file)

### Update heartbeat.md

Review conversation above (do NOT read_file session file; you already have all info).
Scan for future actions:
  - Checking email, weather, news, topics, etc.
  - Arrange future todos.
  - update `{{SESSIONDIR}}/heartbeat.md`

### Pick up items from heartbeat.md

Scan for `{{SESSIONDIR}}/heartbeat.md` items that can be acted on.
Do not pick up items that were handled today.
If you find an item that can be acted on now
  - act on it

### Skip this pulse

If none of the above conditions are met
  - append_file `{{SESSIONDIR}}/heartbeat_skip_log.md` with message explaining why you are skipping this pulse
    - format: `- <timestamp>: skipped because <reason>`
    - User will check it regularly
  - If user is going to sleep, calculate sleep duration and postpone heartbeat pulse until then:
    - `exec: {{WORKSPACE}}/bin/nagobot heartbeat postpone <session-key> <duration>` (range: 15m to 6h)

## Step 3

Append a log entry to `{{SESSIONDIR}}/heartbeat_log.md` describing what you did, with the following format:

```markdown
- <timestamp>: heartbeat pulse handled, action taken: <action description>
```

Clean up old log entries older than 2 days.

## Step 4

If you have sent a message to the user, skip step 4. Otherwise, call `sleep_thread()` to end this pulse silently.

# USER.md format

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

# heartbeat.md format

```markdown
- Check Beijing weather for user (they mentioned going out tomorrow)
  created: 2026-03-11
  when: 2026-03-12 morning
  moved_on: after 2026-03-12 (the outing day has passed)
  reason: xxx
```

# Silent exit

To end a turn without sending anything to the user, call `sleep_thread()`. If tool calling is unavailable or fails, output `SLEEP_THREAD_OK` in your response text instead — the system treats this identically to calling sleep_thread. Append SLEEP_THREAD_OK at the end of your response if you forget to call the function.