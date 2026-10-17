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
| deepseek | DeepSeek Fetch API | ¥5/1k successful, non-empty fetches | Remote webpage extraction to Markdown; reuses the official DeepSeek API key |
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

`deepseek`, `jina` and `kimi-*` are remote extractors and do not download: they return
whatever text their service produced. For a PDF, `jina` extracts it to markdown
directly, which is often the shortest path when you only need the prose.

## DeepSeek Fetch

Select `source: "deepseek"`. Credentials are read from `DEEPSEEK_API_KEY` first,
then `providers.deepseek.apiKey`, independently of the chat provider. No separate
fetch key is required, and an OpenCode Go key cannot be used.

Provide an HTTP(S) webpage URL with a host, no embedded username/password, and at
most 8,192 characters. The service returns Markdown when it can extract the page;
it does not guarantee access to blocked sites or arbitrary file downloads.
The account limit is 5 requests/second for Fetch, separate from Search's limit;
additional per-domain limits may apply. Cached pagination of the same URL/source
does not issue another Fetch request while the cache entry is valid.

## Source selection tips

Omitting `source` returns the available-source guide without fetching the URL.
Choose a source explicitly; the default choice below is a recommendation.

- **Default choice**: `go-readability` — best output quality, preserves headings/links/lists as markdown
- **Anti-bot sites** (403/503): try `kimi-cn` or `jina`
- **China government/bidding sites**: `kimi-cn` — domestic endpoint, good compatibility
- **Quick plain text**: `raw` — fastest, but loses all structure
- **If one source fails**: try another — each source has different anti-bot capabilities
