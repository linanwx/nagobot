---
note: "Appended to an edit_file 'text not found' error for a file too large to quote. PATH arrives already quoted. Rendered as ONE line: line breaks here are joined with spaces."
---
This file has {{LINES}} lines, too large to quote here.
Do NOT guess again — recover the real bytes first: grep(pattern: <a distinctive phrase from the text you are targeting>, path: {{PATH}}, context_lines: 3) to find its line number, then read_file(path: {{PATH}}, offset: <that line minus a few>, limit: 40) and copy old_text out of that window verbatim.
