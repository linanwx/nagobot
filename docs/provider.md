# Important

From my real-world testing, although OpenRouter is convenient for accessing models, it does not perform as well as calling official APIs directly for open-weight models, for the following reasons:

- Quantization standards vary across providers on OpenRouter, which leads to performance degradation and a very high function-calling failure rate.
- OpenRouter may randomly route your requests to different providers, making cache hits unlikely and increasing costs.

# Provider Config Examples

DeepSeek Search + Fetch (official API):

```yaml
providers:
  deepseek:
    apiKey: YOUR_DEEPSEEK_API_KEY
```

Configure with `nagobot set-provider-key --provider deepseek --api-key YOUR_KEY`.
Both `web_search` and `web_fetch` expose `source: "deepseek"` when an official
DeepSeek key is configured. `DEEPSEEK_API_KEY` takes precedence over
`providers.deepseek.apiKey`; credentials are re-read at call time. The chat
provider and model routing do not affect this source, and no dedicated search
or fetch key is needed. OpenCode Go credentials cannot authenticate these APIs.

Requests use Bearer authentication at fixed official endpoints:
`POST https://api.deepseek.com/v1/tools/search` and
`POST https://api.deepseek.com/v1/tools/fetch`. Custom LLM `apiBase` settings do
not redirect these tools. Search and Fetch use dedicated HTTP requests, not
chat completions or another provider's SDK.

Search accepts 1–128 Unicode characters and `max_results` from 1 to 50, retaining
the service's ranking and using summaries (or snippets when no summary is
available). Fetch accepts an HTTP(S) webpage URL with a host, no embedded
credentials, and at most 8,192 characters, returning Markdown through the
existing fetch cache and pagination. It is a remote page extractor, not an
arbitrary file downloader; use `raw` or `go-readability` for file downloads.
Omitting `source` on either tool returns a source guide without an API request.

