---
name: edit-image
description: Edit or compose with one or more reference images using gpt-image-2. Use when the user provides an image and asks to change something about it (replace clothing, swap background, add/remove an object, restyle), or provides multiple images and asks to combine them (e.g. "put the person from image 1 in the outfit from image 2", "make a group photo of these two people"). Returns a single saved file path.
tags: [image, edit, gpt-image-2, reference, compositing, openai]
---
# Edit Image

Run an edit / compose call against gpt-image-2 with one or more reference images and a prompt. The CLI saves the result under `{{WORKSPACE}}/media/` and prints the path. Delivery to the user happens through the separate `send-image` skill.

## Workflow

1. Make sure each reference image is a real file on disk (PNG / JPEG / WebP). If the user just attached an image to the chat, the path will be in the conversation context — use it as-is, do not re-encode.
2. Compose a prompt that:
   - References each input as `image 1`, `image 2`, ... in the order you list them on the command line
   - Says explicitly what should change AND what should be preserved (face, hair, body shape, pose, background, lighting). Without preservation hints, the model often replaces things you wanted to keep.
3. Run:
```
exec: {{WORKSPACE}}/bin/nagobot edit-image \
  --image <path1> [--image <path2> ...] \
  --prompt "<instruction>" \
  --size 1024x1024 --quality medium
```
4. Read the printed `path` and hand it off to the `send-image` skill.

## Flags

| Flag | Default | Notes |
|---|---|---|
| `--image` (required, repeatable) | — | Reference image. Add multiple `--image` flags for multi-reference compositing (gpt-image-2 lets the prompt say "image 1" / "image 2"). PNG / JPEG / WebP. |
| `--prompt` (required) | — | Edit instruction. Reference inputs as "image 1", "image 2". |
| `--mask` | unset | Optional PNG mask. Transparent regions = the area to edit. Applies to the first `--image`. |
| `--provider` | `openai` | `openai` (direct) or `whatai` (relay; useful when the host can't reach api.openai.com). |
| `--model` | `gpt-image-2` | Model name. |
| `--size` | `auto` | `auto` / `1024x1024` / `1536x1024` (landscape) / `1024x1536` (portrait). Default `auto` lets the model pick based on inputs. |
| `--quality` | `auto` | `low` / `medium` / `high`. Default to `medium` unless the user explicitly wants a draft or premium. |
| `--format` | `png` | `png` or `jpeg`. |
| `--compression` | unset | 0-100, jpeg only. |
| `--output` | auto | Defaults to `{{WORKSPACE}}/media/edit-<timestamp>.<ext>`. |

Each call returns exactly one image. If the user wants multiple variations (e.g. "show me 5 different sunglasses on this person"), describe the variations inside the prompt as a labeled grid — gpt-image-2 composes the whole set into a single image.

## Single-image example (replace clothing)

```
exec: {{WORKSPACE}}/bin/nagobot edit-image \
  --image /Users/me/.nagobot/workspace/media/portrait.jpg \
  --prompt "Replace only the clothing with a charcoal wool overcoat. Preserve face, hair, body shape, hands, pose, background, and lighting exactly." \
  --quality medium
```

## Multi-image example (compose two people into one shot)

```
exec: {{WORKSPACE}}/bin/nagobot edit-image \
  --image /path/to/person1.jpg \
  --image /path/to/person2.jpg \
  --prompt "Studio portrait of the two women from image 1 and image 2 standing side by side. Preserve each face and hair faithfully. Clean studio background, even lighting, both women full-body." \
  --size 1536x1024 --quality medium
```

## Multi-variation example (one prompt → grid output)

When the user wants several styles on the same person, ask the model to produce a labeled grid in the prompt itself:

```
exec: {{WORKSPACE}}/bin/nagobot edit-image \
  --image /path/to/portrait.jpg \
  --prompt "Show the person from image 1 in 4 different sunglasses styles arranged as a 2x2 labeled grid: aviator, round wireframe, wayfarer, cat-eye. Preserve face shape, hair, skin tone, and the original background. Label each cell with the style name." \
  --size 1536x1024 --quality medium
```

## Successful output (frontmatter)

```yaml
---
command: edit-image
status: ok
provider: openai
model: gpt-image-2
images: "2"
input_tokens: "460"
output_tokens: "1372"
size: 1536x1024
quality: medium
format: png
path: /Users/me/.nagobot/workspace/media/edit-20260502-143012.png
---

/Users/me/.nagobot/workspace/media/edit-20260502-143012.png
```

Then invoke `send-image` with `path` to deliver.

## Prompt patterns observed in the wild

Real prompts users have shipped against gpt-image-2 image edits — paste/adapt these instead of writing from scratch:

- **Replace clothing, keep everything else** (fal.ai guide): "Change only the clothing. Keep the face, skin tone, body shape, hands, hair, expression, pose, background, camera angle, framing, and lighting exactly the same. Use a [outfit description]."
- **Multi-image clothing transfer** (fal.ai guide): "Dress the woman from Image 1 using the clothing from Images 2, 3, and 4. Preserve her face, facial features, skin tone, body shape, hands, pose, hair, expression, background, camera angle, framing, and lighting exactly. Replace only the clothing."
- **Strong identity preservation** (OpenAI cookbook): "Do not change her face, facial features, skin tone, body shape, pose, or identity in any way. Preserve her exact likeness, expression, hairstyle, and proportions."
- **Hairstyle grid** (sushilprompt): "Generate a 3x3 grid of avatars, each with a different hairstyle. Keep the outfit consistent."
- **Color comparison card**: "Show side-by-side [color/style] comparisons to highlight which suit the subject best. Visual-first, short labels only, no paragraphs."

## When to Use This Skill

- The user gives an image and asks to **change something on it** (clothing, hair, background, accessory, expression).
- The user gives **multiple images** and asks to combine them (composite, group shot, swap features).
- The user wants **multiple variations** of one person/object — describe the variations inside the prompt as a labeled grid; gpt-image-2 produces them in a single image.

## When NOT to Use This Skill

- **No reference image** → use `generate-image` (text-to-image only).
- **The user wants a precise diagram / chart** → use `create-html-image` (SVG rendering).
- **The user just wants you to send back an image they already have** → use `send-image` with the existing path.

## Cost & Latency

- Token-based billing on input image(s) + text + output image. medium quality 1024×1024 ≈ \$0.05 per call; 1536×1024 medium ≈ \$0.07–0.09.
- Multi-reference calls cost more on input tokens (each image is billed). 2 references typically 200–500 input tokens depending on resolution.
- Latency is **30–60s** for medium, longer for high-quality non-square outputs. Don't pre-emptively retry on slow responses; gpt-image-2 reasons before rendering.

## Provider Notes

- **`--provider openai`** (default): direct OpenAI API. Honors `output_format` and `output_compression`. Returns `data[0].b64_json`.
- **`--provider whatai`**: api.whatai.cc relay. We always pass `response_format: b64_json` since the relay defaults to a URL response. `output_format` / `output_compression` are silently ignored by the relay. Pricing on whatai is fixed `$0.04` per call regardless of size/quality.
