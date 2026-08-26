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

Zhipu / Z.ai native config example (GLM-5.3 / GLM-5.3-Flash):

```yaml
thread:
  provider: zhipu-cn # or zhipu-global
  modelType: glm-5.3

providers:
  zhipuCN:
    apiKey: xxx
    # apiBase: https://open.bigmodel.cn/api/paas/v4 # optional (zhipu-global defaults to https://api.z.ai/api/paas/v4)
```

**Note:** both GLM-5.3 models are 1M-context (1,000,000 tokens), 128K-max-output reasoning models, and both use the same model id on `zhipu-cn` (open.bigmodel.cn) and `zhipu-global` (api.z.ai). The only difference the bot cares about is image input.

| | `glm-5.3` | `glm-5.3-flash` |
|---|---|---|
| input | text only — an image is a `400` | text + image (natively multimodal; video/file are not wired here) |
| thinking | always on, cannot be disabled | same |
| `reasoning_effort` | `high` | `high` — same dial, same legal set |
| OpenRouter route | `z-ai/glm-5.3` | `z-ai/glm-5.3-flash` |
| price (OpenRouter, per 1M) | $1.40 in / $4.40 out | $0.075 in / $0.25 out |

Everything above was measured against the live API on 2026-08-26, and two of the rows contradict the vendor documentation:

- **`thinking.type` accepts only `enabled`.** `disabled` — and every other value — is a `400` on both models (`该模型始终思考，不支持关闭思考`). Since thinking is always on, the request temperature is forced to `1`.
- **`reasoning_effort` is a real, top-level parameter, and `glm-5.3-flash` takes it**, despite its doc page saying it does not. The legal set on this family is exactly `low` / `high` / `max`; `none`, `minimal`, `medium` and `xhigh` are all `400`, on both models, even though the error message lists them. The dial measurably moves the depth (~32 / ~90 / ~135 characters of reasoning at low / unset / max on a fixed arithmetic question).

**Both parameters used to be sent under an `extra_body` wrapper, and neither was ever applied.** `extra_body` is a Python-SDK convention that the Python client unwraps before sending; on the wire it is just an unknown object, which this endpoint ignores. It returned `200` every time, so nothing ever surfaced it. Fixed 2026-08-26 — both fields are now top-level, guarded by `TestZhipuSendsThinkingParamsAtTopLevel`, which asserts on the marshalled body because every check above that level passed throughout. Note this is a real behavior change on `glm-5.3`: it now actually runs at `high` instead of the server default.

Both OpenRouter routes are pinned to the `z-ai` upstream. On `z-ai/glm-5.3-flash` that pin is doing real work: Z.AI and Novita both serve fp8, but a Cloudflare host is listed at quantization `unknown` for twice the price, so an unpinned route can silently answer from different weights.

The OpenRouter window for `z-ai/glm-5.3` was **262,144 until 2026-08-26, and that was a bug, not a policy** — 262,144 is `kimi-k2.6`'s window and `glm-5.2`'s max output, never any GLM's context. It made the bot compress and trim a 1M model at a quarter of its capacity, with no error anywhere. Both routes now register 1,000,000, matching the native ones (`TestGLM53WindowsAgreeAcrossRoutes`).
