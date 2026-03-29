# web_search source guide

## Available sources

| source | engine | cost | best for |
|--------|--------|------|----------|
| zhipu-cn-std | Zhipu basic | ¥0.01/query | Chinese general queries, best cost-efficiency |
| zhipu-cn-pro | Zhipu advanced | ¥0.03/query | Chinese queries needing higher recall, multi-engine collaboration |
| zhipu-cn-sogou | Zhipu + Sogou | ¥0.05/query | Tencent ecosystem (WeChat/Zhihu), government procurement/bidding, real-time info with dates |
| zhipu-cn-quark | Zhipu + Quark | ¥0.05/query | Vertical content (tech blogs, niche topics) |

## Zhipu engine selection tips

- **Default choice**: `zhipu-cn-std` — cheapest, results nearly identical to `zhipu-cn-pro` for most queries
- **Procurement/bidding** (招标/采购): prefer `zhipu-cn-sogou` — returns latest dated announcements from bidding platforms
- **Real-time info** (weather, breaking news): `zhipu-cn-sogou` — fastest, strong Tencent news coverage
- **Deep content** (medical guidelines, financial policy, tech reports): `zhipu-cn-std` — richer snippets from authoritative sources
- **Avoid** `zhipu-cn-quark` for broad queries — may return very few results (e.g., 1 result for weather)

## Other sources

| source | engine | cost | notes |
|--------|--------|------|-------|
| brave | Brave Search API | $5/1k queries, $5/mo free credit | Structured JSON, stable quality, good for English queries |
| opensearch | Alibaba Cloud OpenSearch | ¥0.0048/query | Chinese web search API, reliable |
| duckduckgo | DuckDuckGo HTML scraping | free | Good quality, but blocked in China. Heavy use may trigger anti-bot |
| bing | www.bing.com HTML scraping | free | Low quality on datacenter IPs — returns entity-level matches instead of content matches for complex queries |
| bing-cn | cn.bing.com HTML scraping | free | Same as bing, worse for Chinese vertical queries (e.g. "深圳 招投标" returns only gov.cn homepage) |

## Source selection rules

1. **First call with empty source** returns available sources and stats — use this to learn what's configured
2. **Chinese queries**: prefer `zhipu-cn-std` (cheapest paid) or `zhipu-cn-sogou` (procurement/real-time)
3. **English queries**: prefer `brave` if configured, otherwise `duckduckgo`
4. **Avoid bing/bing-cn** unless no other source is available — scraping quality is unreliable
5. **Cost-conscious**: `duckduckgo` (free) > `opensearch` (¥0.0048) > `zhipu-cn-std` (¥0.01) > `brave` ($0.005)