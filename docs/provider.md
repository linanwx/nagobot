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
| `reasoning_effort` | `high` | `high` — below the vendor default, see below |
| OpenRouter route | `z-ai/glm-5.3` | `z-ai/glm-5.3-flash` |
| price (OpenRouter, per 1M) | $1.40 in / $4.40 out | $0.075 in / $0.25 out |

Everything above was measured against the live API on 2026-08-26, and two of the rows contradict the vendor documentation:

- **`thinking.type` accepts only `enabled`.** `disabled` — and every other value — is a `400` on both models (`该模型始终思考，不支持关闭思考`). Since thinking is always on, the request temperature is forced to `1`.
- **`reasoning_effort` is a real, top-level parameter that both models accept** (despite the flash doc page saying otherwise; the legal set is exactly `low` / `high` / `max`, and `none` / `minimal` / `medium` / `xhigh` are all `400`). **The vendor default is `max`, so `high` is the middle tier, not the top one** — the name is a trap. Measured on `glm-5.3-flash`, reasoning tokens per turn:

  | tier | reasoning tokens, 8 runs | median |
  |---|---|---|
  | `low` | 7, 9, 29 (n=3) | ~9 |
  | **`high` (what we send)** | **7, 28, 43, 45, 49, 50, 58, 63** | **47** |
  | no field | 383, 542, 591, 709, 714, 741, 813, 827 | 711 |
  | `max` | 442, 569, 571, 584, 604, 616, 667, 959 | 594 |

  `max` and "no field" are the same distribution, which confirms the documented default: **omitting the field gives you `max`.** `high` is roughly **1/14** of it.

  The same ordering holds through OpenRouter (`high` 91–150, no field 632–1032, `max` 815–1669). We send `high` deliberately: it buys a fast, cheap chat model that barely deliberates. Raising it is a one-word change and the trade is latency and output tokens, not correctness.

- **At `high`, most easy turns return NO reasoning at all, and that is the model, not the transport.** Over 10 trivial questions, 6 came back with zero reasoning tokens — the model simply does not deliberate when it sees no need. This is the symptom to expect in the UI at this tier, and the only real fix is a deeper tier.

- **`thinking.clear_thinking: false` is sent because the vendor recommends it, and we could not measure any benefit.** The obvious hypothesis — that the default `clear_thinking: true` makes the server discard a short trace — was tested directly and does not hold: 7/10 zero-reasoning turns with it false against 6/10 with it default. An earlier apparent doubling of depth (66/74/94 against 31/61/139) did not survive a larger sample. It is kept as a vendor recommendation on a flag that costs nothing, not as a fix for anything.

**Both parameters used to be sent under an `extra_body` wrapper, and neither was ever applied.** There is an irony worth recording: because the wrapper ate `reasoning_effort: high`, the model had been running at the vendor default (`max`) all along, which is far deeper. v1.7.49 fixed the transport, and delivering the long-intended `high` cut reasoning about 10x — visible in production within a minute of the routing switch. v1.7.51 keeps `high` as an explicit cost choice, now stated as one in the code.

 `extra_body` is a Python-SDK convention that the Python client unwraps before sending; on the wire it is just an unknown object, which this endpoint ignores. It returned `200` every time, so nothing ever surfaced it. Fixed 2026-08-26 — both fields are now top-level, guarded by `TestZhipuSendsThinkingParamsAtTopLevel`, which asserts on the marshalled body because every check above that level passed throughout. Note this is a real behavior change on `glm-5.3`: it now actually runs at `high` instead of the server default.

Both OpenRouter routes are pinned to the `z-ai` upstream. On `z-ai/glm-5.3-flash` that pin is doing real work: Z.AI and Novita both serve fp8, but a Cloudflare host is listed at quantization `unknown` for twice the price, so an unpinned route can silently answer from different weights.

The OpenRouter window for `z-ai/glm-5.3` was **262,144 until 2026-08-26, and that was a bug, not a policy** — 262,144 is `kimi-k2.6`'s window and `glm-5.2`'s max output, never any GLM's context. Both routes now register 1,000,000, matching the native ones (`TestGLM53WindowsAgreeAcrossRoutes`).

The effect on a running bot is smaller than the registration difference suggests, because the number that governs compression is `EffectiveContextWindow` = **min(model window, `thread.contextWindowTokens`)**, and that config value defaults to 300,000. Every deployment today sits at the default, so this fix moves the OpenRouter GLM route from 262,144 to 300,000 — real, but ~14%, not 4x. The full 1M only becomes reachable on a deployment that also raises `contextWindowTokens`.
