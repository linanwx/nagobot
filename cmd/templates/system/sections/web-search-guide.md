---
name: web-search-guide
priority: 410
parent: tools
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
| brave | Brave Search API | $5/1k queries, $5/mo free credit | Structured JSON, stable quality, good for English queries |
| opensearch | Alibaba Cloud OpenSearch | ¥0.0048/query | Chinese web search API, reliable |
| duckduckgo | DuckDuckGo HTML scraping | free | Good quality, but blocked in China. Heavy use may trigger anti-bot |
| bing | www.bing.com HTML scraping | free | Low quality on datacenter IPs — returns entity-level matches instead of content matches for complex queries |
| bing-cn | cn.bing.com HTML scraping | free | Same as bing, low quality, worse for Chinese vertical queries (e.g. "深圳 招投标" returns only gov.cn homepage) |

## Source selection rules

1. No strict rules — try multiple sources when results are unsatisfying
2. Simple queries: lean toward free sources. Hard queries: lean toward paid sources
3. Chinese context: lean toward Chinese sources. Otherwise: lean toward English sources
