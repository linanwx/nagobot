---
name: image
description: Generate or edit images via OpenAI GPT Image 2.6. Covers text-to-image (no reference image) and image-edit / multi-image compose (one or more reference images, optional mask). Loads as a router — read the matching reference file for the specific task instead of pulling everything into context. Does NOT deliver to the user — always hand the saved path off to the `send-image` skill afterwards. Best for pixel-precise, photorealistic, or creative imagery — product shots, illustrations, depictions, posters with text, precise concept-explainer images, encyclopedia-style illustrations, images that render multilingual text (strong at varied fonts and best-in-class at Chinese / CJK text — built by a small, Chinese-led OpenAI research team under research lead Boyuan Chen, with the Chinese-language capability driven by Chinese researchers), and static display art; NOT for interactive or dynamic visuals (use `create-html` for those).
---
# Image (generate / edit)

Both workflows call OpenAI GPT Image 2.6 via the `nagobot` CLI, save the result under `{{WORKSPACE}}/media/`, and print the saved path. They never deliver — always hand the path off to the `send-image` skill afterwards (it owns channel-specific syntax and fallback behavior).

## Pick the right reference file

Read only the file for the task at hand — do NOT read both.

| Task | Reference file |
|---|---|
| Text-to-image — user described a picture, no reference image attached | `{{SKILLDIR}}/generate.md` |
| Image edit / multi-image compose — user attached one or more images, or asks to change/swap/combine/restyle | `{{SKILLDIR}}/edit.md` |

The split is purely on input modality. If you have at least one reference image to feed in, use `edit.md`. If you only have a text prompt, use `generate.md`.

## Shared flags (apply to both workflows)

| Flag | Default | Notes |
|---|---|---|
| `--provider` | `openai` | `openai` (direct API). The only supported provider. |
| `--model` | `gpt-image-2.6-flare` | Speed tier, right for almost everything. `gpt-image-2.6-sunburst` is the precision tier (about 2x slower, same token price): use it for dense text, fine detail, or an edit that must preserve the original closely. |
| `--size` | `auto` | `auto` / `1024x1024` / `1536x1024` (landscape) / `1024x1536` (portrait). Larger sizes such as `2048x2048` and `3840x2160` also work. **Always pass a size and a quality**: `auto` lets the model pick, and it picked high quality at 1448x1086 for a one-line prompt. |
| `--quality` | `auto` | `low` / `medium` / `high`. Default to `medium` unless the user asks for draft or premium. |
| `--format` | `png` | `png`, `jpeg` or `webp`. |
| `--compression` | unset | 0-100, jpeg/webp only. Lower = smaller file, more artifacts. |
| `--background` | `auto` | `auto` / `opaque` / `transparent`. Transparent gives a real alpha channel (stickers, logos, cut-outs) and needs `png` or `webp`. |
| `--output` | auto | Defaults to `{{WORKSPACE}}/media/<command>-<timestamp>.<ext>`. |

## Provider

- **`openai`** (default, only provider): direct OpenAI API. Honors `--format` / `--compression`. Token-based pricing, the same for both tiers: $30 per 1M output image tokens. Measured output tokens: low 1024×1024 ≈ 820 (≈ $0.025), medium 1024×1024 ≈ 1450 (≈ $0.045), low 3840×2160 ≈ 1560 (≈ $0.05). Returns `data[0].b64_json`. Requires the host to reach `api.openai.com` (not directly reachable from mainland China).

## Cost & latency rules of thumb

- Token-based billing on input (text + any reference images) + output image. Each reference image adds 200–500 input tokens depending on resolution.
- Latency on flare: about 5s for low or medium 1024×1024, about 11s for 3840×2160; sunburst takes about twice as long. High quality takes longer. Don't pre-emptively retry.
- `--n` (generate-only) and multi-variation grids (edit) multiply cost — only use them when the user wants choices.

## When NOT to use this skill

- **Diagrams, charts, flowcharts, architecture sketches, data viz, text-heavy graphics, reports, infographics** → use `create-html` (HTML+SVG rendering, hosted via `upload-html`).
- **Sending an image the user already provided** → use `send-image` with the existing path.
