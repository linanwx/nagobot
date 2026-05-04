# Generate image (text → image)

Pure text-to-image via gpt-image-2. Use when the user has NOT attached a reference image and wants a generated picture / illustration / photo / scene / character art.

## Workflow

1. Compose a clear, descriptive prompt. English usually performs best — include subject, style, lighting, framing.
2. Run:
```
exec: {{WORKSPACE}}/bin/nagobot generate-image --prompt "<prompt>" --size 1024x1024 --quality medium
```
3. Read the printed `path_0` (and `path_1`, … when `--n > 1`).
4. Hand the path to the `send-image` skill — that skill owns delivery rules for the current channel. Do not reproduce its syntax from memory.

## Flags specific to generate

| Flag | Default | Notes |
|---|---|---|
| `--prompt` (required) | — | Up to 32k chars. |
| `--n` | `1` | 1-10. Cost scales linearly. With `--n > 1`, output paths are `path_0` / `path_1` / … and an index suffix is appended to filenames. |

For shared flags (`--provider`, `--model`, `--size`, `--quality`, `--format`, `--compression`, `--output`) see the parent `SKILL.md`.

## Example

```
exec: {{WORKSPACE}}/bin/nagobot generate-image \
  --prompt "A neon-lit Shibuya alley at night, 35mm photoreal, light rain" \
  --size 1536x1024 --quality medium
```

Successful output:

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

Then invoke `send-image` with `path_0` to deliver.

## Trigger conditions

- User asks for a generated picture, photo, illustration, scene, character art, concept art, or stylized rendering.
- You want a vivid visual that exact code-rendered SVG cannot produce.

## Transparent background

gpt-image-2 cannot produce transparent PNGs. If the user needs an alpha channel, pass `--model gpt-image-1` (lower fidelity but supports transparency).
