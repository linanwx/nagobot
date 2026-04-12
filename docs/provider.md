# Important

From my real-world testing, although OpenRouter is convenient for accessing models, it does not perform as well as calling official APIs directly for open-weight models, for the following reasons:

- Quantization standards vary across providers on OpenRouter, which leads to performance degradation and a very high function-calling failure rate.
- OpenRouter may randomly route your requests to different providers, making cache hits unlikely and increasing costs.

# Provider Config Examples

OpenRouter (Kimi K2.5):

```yaml
thread:
  provider: openrouter
  modelType: moonshotai/kimi-k2.5

providers:
  openrouter:
    apiKey: sk-or-v1-xxx
```

When using `moonshotai/kimi-k2.5`, provider routing to Moonshot is applied automatically.

Anthropic config example:

```yaml
thread:
  provider: anthropic
  modelType: claude-opus-4-6 # or claude-sonnet-4-6

providers:
  anthropic:
    apiKey: sk-ant-xxx
    # apiBase: https://api.anthropic.com # optional
```

Moonshot CN (official) config example:

```yaml
thread:
  provider: moonshot-cn
  modelType: kimi-k2.5

providers:
  moonshotCN:
    apiKey: sk-xxx
    # apiBase: https://api.moonshot.cn/v1 # optional
```

Moonshot Global (official) config example:

```yaml
thread:
  provider: moonshot-global
  modelType: kimi-k2.5

providers:
  moonshotGlobal:
    apiKey: sk-xxx
    # apiBase: https://api.moonshot.ai/v1 # optional
```

SiliconFlow CN config example:

```yaml
thread:
  provider: siliconflow-cn
  modelType: Pro/zai-org/GLM-5.1

providers:
  siliconflowCN:
    apiKey: sk-xxx
    # apiBase: https://api.siliconflow.cn/v1 # optional
```

SiliconFlow Global config example:

```yaml
thread:
  provider: siliconflow-global
  modelType: zai-org/GLM-5.1

providers:
  siliconflowGlobal:
    apiKey: sk-xxx
    # apiBase: https://api.siliconflow.com/v1 # optional
```

**Note:** SiliconFlow CN and Global are fully separate accounts with separate API keys and different model IDs for the same underlying model — CN uses `Pro/zai-org/GLM-5.1` (paid-tier prefix), Global uses `zai-org/GLM-5.1`. SiliconFlow hosts GLM-5.1 on its own infrastructure as an alternative to zai's overloaded endpoints. Reasoning (`reasoning_content`) is enabled by default on both endpoints and requires no extra configuration. Only GLM-5.1 is whitelisted — other SiliconFlow-hosted models can be added later on demand.
