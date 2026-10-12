# OpenCode Go inference SDK

This is nagobot's private Go SDK for the OpenCode Go inference gateway. It is
not an official OpenCode library. The official `opencode-sdk-go` controls an
OpenCode agent server and does not expose this inference API.

The package depends only on the Go standard library. It owns its model catalog,
wire types, authentication, request policy, HTTP retries, SSE parsing, tool
continuation, signed reasoning replay and token accounting. It does not import
the provider package, other providers' implementations, or their SDKs.

`Client.Chat` selects the gateway protocol from its own registered model:

| Model | Endpoint | Authentication | Effort |
| --- | --- | --- | --- |
| Claude Haiku 5.5 | `/messages` | `x-api-key` + `anthropic-version` | `output_config.effort` |
| GLM 5.3 / Flash | `/chat/completions` | Bearer | `reasoning_effort` |
| DeepSeek V4.1 Flash / V4 Pro | `/chat/completions` | Bearer | `reasoning_effort` |

Haiku uses adaptive thinking and signed block binding controls. Signed blocks
with empty thinking text are retained; prefix mismatch controls allow stale
blocks to be dropped after changes to the system prompt or compressed history.
The Chat Completions models do not receive the native `thinking` extension.

Both protocols send `User-Agent: nagobot/1.0` and `x-opencode-session`. Callers
supply the stable conversation identifier; a client-local fallback is used for
standalone requests. Comment-only and empty SSE events are keep-alives, not JSON.
Corrupt payloads and streams missing terminal events remain errors. Only HTTP
and connection failures before generation begins can be retried.

Haiku input usage includes uncached input, cache reads and cache writes in the
normalized total; Chat Completions already reports total input. Cache buckets
remain available separately. Repeated cumulative usage is not added repeatedly.

Owning sources:

- [OpenCode Go endpoints and client requirements](https://opencode.ai/docs/go/)
- [Go model metadata](https://github.com/anomalyco/models.dev/tree/dev/providers/opencode-go/models)
- [OpenCode inference authentication](https://github.com/anomalyco/opencode/blob/dev/packages/console/app/src/lib/inference-proxy.ts)
- [Haiku 5.5 migration and thinking](https://platform.claude.com/docs/en/models/haiku-5-5/migration-guide)
- [Preserved thinking and binding controls](https://platform.claude.com/docs/en/build-with-claude/preserved-thinking)
