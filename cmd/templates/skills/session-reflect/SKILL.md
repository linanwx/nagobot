---
name: session-reflect
description: Background reflection on conversation history. Extracts user preferences, corrections, and workflow patterns into USER.md. Triggered by heartbeat at the 4-hour quiet mark — never call directly.
---
# Session Reflect

Review the conversation history and extract learnings. This runs in the background after the user has been quiet for ~4 hours.

Learnings have two destinations, and picking the right one is part of the job: **standing rules** go into USER.md, **material** goes into its own file in the session directory (step 5).

## Workflow

1. **Locate USER.md**: read the `file_path` from the `user_preference` section in your system prompt. This is the canonical path to USER.md for the current session.

2. **Read current USER.md**: use `read_file` to load the existing content. If the file is empty or missing, you will create it from scratch.

3. **Review conversation history** since the last reflection (look for a `## Reflection Log` section with timestamps in USER.md to know where you left off). If no prior reflection exists, review the entire conversation.

   Scan for:
   - What the user asked and how they reacted to your outputs
   - Explicit corrections ("no, do it this way", "I meant X not Y")
   - Implicit preferences revealed by their behavior (e.g., they always paste code in a certain style, they prefer brief answers)
   - Successful interaction patterns worth repeating
   - **Material worth keeping**: detail the user will want looked up later — an inventory or list that is now out of date in your head, a table of options they compared, a procedure they walked you through, account/config/device mappings, the conclusions of a piece of research. This is the half of the conversation that does NOT fit in USER.md; step 5 is where it goes.

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

   ### Material (does not go into USER.md)
   - Anything from the last scan bullet: the detail itself, not a rule about it. Carry it to step 5 rather than compressing it into a USER.md line — compressing it is what loses it.

5. **Route bulky material out of USER.md.**

   USER.md is injected into your system prompt IN FULL on every turn of this session, so it can only carry **standing rules about how to serve this person** — one line each. The moment a finding is **material** rather than a rule, it does not belong there: reference tables, inventories, checklists, account/config mappings, protocols, procedures, research conclusions — anything the user will want *looked up* rather than *obeyed*.

   Write that material to its own file instead:

   - **Location: the session directory root**, `{{SESSIONDIR}}/<name>.md`. Never a subdirectory — only files at the root are picked up and indexed; anything nested stays invisible permanently.
   - **Name it after the topic**, kebab-case: `fridge-inventory.md`, `driving-test-checklist.md`. Never a date-shaped name like `2026-08-09.md` — that is `memory/`'s naming convention, and a date-named file at the session root collides with the cross-session recall globs.
   - **If a file on that topic already exists, update it — do not create a second one.** Your system prompt already catalogs this session's files; find the one that matches, `read_file` it, merge the new material in, and write it back. Two files on one subject means every future lookup finds half the answer.
   - **Write it to stand on its own.** A future turn opens this file cold, possibly months later, with none of today's conversation in context. Say what things are, not "the thing we discussed".

   One reflection can produce both kinds at once. When it does, split them: the rule stays as one line in USER.md, the material goes to the file. Do not move the rule out, and do not leave a second copy of the material behind in USER.md.

   Writing the file is the whole job here — nothing else to update, nothing else to register.

6. **Update USER.md**:
   - Merge new findings with existing content. Do NOT duplicate entries that already exist.
   - If an existing entry conflicts with a newer observation, update it to reflect the latest preference.
   - Maintain a `## Reflection Log` section at the bottom with a timestamp for each reflection pass (e.g., `- 2025-01-15: extracted language preference, recorded correction about X`).
   - Keep the `## Reflection Log` bounded: only when it exceeds 20 entries, merge or delete the oldest 10 (keeping the ~10 most recent). Do this in a batch when it crosses 20 — do NOT prune every pass.
   - **Keep every entry to one line.** An entry that is growing into a block is material that took the wrong destination — move it out per step 5 and leave behind at most the one-line rule.
   - If the file exceeds 200 lines after your update, consolidate: merge similar entries, remove outdated observations, compress verbose entries, and move any surviving block of material out per step 5.
   - Write in the same language the user predominantly uses in conversation.
   - Use `write_file` to save.

7. **End silently**: call `dispatch({})` with empty sends. Do NOT produce any user-facing output.

## Rules

- This is a BACKGROUND task. NEVER send messages to the user.
- MUST terminate with `dispatch({})` — empty sends, silent termination.
- If the conversation has no meaningful patterns to extract (e.g., a single trivial exchange), skip the USER.md write. Judge step 5 separately — a conversation can produce material worth filing and no new rule at all, and vice versa. Nothing for either → straight to `dispatch({})`.
- Only record genuinely useful observations. Skip trivial or obvious things like "user asked a question and I answered it."
- Never overwrite USER.md from scratch. Always read first, then merge. Same for a topic file that already exists.
- Each reflection should be incremental — add what is new, update what changed, leave the rest untouched.
- USER.md holds rules, not material. It is injected in full on every turn of this session, so anything that belongs in a lookup rather than in your behavior belongs in its own file (step 5).
