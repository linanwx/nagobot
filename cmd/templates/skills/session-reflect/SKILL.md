---
name: session-reflect
description: Background reflection on conversation history. Extracts user preferences, corrections, and workflow patterns into USER.md. Triggered by heartbeat at the 60-minute quiet mark — never call directly.
---
# Session Reflect

Review the conversation history and extract learnings into USER.md. This runs in the background after the user has been quiet for ~60 minutes.

## Workflow

1. **Locate USER.md**: read the `file_path` from the `user_preference` section in your system prompt. This is the canonical path to USER.md for the current session.

2. **Read current USER.md**: use `read_file` to load the existing content. If the file is empty or missing, you will create it from scratch.

3. **Review conversation history** since the last reflection (look for a `## Reflection Log` section with timestamps in USER.md to know where you left off). If no prior reflection exists, review the entire conversation.

   Scan for:
   - What the user asked and how they reacted to your outputs
   - Explicit corrections ("no, do it this way", "I meant X not Y")
   - Implicit preferences revealed by their behavior (e.g., they always paste code in a certain style, they prefer brief answers)
   - Successful interaction patterns worth repeating

4. **Extract findings** into these categories:

   ### Communication Preferences
   - Language preference (which language they use)
   - Detail level (concise vs. detailed)
   - Tone and style expectations

   ### Technical Preferences
   - Tools, libraries, patterns they favor or reject
   - Coding style and conventions they follow
   - How they want code changes delivered

   ### Corrections and Mistakes
   - Cases where you misunderstood the user's intent
   - Approaches the user rejected and what they wanted instead
   - The correct approach for future reference

   ### Workflow Patterns
   - How the user likes to work (e.g., plan first vs. dive in)
   - Successful interaction sequences worth repeating
   - Task delegation preferences

5. **Update USER.md**:
   - Merge new findings with existing content. Do NOT duplicate entries that already exist.
   - If an existing entry conflicts with a newer observation, update it to reflect the latest preference.
   - Maintain a `## Reflection Log` section at the bottom with a timestamp for each reflection pass (e.g., `- 2025-01-15: extracted language preference, recorded correction about X`).
   - If the file exceeds 200 lines after your update, consolidate: merge similar entries, remove outdated observations, compress verbose entries.
   - Write in the same language the user predominantly uses in conversation.
   - Use `write_file` to save.

6. **End silently**: call `dispatch({})` with empty sends. Do NOT produce any user-facing output.

## Rules

- This is a BACKGROUND task. NEVER send messages to the user.
- MUST terminate with `dispatch({})` — empty sends, silent termination.
- If the conversation has no meaningful patterns to extract (e.g., a single trivial exchange), skip the write and go straight to `dispatch({})`.
- Only record genuinely useful observations. Skip trivial or obvious things like "user asked a question and I answered it."
- Never overwrite USER.md from scratch. Always read first, then merge.
- Each reflection should be incremental — add what is new, update what changed, leave the rest untouched.
