---
name: manage-search
description: Configure web search API keys. Use when setting up Brave, OpenSearch, or other search providers to improve search quality. Also use when the user asks to change search settings, check which search providers are available, or troubleshoot search issues.
---
# Manage Search Configuration

## Add or Update a Provider Key

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider <name> --key <api_key>
```

Supported providers: `brave`, `opensearch` (more may be added).

## List Configured Providers

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --list
```

## Check a Specific Provider

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider <name>
```

## Remove a Provider Key

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider <name> --clear
```

## Provider-Specific Setup

### Brave
- Get an API key at https://brave.com/search/api/
- Set key: `nagobot set-search-key --provider brave --key <api_key>`

### OpenSearch (Alibaba Cloud)
OpenSearch AI Search Platform provides web search powered by Chinese search engines, good for Chinese content. It requires TWO config values (API key and API host):
```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider opensearch --key <api_key>
```
```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider opensearch-host --key <api_host>
```
- User needs to sign up and get API key + API host at: https://opensearch.console.aliyun.com/cn-shanghai/rag/api-key
- The API host is account-specific, shown as "公网API域名" on the console (format: `default-xxx.platform-cn-shanghai.opensearch.aliyuncs.com`, omit the `http://` prefix)

## Notes

- After adding a key, use the `source` parameter in `web_search` to select that provider.
- Default source is `duckduckgo` (no key needed).
- Changes take effect on the next search call (no server restart required).
