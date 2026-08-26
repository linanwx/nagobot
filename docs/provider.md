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
| `reasoning_effort` | **not sent** | **not sent** — see below |
| OpenRouter route | `z-ai/glm-5.3` | `z-ai/glm-5.3-flash` |
| price (OpenRouter, per 1M) | $1.40 in / $4.40 out | $0.075 in / $0.25 out |

Everything above was measured against the live API on 2026-08-26, and two of the rows contradict the vendor documentation:

- **`thinking.type` accepts only `enabled`.** `disabled` — and every other value — is a `400` on both models (`该模型始终思考，不支持关闭思考`). Since thinking is always on, the request temperature is forced to `1`.
- **`reasoning_effort` is a real, top-level parameter that both models accept** (despite the flash doc page saying otherwise; the legal set is exactly `low` / `high` / `max`, and `none` / `minimal` / `medium` / `xhigh` are all `400`). **We deliberately send none of them**, because on this family `high` is not a high setting — it is well below the depth the vendor picks unprompted. Measured on `glm-5.3-flash` with the real system prompt and tool set, reasoning tokens over three runs each:

  | | run 1 | run 2 | run 3 |
  |---|---|---|---|
  | `low` | 7 | 9 | 29 |
  | `high` | 31 | 61 | 139 |
  | **no field** | **580** | **743** | **1000** |
  | `max` | 807 | 997 | 2051 |

  The same ordering holds through OpenRouter (`high` 91–150, no field 632–1032, `max` 815–1669). Leaving the field out is therefore the *deeper* setting, and `max` is the only way to go deeper still.

- **`thinking.clear_thinking: false` is sent**, per the vendor's recommendation. It roughly doubles the reasoning that comes back (899 / 1361 / 1488 tokens against 580 / 743 / 1000 with the field absent).

**Both parameters used to be sent under an `extra_body` wrapper, and neither was ever applied.** There is an irony worth recording: because the wrapper ate `reasoning_effort: high`, the model had been running at the vendor default all along — which the table above shows is the *better* setting. v1.7.49 fixed the transport and shipped the wrong value, cutting reasoning about 10x; v1.7.50 keeps the transport fix and drops the value.

 `extra_body` is a Python-SDK convention that the Python client unwraps before sending; on the wire it is just an unknown object, which this endpoint ignores. It returned `200` every time, so nothing ever surfaced it. Fixed 2026-08-26 — both fields are now top-level, guarded by `TestZhipuSendsThinkingParamsAtTopLevel`, which asserts on the marshalled body because every check above that level passed throughout. Note this is a real behavior change on `glm-5.3`: it now actually runs at `high` instead of the server default.

Both OpenRouter routes are pinned to the `z-ai` upstream. On `z-ai/glm-5.3-flash` that pin is doing real work: Z.AI and Novita both serve fp8, but a Cloudflare host is listed at quantization `unknown` for twice the price, so an unpinned route can silently answer from different weights.

The OpenRouter window for `z-ai/glm-5.3` was **262,144 until 2026-08-26, and that was a bug, not a policy** — 262,144 is `kimi-k2.6`'s window and `glm-5.2`'s max output, never any GLM's context. Both routes now register 1,000,000, matching the native ones (`TestGLM53WindowsAgreeAcrossRoutes`).

The effect on a running bot is smaller than the registration difference suggests, because the number that governs compression is `EffectiveContextWindow` = **min(model window, `thread.contextWindowTokens`)**, and that config value defaults to 300,000. Every deployment today sits at the default, so this fix moves the OpenRouter GLM route from 262,144 to 300,000 — real, but ~14%, not 4x. The full 1M only becomes reachable on a deployment that also raises `contextWindowTokens`.
