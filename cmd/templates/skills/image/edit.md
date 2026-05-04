# Edit image (reference image(s) → image)

Image-conditioned generation via gpt-image-2. Use when the user provides one or more reference images and asks to change something on them, combine them, or produce variations of a subject.

## Workflow

1. Make sure each reference image is a real file on disk (PNG / JPEG / WebP). If the user attached an image to the chat, the path is already in conversation context — use it as-is, do not re-encode.
2. Compose a prompt that:
   - References each input as `image 1`, `image 2`, … in the order you list them on the command line.
   - States explicitly what should change AND what should be preserved (face, hair, body shape, pose, background, lighting). Without preservation hints, gpt-image-2 often replaces things you wanted to keep.
3. Run:
```
exec: {{WORKSPACE}}/bin/nagobot edit-image \
  --image <path1> [--image <path2> ...] \
  --prompt "<instruction>" \
  --size 1024x1024 --quality medium
```
4. Read the printed `path` and hand off to the `send-image` skill.

## Flags specific to edit

| Flag | Default | Notes |
|---|---|---|
| `--image` (required, repeatable) | — | Reference image. Add multiple `--image` flags for multi-reference compositing — the prompt then references them as `image 1` / `image 2`. PNG / JPEG / WebP. |
| `--prompt` (required) | — | Edit instruction. Reference inputs as `image 1`, `image 2`. |
| `--mask` | unset | Optional PNG mask. Transparent regions = the area to edit. Applies to the first `--image`. |

For shared flags (`--provider`, `--model`, `--size`, `--quality`, `--format`, `--compression`, `--output`) see the parent `SKILL.md`.

Each call returns exactly one image. For multiple variations (e.g. "5 different sunglasses"), describe them inside one prompt as a labeled grid — gpt-image-2 composes the whole set into a single image.

## Examples

### Single-image: replace clothing

```
exec: {{WORKSPACE}}/bin/nagobot edit-image \
  --image /Users/me/.nagobot/workspace/media/portrait.jpg \
  --prompt "Replace only the clothing with a charcoal wool overcoat. Preserve face, hair, body shape, hands, pose, background, and lighting exactly." \
  --quality medium
```

### Multi-image: compose two people

```
exec: {{WORKSPACE}}/bin/nagobot edit-image \
  --image /path/to/person1.jpg \
  --image /path/to/person2.jpg \
  --prompt "Studio portrait of the two women from image 1 and image 2 standing side by side. Preserve each face and hair faithfully. Clean studio background, even lighting, both women full-body." \
  --size 1536x1024 --quality medium
```

### Multi-variation grid: one prompt → 4 styles

```
exec: {{WORKSPACE}}/bin/nagobot edit-image \
  --image /path/to/portrait.jpg \
  --prompt "Show the person from image 1 in 4 different sunglasses styles arranged as a 2x2 labeled grid: aviator, round wireframe, wayfarer, cat-eye. Preserve face shape, hair, skin tone, and the original background. Label each cell with the style name." \
  --size 1536x1024 --quality medium
```

## Successful output

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

Paste / adapt these instead of writing from scratch:

- **Replace clothing, keep everything else** (fal.ai): "Change only the clothing. Keep the face, skin tone, body shape, hands, hair, expression, pose, background, camera angle, framing, and lighting exactly the same. Use a [outfit description]."
- **Multi-image clothing transfer** (fal.ai): "Dress the woman from Image 1 using the clothing from Images 2, 3, and 4. Preserve her face, facial features, skin tone, body shape, hands, pose, hair, expression, background, camera angle, framing, and lighting exactly. Replace only the clothing."
- **Strong identity preservation** (OpenAI cookbook): "Do not change her face, facial features, skin tone, body shape, pose, or identity in any way. Preserve her exact likeness, expression, hairstyle, and proportions."
- **Hairstyle grid** (sushilprompt): "Generate a 3x3 grid of avatars, each with a different hairstyle. Keep the outfit consistent."
- **Color comparison card**: "Show side-by-side [color/style] comparisons to highlight which suit the subject best. Visual-first, short labels only, no paragraphs."

## Trigger conditions

- User gives an image and asks to **change something on it** (clothing, hair, background, accessory, expression).
- User gives **multiple images** and asks to combine them (composite, group shot, swap features).
- User wants **multiple variations** of one subject — describe the variations inside the prompt as a labeled grid.
