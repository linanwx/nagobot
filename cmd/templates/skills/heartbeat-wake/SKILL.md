---
name: heartbeat-wake
description: Routing layer for heartbeat pulses. Reads pulse_index from the wake message and dispatches to the appropriate sub-skill. Called automatically by the heartbeat scheduler — never call directly.
---
# Heartbeat Wake Router

The heartbeat scheduler fired a pulse into this session. Your job is to read the `pulse_index` from the wake message and route accordingly.

## Wake Message Format

The wake message is a system message with YAML frontmatter containing:

- `pulse_index`: 1-based integer indicating which pulse this is
- `elapsed_since_user`: duration string since last user activity
- `next_pulse`: RFC3339 timestamp of the next scheduled pulse
- `should_dream`: present and set to `true` only when the scheduler decided this pulse should trigger a dream (session-local night, user quiet for a long time)

## Routing Table

Check `should_dream` FIRST — it takes priority over `pulse_index`:

| Condition | Action |
|-----------|--------|
| `should_dream: true` is present | Call `use_skill("dream")` and follow its instructions. Do nothing else. |
| `pulse_index == 2` (no dream) | Call `use_skill("session-reflect")` and follow its instructions. |
| Any other value | No action needed. Call `dispatch({})` to end silently. |

## Rules

1. First check the wake message YAML frontmatter for `should_dream: true`. If present, call `use_skill("dream")`, follow its output, and do nothing else.
2. Otherwise, extract `pulse_index`. If `pulse_index == 2`: call `use_skill("session-reflect")` and follow its output.
3. For all other cases: call `dispatch({})` to end the turn silently.
4. NEVER produce user-facing output from this router.
5. Every execution MUST terminate with either a sub-skill call or `dispatch({})`.
