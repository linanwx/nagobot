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

## Routing Table

| pulse_index | Action |
|-------------|--------|
| 2 (60min mark) | Call `use_skill("session-reflect")` and follow its instructions. |
| Any other value | No action needed. End silently. |

## Rules

1. Extract `pulse_index` from the wake message YAML frontmatter.
2. If `pulse_index == 2`: call `use_skill("session-reflect")` and follow its output.
3. For all other `pulse_index` values: call `dispatch({})` to end the turn silently.
4. NEVER produce user-facing output for unhandled pulse indices.
5. Every execution MUST terminate with either a sub-skill call or `dispatch({})`.