As of 2026-10-17, each API costs ¥5 per 1,000 successful, non-empty requests and
has a separate account-level limit of 5 QPS. Fetch also has per-domain limits.
Search's result count does not change its per-request price. Failed, empty and
timed-out requests are not billed. Temporary free offers for server-side tools
do not make these standalone APIs free. A configured key indicates availability,
not verified balance or a successful live request. See the official
[Search + Fetch guide](https://api-docs.deepseek.com/zh-cn/guides/search/),
[Search API](https://api-docs.deepseek.com/api/search/) and
[Fetch API](https://api-docs.deepseek.com/api/fetch/).

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

OpenCode Go (Haiku / Qwen / GLM / DeepSeek / Step):

```yaml
thread:
  provider: opencode-go
  modelType: glm-5.3-flash

providers:
  opencodeGo:
    apiKey: YOUR_OPENCODE_GO_KEY
    # apiBase: https://opencode.ai/zen/go/v1 # optional
```

Supported models: `claude-haiku-5-5`, `glm-5.3-flash`, `glm-5.3`,
`deepseek-v4.1-flash`, `deepseek-v4-pro`, `qwen3.8-flash`, and `step-5-preview-free`.
Haiku supports `[low]`, `[medium]`, `[high]`, `[xhigh]`, and `[max]`, for example
`claude-haiku-5-5[low]`. Its effort uses `output_config.effort` on the Go
Messages endpoint, with its own `x-api-key` authentication. It uses adaptive
thinking and preserves signed thinking blocks, including blocks with empty
thinking text. Binding mismatch controls let the service drop a stale block
after prompt changes or compression. No sampling parameters are sent.

Qwen3.8 Flash uses the Go `/messages` endpoint with `x-api-key`, independently
of Haiku's adaptive-thinking policy. It supports `[none]`, `[low]`, `[medium]`,
and `[xhigh]`. The bare model enables thinking and omits effort to preserve the
server default; `[none]` disables thinking. Other suffixes use
`output_config.effort`. No fixed thinking budget, adaptive thinking, binding
controls or Haiku beta header is sent. Unsigned Qwen thinking is streamed and
replayed on tool continuation. See the [Go model catalog](https://github.com/anomalyco/models.dev/blob/dev/providers/opencode-go/models/qwen3.8-flash.toml)
and [Messages protocol](https://help.aliyun.com/en/model-studio/anthropic-api-messages).

GLM and DeepSeek Flash support `[low]`, `[high]`, and `[max]`
reasoning effort suffixes, for example `deepseek-v4.1-flash[max]`; DeepSeek Pro
supports `[high]` and `[max]`. A bare model id leaves effort at the server
default. Requests use the gateway's standard `reasoning_effort` parameter;
native `thinking` / `clear_thinking` fields are omitted because some Go
upstreams reject them. GLM uses temperature 1 and DeepSeek omits temperature.
Step 5 Preview Free uses `/chat/completions` with Bearer authentication and
supports `[low]`, `[medium]`, and `[high]` through `reasoning_effort`. A bare
model omits effort. The exact `step-5-preview-free` id is sent to Go; it is not
rewritten to the native StepFun model id. Reasoning is replayed through
`reasoning_content`, and no native thinking extension or temperature is sent.
The Go route's output limit is 65,536 tokens; larger configured limits are
capped at that value. See the [Go model catalog](https://github.com/anomalyco/models.dev/blob/dev/providers/opencode-go/models/step-5-preview-free.toml).

All seven register a 1,000,000-token
context window, still capped by `thread.contextWindowTokens`. Image input is
enabled for `claude-haiku-5-5`, `qwen3.8-flash`, `glm-5.3-flash`,
`deepseek-v4.1-flash`, and `step-5-preview-free`.

Configure with `nagobot set-provider-key --provider opencode-go --api-key YOUR_KEY`,
then `nagobot set-model --default --provider opencode-go --model glm-5.3-flash`.
`OPENCODE_GO_API_KEY` and `OPENCODE_GO_API_BASE` override the saved credentials
and endpoint. The dedicated `internal/opencodego` inference SDK owns the model
catalog, request serialization, authentication, HTTP transport, SSE parser,
tool continuation, reasoning replay and usage accounting. It has no dependency
on other Provider implementations or on OpenAI/Anthropic SDKs. Haiku and Qwen use
`/messages`; GLM, DeepSeek and Step use `/chat/completions`. Both use nagobot's own
User-Agent and a stable, hashed `x-opencode-session` per conversation, including
tool iterations and subsequent turns. Token/cache usage is recorded under
`opencode-go`; subscription balance is unavailable through a public API.
Comment-only SSE keep-alives are ignored; malformed JSON and truncated streams
remain errors. Stream failures are not retried after generation begins.

This repository-owned inference SDK is separate from the official
[`opencode-sdk-go`](https://github.com/anomalyco/opencode-sdk-go), which controls
OpenCode's local agent server and does not expose Go subscription inference.

In the [OpenCode console](https://opencode.ai/auth), open **Settings → Service
accounts**, create an account, then open it and choose **Add API Key** with
**Inference only** permissions. Copy the key when it is shown; it is displayed
only once. See the [service account documentation](https://opencode.ai/v2/docs/console/api#service-accounts).
Check subscription usage on the console's **Go** page. DeepSeek Go requests
require **Global** regions in the workspace's **Settings → Privacy**; with a
different region selection, the service rejects them even when the key is valid.
See [OpenCode Go's client requirements](https://opencode.ai/docs/go/#where-can-i-use-it)
for its intended coding-agent traffic and
[model endpoints](https://opencode.ai/docs/go/#endpoints).
Other Go models are not registered by this adapter.

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
| `reasoning_effort` | vendor default (`max`), or a tier from an `[low\|high\|max]` suffix | same |
| OpenRouter route | `z-ai/glm-5.3` | `z-ai/glm-5.3-flash` |
| price (OpenRouter, per 1M) | $1.40 in / $4.40 out | $0.075 in / $0.25 out |

Everything above was measured against the live API on 2026-08-26, and two of the rows contradict the vendor documentation:

- **`thinking.type` accepts only `enabled`.** `disabled` — and every other value — is a `400` on both models (`该模型始终思考，不支持关闭思考`). Since thinking is always on, the request temperature is forced to `1`.
- **`reasoning_effort` is a real, top-level parameter that both models accept** (despite the flash doc page saying otherwise; the legal set is exactly `low` / `high` / `max`, and `none` / `minimal` / `medium` / `xhigh` are all `400`). **The vendor default is `max`, so `high` is the middle tier, not the top one** — the name is a trap. Measured on `glm-5.3-flash`, reasoning tokens per turn:

  | tier | reasoning tokens, 8 runs | median |
  |---|---|---|
  | `low` | 7, 9, 29 (n=3) | ~9 |
  | `high` (what we used to send) | 7, 28, 43, 45, 49, 50, 58, 63 | 47 |
  | no field | 383, 542, 591, 709, 714, 741, 813, 827 | 711 |
  | `max` | 442, 569, 571, 584, 604, 616, 667, 959 | 594 |

  `max` and "no field" are the same distribution, which confirms the documented default: **omitting the field gives you `max`.** `high` is roughly **1/14** of it.

  The same ordering holds through OpenRouter (`high` 91–150, no field 632–1032, `max` 815–1669).

  **The tier is now chosen per model rule, by a bracket suffix, and a bare alias sends no field at all.** `glm-5.3` and `glm-5.3-flash` are registered with `[low]` / `[high]` / `[max]` variants alongside the bare id (`zhipuWithEffortVariants`, the same mechanism DeepSeek uses). A bare `glm-5.3-flash` omits `reasoning_effort` entirely and gets the vendor default — which on this family is the DEEPEST tier, so the safe default is the absent one. `TestZhipuEffortTierRidesTheBracket` asserts the absence, not just the values.

- **Sending `high` costs ~4x the reasoning on an answer-only turn and ~36x on a tool-calling turn, and that second number is why this is no longer shipped.** The penalty is not a constant factor on the tier — it interacts with tool availability. Measured live on `glm-5.3-flash`, n=5, identical request, streamed:

  | turn shape | `high` median | field omitted | ratio |
  |---|---|---|---|
  | answer only | 781 | 2989 | 3.8x |
  | **tools offered, model calls one** | **53** | **1931** | **36x** |

  53 reasoning tokens is not deliberation, it is a rounding error — so inside an agentic loop, where nearly every turn calls a tool, `high` reads as *literally zero thinking*. `glm-5.3` reproduces it (88–107 against 1407–2991).

  **What this looks like in production is the model doing its thinking in `exec`.** Confirmed from the deployment's own logs rather than inferred: over 24h and 131 responses, **85.2% of tool-calling turns returned zero reasoning tokens**, and across all history there are **47 turns where the model called `bash` with a bare `echo` of prose** — a scratchpad, not a command — every one of them on `glm-5.3-flash`, against zero such calls in ~3000 `exec` calls from 19 other models. 46 of the 46 prose-length ones sat on turns with `reasoning_tokens == 0`. The tightest control available is same-session, same-agent, model-swapped: `soul` at 60 exec / 5 echo on GLM against 18 exec / 0 echo on DeepSeek. Denied a reasoning budget, the model bought one back through the tool loop — at the cost of a round trip per thought.

  Ruled out with evidence before landing on the tier: transport and parsing (`reasoningInResponse` agreed with `reasoningTokens > 0` in 131/131), context length (82K of filler still reasoned), the model imitating its own echo history (a clean session is also zero), tool availability alone, and the `reasoning_content` echo-back the vendor requires for interleaved thinking — which this codebase already does (`provider/openrouter.go`, `extras["reasoning_content"]`) and which changed nothing in an A/B at step 2 of a loop.

  The `high` × tool-call interaction appears to be unreported — a web search turned up the enum and the default, and nothing on either this penalty or on echo-as-scratchpad.

- **`thinking.clear_thinking: false` is not a depth dial and never was.** The hypothesis it was once kept for — that the default `true` makes the server discard a short trace — was tested head-to-head and refuted: 7/10 zero-reasoning turns with it false against 6/10 with it default, and an apparent doubling of depth (66/74/94 against 31/61/139) did not survive a larger sample. What it actually controls is whether reasoning blocks from PREVIOUS turns survive into this one, which is the vendor's Preserved Thinking and the thing `toOpenAIChatMessages` feeds by echoing `reasoning_content` back on assistant messages. It stays `false` for that reason, on a flag that costs nothing — not as a fix for zero-reasoning turns, which it does not affect.

**Both parameters used to be sent under an `extra_body` wrapper, and neither was ever applied — which is the whole reason `high` ever shipped.** Because the wrapper ate `reasoning_effort: high`, the model had been running at the vendor default (`max`) all along. v1.7.49 fixed the transport and kept the value the broken code had *intended* to send; delivering the long-intended `high` cut reasoning about 10x, visible in production within a minute of the routing switch, and v1.7.51 restated it as a deliberate cost choice. It was not one — it was a value nobody had ever observed in effect.

**A parameter that was never delivered has no known-good value. Restoring transport is not restoring behaviour, and the value has to be re-measured as if it were new.** That is the lesson worth keeping from this whole sequence: the regression survived two releases because "the code always meant to send `high`" reads as provenance and is not.

 `extra_body` is a Python-SDK convention that the Python client unwraps before sending; on the wire it is just an unknown object, which this endpoint ignores. It returned `200` every time, so nothing ever surfaced it. Fixed 2026-08-26 — both fields are now top-level, guarded by `TestZhipuSendsThinkingParamsAtTopLevel`, which asserts on the marshalled body because every check above that level passed throughout. Note the guard survives the tier change unaltered: what it pins is that these fields reach the wire top-level at all.

Both OpenRouter routes are pinned to the `z-ai` upstream. On `z-ai/glm-5.3-flash` that pin is doing real work: Z.AI and Novita both serve fp8, but a Cloudflare host is listed at quantization `unknown` for twice the price, so an unpinned route can silently answer from different weights.

The OpenRouter window for `z-ai/glm-5.3` was **262,144 until 2026-08-26, and that was a bug, not a policy** — 262,144 is `kimi-k2.6`'s window and `glm-5.2`'s max output, never any GLM's context. Both routes now register 1,000,000, matching the native ones (`TestGLM53WindowsAgreeAcrossRoutes`).

The effect on a running bot is smaller than the registration difference suggests, because the number that governs compression is `EffectiveContextWindow` = **min(model window, `thread.contextWindowTokens`)**, and that config value defaults to 300,000. Every deployment today sits at the default, so this fix moves the OpenRouter GLM route from 262,144 to 300,000 — real, but ~14%, not 4x. The full 1M only becomes reachable on a deployment that also raises `contextWindowTokens`.
