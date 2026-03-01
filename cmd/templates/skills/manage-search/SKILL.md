---
name: manage-search
description: Configure web search API keys. Use when setting up Brave, zhipu, or other search providers to improve search quality. Also use when the user asks to change search settings, check which search providers are available, or troubleshoot search issues.
---
# Manage Search Configuration

## Add or Update a Provider Key

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider <name> --key <api_key>
```

Supported providers: `brave`, `zhipu` (more may be added).

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

## Notes

- After adding a key, use the `source` parameter in `web_search` to select that provider.
- Default source is `duckduckgo` (no key needed).
- Changes take effect on the next search call (no server restart required).
- Get a Brave Search API key at https://brave.com/search/api/
