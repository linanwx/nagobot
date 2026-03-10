---
name: manage-config
description: Configure nagobot settings — LLM provider API keys, model routing, messaging channels (Telegram), and web search providers. Use when the user wants to add/change/check API keys, switch models, set up Telegram, configure search providers, or troubleshoot any configuration issue.
---
# Manage Configuration

## Provider API Keys

### Add or Update a Provider Key

```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --provider <name> --api-key <api_key>
```

With custom API base URL:
```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --provider <name> --api-key <api_key> --api-base <url>
```

Supported providers: `openai`, `openrouter`, `anthropic`, `deepseek`, `gemini`, `moonshot-cn`, `moonshot-global`, `zhipu-cn`, `zhipu-global`, `minimax-cn`, `minimax-global`.

### List All Provider Key Status

```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --list
```

### Check / Remove a Provider

```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --provider <name>
```

```
exec: {{WORKSPACE}}/bin/nagobot set-provider-key --provider <name> --clear
```

---

## Model Routing

### Set Default Provider/Model

```
exec: {{WORKSPACE}}/bin/nagobot set-model --default --provider <name> --model <model>
```

### Set Per-Type Routing

Agent templates declare a `specialty` in their frontmatter (e.g. `specialty: chat`, `specialty: toolcall`). Per-type routing maps specialties to a specific provider and model.

```
exec: {{WORKSPACE}}/bin/nagobot set-model --type <model_type> --provider <name> --model <model>
```

### List Current Routing and Available Models

```
exec: {{WORKSPACE}}/bin/nagobot set-model --list
```

### Clear Routing (Revert to Default)

```
exec: {{WORKSPACE}}/bin/nagobot set-model --type <model_type> --clear
```

**Note**: You must configure a provider's API key BEFORE routing models to it.

---

## Telegram Channel

### View Status

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram
```

### Set Bot Token

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --token <BOT_TOKEN>
```

Get token from [@BotFather](https://t.me/BotFather) (`/newbot` command).

### Set Allowed User/Chat IDs

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --allowed "123456,789012"
```

Allow all (no restriction):
```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --allowed ""
```

### Clear Telegram Config

```
exec: {{WORKSPACE}}/bin/nagobot set-telegram --clear
```

**Hot-reload**: Token and allowed ID changes are detected every 10 seconds. No restart needed.

---

## Web Search Providers

### Add or Update a Search Key

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider <name> --key <api_key>
```

Supported: `brave`, `opensearch`, `zhipu`.

### List / Check / Remove

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --list
```

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider <name>
```

```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider <name> --clear
```

### Provider-Specific Setup

**Brave**: Get API key at https://brave.com/search/api/

**OpenSearch (Alibaba Cloud)**: Requires TWO values (API key + API host):
```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider opensearch --key <api_key>
```
```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider opensearch-host --key <api_host>
```
- Sign up at: https://opensearch.console.aliyun.com/cn-shanghai/rag/api-key
- API host shown as "公网API域名" on console (format: `default-xxx.platform-cn-shanghai.opensearch.aliyuncs.com`, omit `http://`)

**Zhipu (智谱)**:
```
exec: {{WORKSPACE}}/bin/nagobot set-search-key --provider zhipu --key <api_key>
```
- Sign up at: https://open.bigmodel.cn/usercenter/apikeys
- If `zhipu-cn` LLM provider is already configured, its key is automatically reused (no extra setup needed)

---

## General Notes

- All changes take effect immediately (no server restart required).
- All changes persist across restarts (saved to config.yaml).
- Use `source` parameter in `web_search` to select a search provider. Default: `duckduckgo` (no key needed).
