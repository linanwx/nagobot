---
name: heartbeat-wake
description: Routing layer for heartbeat pulses. Reads the task name the scheduler selected and dispatches to the matching sub-skill. Called automatically by the heartbeat scheduler — never call directly.
---
# Heartbeat Wake Router

The heartbeat scheduler fired a pulse into this session and has ALREADY decided
what should happen. Your only job is to read the `task` field from the wake
message and call the matching sub-skill.

## Wake Message Format

The wake message is a system message with YAML frontmatter containing:

- `task`: the name of the task the scheduler selected. This is the routing key.
- `pulse_index`: 1-based integer indicating which pulse this is
- `elapsed_since_user`: duration string since last user activity
- `next_pulse`: RFC3339 timestamp of the next scheduled pulse
- `session_summary`: present only on a `dream` task — this session's current one-line summary, or `(none on record — …)` when it has never had one. The dream skill judges and may rewrite it; this router does not read it.

## Routing Table

| `task` | Action |
|--------|--------|
| `dream` | Call `use_skill("dream")` and follow its instructions. Do nothing else. |
| `reflect` | Call `use_skill("session-reflect")` and follow its instructions. Do nothing else. |
| anything else, or absent | No action needed. Call `dispatch({})` to end silently. |

## Rules

1. Route on `task` alone. Do NOT derive the routing from `pulse_index`,
   `elapsed_since_user`, or the time of day — the scheduler already weighed
   those, applied priority between competing tasks, and recorded the result. A
   second opinion here can only disagree with it.
2. `pulse_index` and `elapsed_since_user` are context for the sub-skill, not
   inputs to this decision.
3. An unrecognized or absent `task` is not an error to report — end silently
   with `dispatch({})`.
4. NEVER produce user-facing output from this router.
5. Every execution MUST terminate with either a sub-skill call or `dispatch({})`.
