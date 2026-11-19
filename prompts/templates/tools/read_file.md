---
note: "read_file tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
params:
  limit: "Maximum number of lines to return. Can be omitted (reads up to 2000 lines). Ignored when tail is set."
  offset: "Starting line number (1-based). Can be omitted to start from the beginning. Ignored when tail is set."
  path: "The path to the file to read."
  tail: "Read the last N lines of the file. When set, offset and limit are ignored."
---
Read a file. Automatically detects file type: text files are returned with line numbers and pagination, images are analyzed if the model supports vision or delegated to the imagereader agent, audio files are analyzed if the model supports audio or delegated to the audioreader agent, and binary files are rejected with an error. Use tail to read the last N lines of a text file (offset and limit are ignored when tail is set).
