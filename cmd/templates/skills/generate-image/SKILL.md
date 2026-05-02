---
name: generate-image
description: Generate photorealistic, illustrative, or stylized images from a text prompt using OpenAI gpt-image-2. Returns the saved file path. Use when the user asks for a generated picture, art, illustration, photo-style image, or scene — not for diagrams/charts (use create-html-image instead).
tags: [image, generation, openai, gpt-image, photoreal, illustration]
---
# Generate Image

Generate an image from a text prompt and save it under `{{WORKSPACE}}/media/`. The CLI prints the saved file path. **This skill does not deliver the image** — to show it to the user, invoke the `send-image` skill afterwards (it owns channel compatibility, syntax, and fallback behavior).

## Workflow

1. Compose a clear, descriptive prompt (English usually performs best; include subject, style, lighting, framing).
2. Run:
```
exec: {{WORKSPACE}}/bin/nagobot generate-image --prompt "<prompt>" --size 1024x1024 --quality medium
```
3. Read the printed `path_0` (or `path_0`, `path_1`, … when `--n > 1`).
4. Hand the path off to the `send-image` skill — follow whatever delivery rules that skill specifies for the current channel. Do not reproduce its syntax from memory.

## Flags

| Flag | Default | Notes |
|---|---|---|
| `--prompt` (required) | — | Up to 32k chars. |
| `--provider` | `openai` | `openai` for direct API; `whatai` to route through the api.whatai.cc relay (useful when the host can't reach api.openai.com reliably — e.g. mainland China). |
| `--model` | `gpt-image-2` | Use `gpt-image-1` if you need transparent background (gpt-image-2 does not support it). |
| `--size` | `auto` | `auto` / `1024x1024` / `1536x1024` (landscape) / `1024x1536` (portrait). |
| `--quality` | `auto` | `low` (~$0.006) / `medium` (~$0.05) / `high` (~$0.21). Default to `medium` unless the user asks for draft or premium. |
| `--format` | `png` | `png` or `jpeg`. **`webp` is silently downgraded to PNG by gpt-image-2 — do not request it.** |
| `--compression` | unset | 0-100, `jpeg` only. Lower = smaller file, more artifacts. |
| `--n` | `1` | 1-10. Costs scale linearly with `n`. |
| `--output` | auto | If omitted, saves to `{{WORKSPACE}}/media/img-<timestamp>.<ext>`. With `--n > 1`, an index suffix is appended. |

## Example

```
exec: {{WORKSPACE}}/bin/nagobot generate-image \
  --prompt "A neon-lit Shibuya alley at night, 35mm photoreal, light rain" \
  --size 1536x1024 --quality medium
```

Successful output (frontmatter):

```yaml
---
command: generate-image
status: ok
model: gpt-image-2
count: "1"
input_tokens: "18"
output_tokens: "1372"
size: 1536x1024
quality: medium
format: png
path_0: /home/me/.nagobot/workspace/media/img-20260502-143012.png
---

/home/me/.nagobot/workspace/media/img-20260502-143012.png
```

Then invoke the `send-image` skill with `path_0` to deliver it — that skill controls how (or whether) the image is shown on the current channel.

## When to Use This Skill

- The user asks for a generated picture, photo, illustration, scene, character art, concept art, or stylized rendering.
- You want a vivid visual that exact code-rendered SVG cannot produce.

## When NOT to Use This Skill

- **Diagrams, charts, flowcharts, architecture sketches, data viz** → use `create-html-image` (precise SVG rendering).
- **Sending an image the user already provided** → just use `send-image` with the existing path.
- **Text-heavy graphics** (long paragraphs, multi-line UI mockups) → `create-html-image` is more reliable.

## Cost & Latency Notes

- `quality=high` on a portrait/landscape can take 30-90s and cost ~$0.20 per image. Default to `medium` unless the user asks for top quality.
- `n>1` multiplies cost — only use it when the user wants variations to pick from.
- Output is always returned as base64 from the API and decoded locally; no extra hosting step is needed.

## Provider Notes

- **`--provider openai`** (default): direct OpenAI API. Honors `--format`/`--compression` (output_format / output_compression). Pricing is token-based.
- **`--provider whatai`**: routes through api.whatai.cc, which uses DALL-E-3-style protocol internally. We always pass `response_format: b64_json` to force binary return. `--format`/`--compression` are silently ignored by the relay; output is whatever PNG the underlying model produces. Pricing is fixed `$0.04` per call regardless of `--quality`/`--size`/`--n`.
