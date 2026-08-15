---
name: web-fetch-guide
description: Use when choosing or switching web_fetch sources, or when web_fetch fails (403/503/anti-bot) — lists available fetch sources and source-selection tips.
---
# web_fetch source guide

## Available sources

| source | provider | cost | best for |
|--------|----------|------|----------|
| go-readability | HTTP GET + readability + markdown | free | Best quality for static pages, preserves structure |
| raw | HTTP GET + tag stripping | free | Fast, plain text output, no structure |
| jina | Jina Reader (r.jina.ai) | free (rate-limited 20 RPM) | Anti-bot bypass, clean markdown |
| kimi-cn | Moonshot/Kimi China API | free (limited-time) | China domestic sites, fast, anti-bot bypass |
| kimi-global | Moonshot/Kimi Global API | free (limited-time) | International sites via Kimi |

## When the URL is a file, not a page

A URL serving a PDF, image, audio, archive or Office document is not fetched as
text. `go-readability` and `raw` download it into the workspace and return its
local path, type and size instead of page content. Act on the path:

- **PDF / image / audio** — call `read_file` on it. It dispatches on file type
  and tells you what to do next (vision marker, `imagereader`/`audioreader`
  delegation, or PDF extraction steps).
- **Archive / Office document** — use `exec` at that path (`unzip`, or python
  with `openpyxl` / `python-docx`).

Do not re-fetch the URL to "get the text" — the bytes are already on disk, and
the download is not cached.

`jina` and `kimi-*` are remote extractors and do not download: they return
whatever text their service produced. For a PDF, `jina` extracts it to markdown
directly, which is often the shortest path when you only need the prose.

## Source selection tips

- **Default choice**: `go-readability` — best output quality, preserves headings/links/lists as markdown
- **Anti-bot sites** (403/503): try `kimi-cn` or `jina`
- **China government/bidding sites**: `kimi-cn` — domestic endpoint, good compatibility
- **Quick plain text**: `raw` — fastest, but loses all structure
- **If one source fails**: try another — each source has different anti-bot capabilities
