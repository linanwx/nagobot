---
name: web-search-guide
description: Use when choosing or switching web_search sources, or when web_search returns no/poor results — prefer deepseek, then zhipu-cn-std, then brave, and avoid expensive sources.
---
# web_search source guide

## Available sources

| source | engine | cost | best for |
|--------|--------|------|----------|
| deepseek | DeepSeek Search API | ¥5/1k successful, non-empty queries | First choice; ranked results with summaries, reuses the official DeepSeek API key |
| zhipu-cn-std | Zhipu basic | ¥0.01/query | Second choice; Chinese general queries and authoritative-source snippets |
| brave | Brave Search API | $5/1k queries, $5/mo free credit | Third choice; structured JSON and good English coverage |

## Other sources

| source | engine | cost | notes |
|--------|--------|------|-------|
| zhipu-cn-pro | Zhipu advanced | ¥0.03/query | Paid alternative; not part of the preferred fallback chain |
| zhipu-cn-sogou | Zhipu + Sogou | ¥0.05/query | Avoid for cost control; do not automatically select for bidding, Tencent content or fresh news |
| zhipu-cn-quark | Zhipu + Quark | ¥0.05/query | Avoid for cost control; do not automatically select for vertical or time-sensitive queries |
| opensearch | Alibaba Cloud OpenSearch | Paid; depends on plan/quota | Avoid per the user's cost-control preference |
| duckduckgo | DuckDuckGo HTML scraping | free | Good quality, but blocked in China. Heavy use may trigger anti-bot |
| bing | www.bing.com HTML scraping | free | Low quality on datacenter IPs — returns entity-level matches instead of content matches for complex queries |
| bing-cn | cn.bing.com HTML scraping | free | Same as bing, low quality, worse for Chinese vertical queries (e.g. "深圳 招投标" returns only gov.cn homepage) |

## DeepSeek Search

Select `source: "deepseek"`. Credentials are read from `DEEPSEEK_API_KEY` first,
then `providers.deepseek.apiKey`; the chat provider does not affect availability.
An OpenCode Go key cannot be used here. No separate search key is required.

Queries must contain 1–128 Unicode characters; `max_results` supports 1–50.
Use concise queries and retain useful operators such as `site:` and quoted phrases.
The account limit is 5 requests/second for Search, separate from Fetch's limit.
Fetch a result URL with `web_fetch` when the summary is insufficient.

## Source selection rules

Omitting `source` returns the available-source guide without running a search.
Choose a source explicitly; recommendations below are not automatic defaults.

1. **Preferred order: `deepseek` → `zhipu-cn-std` → `brave`.** This is the user's cost/free-credit preference and applies to both Chinese and English queries. Skip a source if it is unavailable.
2. Start with the first available preferred source. If it fails, is rate-limited, or produces empty/poor results, refine the query as useful and try the next source. Stop when there is enough reliable evidence; do not call all three routinely.
3. **Avoid `zhipu-cn-sogou`, `zhipu-cn-quark` and `opensearch`.** Do not automatically escalate to them because a query concerns procurement, breaking news, medicine or other specialized content. Use them only when the user specifically requests that source.
4. If all preferred sources are unavailable or inadequate, free HTML sources may be used as a fallback, subject to their quality and anti-bot limitations. Query difficulty alone is not a reason to switch to a more expensive source.
