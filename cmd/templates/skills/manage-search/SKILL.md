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
OpenSearch provides web search powered by Chinese search engines, good for Chinese content. It requires TWO config values (API key and workspace ID):
```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider opensearch --key <api_key>
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider opensearch-workspace --key <workspace_id>
```
- Get API key and workspace ID at https://opensearch.console.aliyun.com/

## Notes

- After adding a key, use the `source` parameter in `web_search` to select that provider.
- Default source is `duckduckgo` (no key needed).
- Changes take effect on the next search call (no server restart required).
