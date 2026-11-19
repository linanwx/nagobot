---
note: "read_file on an image when the model has no vision: delegate to the imagereader subagent. Line breaks are kept."
---
This is an image file. You cannot view images directly. Delegate to the imagereader subagent: call dispatch with to=subagent, agent='imagereader', a descriptive task_id (e.g. 'read-image-<short-name>'), and a body that includes this image's file path followed by the user's question/context.
path: {{PATH}}
imagereader reads the image from that path itself; the body MUST contain the path or it cannot proceed.
