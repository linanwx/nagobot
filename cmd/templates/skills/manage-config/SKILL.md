---
name: manage-config
description: Change nagobot's configuration — set or replace LLM provider API keys, switch which model a session or agent runs on, and set up web search providers. This is the WRITE path — add a key, rotate an expired key, change the model, fix a misconfigured provider. Use when the user wants to configure, change, or troubleshoot provider/model/search settings. To only READ balances or usage, use monitoring instead.
examples:
  - rotate an expired provider API key
  - 把机器人的默认模型换成另一家的
  - update the OpenRouter key in the config
  - 帮我更新一下配置里的 API 密钥
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

Supported providers: `openai`, `openrouter`, `deepseek`, `moonshot-cn`, `moonshot-global`, `zhipu-cn`, `zhipu-global`, `minimax-cn`, `minimax-global`, `siliconflow-cn`, `siliconflow-global`, `xai`, `mimo`. Gemini models are reachable only through `openrouter` (`google/gemini-*`) — the native `gemini` provider was removed.

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

### Set Per-Specialty Routing

Agent templates declare a `specialty` **array** in their frontmatter (e.g. `specialty: [chat]`, `specialty: [cron, toolcall]`). `set-model --type` writes a **specialty rule** into the typed routing list (`config.yaml > thread.models`), applying to every agent that carries that tag.

```
exec: {{WORKSPACE}}/bin/nagobot set-model --type <specialty> --provider <name> --model <model>
```

Resolution precedence is **session > agent > specialty > default** (an agent's specialties are tried left-to-right). To pin a model to a specific **session** (not a specialty), use `set-agent --session ... --provider ... --model ...` — see the session-ops skill.

### List Current Routing and Available Models

```
exec: {{WORKSPACE}}/bin/nagobot set-model --list
```

### List Fallback Candidates with Balance Status

Shows all configured providers classified into three groups:
1. **Available** — API key configured, balance OK (fallback candidates)
2. **Exhausted** — API key configured, but balance depleted
3. **Unreliable** — cannot verify balance (no balance API)

```
exec: {{WORKSPACE}}/bin/nagobot set-model --list-fallback
```

### Clear Routing (Revert to Default)

```
exec: {{WORKSPACE}}/bin/nagobot set-model --type <model_type> --clear
```

**Note**: You must configure a provider's API key BEFORE routing models to it.

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
