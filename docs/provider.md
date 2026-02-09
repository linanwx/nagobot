# Provider Config Examples

OpenRouter (Kimi K2.5):

```yaml
agents:
  defaults:
    provider: openrouter
    modelType: moonshotai/kimi-k2.5
    # modelName: your-openrouter-preset-or-alias # optional

providers:
  openrouter:
    apiKey: sk-or-v1-xxx
```

When using `moonshotai/kimi-k2.5`, route to OpenRouter's official `moonshot` provider.
If routing falls back to other upstream providers, chain-of-thought and tool-calling can fail.

Anthropic config example:

```yaml
agents:
  defaults:
    provider: anthropic
    modelType: claude-opus-4-6 # or claude-sonnet-4-5

providers:
  anthropic:
    apiKey: sk-ant-xxx
    # apiBase: https://api.anthropic.com # optional
```

Moonshot CN (official) config example:

```yaml
agents:
  defaults:
    provider: moonshot-cn
    modelType: kimi-k2.5

providers:
  moonshotCN:
    apiKey: sk-xxx
    # apiBase: https://api.moonshot.cn/v1 # optional
```

Moonshot Global (official) config example:

```yaml
agents:
  defaults:
    provider: moonshot-global
    modelType: kimi-k2.5

providers:
  moonshotGlobal:
    apiKey: sk-xxx
    # apiBase: https://api.moonshot.ai/v1 # optional
```
