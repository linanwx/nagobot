---
note: "edit_file tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
params:
  new_text: "The text to replace it with. Empty deletes the matched text."
  old_text: "The exact text to find and replace. Must be unique in the file."
  path: "The path to the file to edit."
---
Replace one exact block of text in a file. Relative paths are resolved from workspace root. old_text must match the file exactly (trailing whitespace, smart quotes, Unicode dashes/spaces, BOM, and CRLF/LF differences are tolerated) and must be unique in the file; include enough surrounding context to make it unique. One replacement per call — to make several changes, call edit_file once per change.
