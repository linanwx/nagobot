---
name: web-search-guide
description: Use when choosing or switching web_search sources, or when web_search returns no/poor results — lists available sources, their cost and strengths, and source-selection rules.
---
# web_search source guide

## Available sources

| source | engine | cost | best for |
|--------|--------|------|----------|
| zhipu-cn-std | Zhipu basic | ¥0.01/query | Chinese general queries, best cost-efficiency |
| zhipu-cn-pro | Zhipu advanced | ¥0.03/query | Chinese queries needing higher recall, multi-engine collaboration |
| zhipu-cn-sogou | Zhipu + Sogou | ¥0.05/query | Tencent ecosystem (WeChat/Zhihu), government procurement/bidding, real-time info with dates |
| zhipu-cn-quark | Zhipu + Quark | ¥0.05/query | Time-sensitive vertical content (procurement, news, medical) |

## Zhipu engine selection tips

- **Default choice**: `zhipu-cn-std` — cheapest
- **Procurement/bidding** (招标/采购): prefer `zhipu-cn-sogou` — returns latest dated announcements from bidding platforms
- **Real-time info** (weather, breaking news): `zhipu-cn-sogou` — fastest, strong Tencent news coverage
- **Deep content** (medical guidelines, financial policy, tech reports): `zhipu-cn-std` — richer snippets from authoritative sources

## Other sources

| source | engine | cost | notes |
|--------|--------|------|-------|
| deepseek | DeepSeek Search API | ¥5/1k successful, non-empty queries | Reuses the official DeepSeek API key; ranked results with summaries |
| brave | Brave Search API | $5/1k queries, $5/mo free credit | Structured JSON, stable quality, good for English queries |
| opensearch | Alibaba Cloud OpenSearch | ¥0.0048/query | Chinese web search API, reliable |
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

1. No strict rules — try multiple sources when results are unsatisfying
2. Simple queries: lean toward free sources. Hard queries: lean toward paid sources
3. Chinese context: lean toward Chinese sources. Otherwise: lean toward English sources
