---
note: "read_file on an audio file when the model has no audio input: delegate to the audioreader subagent. Rendered as ONE line: line breaks here are joined with spaces."
---
This is an audio file.
You cannot listen to audio directly.
Use dispatch with to=subagent, agent='audioreader', and pass the audio file path as the body.
Pick a descriptive task_id (e.g.
'read-audio-<short-name>').
