---
name: generate-image
description: Generate photorealistic, illustrative, or stylized images from a text prompt using OpenAI gpt-image-2. Returns the saved file path. Use when the user asks for a generated picture, art, illustration, photo-style image, or scene — not for diagrams/charts (use create-html-image instead).
tags: [image, generation, openai, gpt-image, photoreal, illustration]
---
# Generate Image

Generate an image from a text prompt and save it under `{{WORKSPACE}}/media/`. The CLI prints the saved file path; you then deliver the image to the user with the `send-image` skill (`![](path)` markdown).

## Workflow

1. Compose a clear, descriptive prompt (English usually performs best; include subject, style, lighting, framing).
2. Run:
```
exec: {{WORKSPACE}}/bin/nagobot generate-image --prompt "<prompt>" --size 1024x1024 --quality medium
```
3. Read the printed `path_0` (or `path_0`, `path_1`, … when `--n > 1`).
4. Send the image with the `send-image` skill: include `![alt](path)` somewhere in your reply.

## Flags

| Flag | Default | Notes |
|---|---|---|
| `--prompt` (required) | — | Up to 32k chars. |
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

Then in your reply to the user:

```
Here you go: ![Shibuya alley](/home/me/.nagobot/workspace/media/img-20260502-143012.png)
```

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

## Channel Compatibility

The generated file lives at a real filesystem path. To reach the user, pair this skill with `send-image` — the channel layer (Discord, WeCom) will then upload it as a native attachment. On channels that don't support `send-image`, share the path or fall back to text.
