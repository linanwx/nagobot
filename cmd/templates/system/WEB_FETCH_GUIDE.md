# web_fetch source guide

## Available sources

| source | provider | cost | best for |
|--------|----------|------|----------|
| go-readability | HTTP GET + readability + markdown | free | Best quality for static pages, preserves structure |
| raw | HTTP GET + tag stripping | free | Fast, plain text output, no structure |
| jina | Jina Reader (r.jina.ai) | free (rate-limited 20 RPM) | Anti-bot bypass, clean markdown |
| kimi-cn | Moonshot/Kimi China API | free (limited-time) | China domestic sites, fast, anti-bot bypass |
| kimi-global | Moonshot/Kimi Global API | free (limited-time) | International sites via Kimi |

## Source selection tips

- **Default choice**: `go-readability` — best output quality, preserves headings/links/lists as markdown
- **Anti-bot sites** (403/503): try `kimi-cn` or `jina`
- **China government/bidding sites**: `kimi-cn` — domestic endpoint, good compatibility
- **Quick plain text**: `raw` — fastest, but loses all structure
- **If one source fails**: try another — each source has different anti-bot capabilities
