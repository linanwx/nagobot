# Important

From my real-world testing, although OpenRouter is convenient for accessing models, it does not perform as well as calling official APIs directly for open-weight models, for the following reasons:

- Quantization standards vary across providers on OpenRouter, which leads to performance degradation and a very high function-calling failure rate.
- OpenRouter may randomly route your requests to different providers, making cache hits unlikely and increasing costs.

# Provider Config Examples

OpenRouter (Kimi K2.6):

```yaml
thread:
  provider: openrouter
  modelType: moonshotai/kimi-k2.6

providers:
  openrouter:
    apiKey: sk-or-v1-xxx
```

When using `moonshotai/kimi-k2.6`, provider routing to Moonshot is applied automatically.

Moonshot CN (official) config example:

```yaml
thread:
  provider: moonshot-cn
  modelType: kimi-k2.6

providers:
  moonshotCN:
    apiKey: sk-xxx
    # apiBase: https://api.moonshot.cn/v1 # optional
```

Moonshot Global (official) config example:

```yaml
thread:
  provider: moonshot-global
  modelType: kimi-k2.6

providers:
  moonshotGlobal:
    apiKey: sk-xxx
    # apiBase: https://api.moonshot.ai/v1 # optional
```

SiliconFlow CN config example:

```yaml
thread:
  provider: siliconflow-cn
  modelType: Pro/zai-org/GLM-5.2

providers:
  siliconflowCN:
    apiKey: sk-xxx
    # apiBase: https://api.siliconflow.cn/v1 # optional
```

SiliconFlow Global config example:

```yaml
thread:
  provider: siliconflow-global
  modelType: zai-org/GLM-5.2

providers:
  siliconflowGlobal:
    apiKey: sk-xxx
    # apiBase: https://api.siliconflow.com/v1 # optional
```

**Note:** SiliconFlow CN and Global are fully separate accounts with separate API keys and different model IDs for the same underlying model — CN uses `Pro/zai-org/GLM-5.2` (paid-tier prefix), Global uses `zai-org/GLM-5.2`. SiliconFlow hosts these models on its own infrastructure as an alternative to zai's overloaded endpoints. Reasoning (`reasoning_content`) is enabled by default on both endpoints and the bot sends `reasoning_effort: high` to engage the High effort tier. Other SiliconFlow-hosted models can be added later on demand.

Zhipu / Z.ai native config example (GLM-5.3):

```yaml
thread:
  provider: zhipu-cn # or zhipu-global
  modelType: glm-5.3

providers:
  zhipuCN:
    apiKey: xxx
    # apiBase: https://open.bigmodel.cn/api/paas/v4 # optional (zhipu-global defaults to https://api.z.ai/api/paas/v4)
```

**Note:** GLM-5.3 is a 1M-context (1,000,000 tokens), 128K-max-output reasoning model, text-only. Thinking is enabled automatically (`thinking.type: enabled`) and the bot sends `reasoning_effort: high` for the speed/quality-balanced High tier. Because thinking is on, the request temperature is forced to `1`. The same `glm-5.3` model id is used on both `zhipu-cn` (open.bigmodel.cn) and `zhipu-global` (api.z.ai). GLM-5.3 is also routable via OpenRouter as `z-ai/glm-5.3` (routed window 262,144 — deliberately below the model's real 1M, carried over from the 5.2 entry).

The SiliconFlow routes above stay on **GLM-5.2** on purpose: SiliconFlow serves open weights, and zai-org has published nothing past 5.2 on HuggingFace, so GLM-5.3 exists only behind Z.ai's own API and OpenRouter. Bump those two entries once the 5.3 weights appear in SiliconFlow's catalog.
