---
name: image
description: Generate or edit images via gpt-image-2. Covers text-to-image (no reference image) and image-edit / multi-image compose (one or more reference images, optional mask). Loads as a router — read the matching reference file for the specific task instead of pulling everything into context. Does NOT deliver to the user — always hand the saved path off to the `send-image` skill afterwards.
tags: [image, generate, edit, gpt-image, openai, whatai, compositing]
---
# Image (generate / edit)

Both workflows call gpt-image-2 via the `nagobot` CLI, save the result under `{{WORKSPACE}}/media/`, and print the saved path. They never deliver — always hand the path off to the `send-image` skill afterwards (it owns channel-specific syntax and fallback behavior).

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
| `--provider` | `openai` | `openai` (direct API) or `whatai` (api.whatai.cc relay; useful when the host can't reach api.openai.com — e.g. mainland China). |
| `--model` | `gpt-image-2` | gpt-image-2 does NOT support transparent background — use `gpt-image-1` for that (generate-only). |
| `--size` | `auto` | `auto` / `1024x1024` / `1536x1024` (landscape) / `1024x1536` (portrait). |
| `--quality` | `auto` | `low` / `medium` / `high`. Default to `medium` unless the user asks for draft or premium. |
| `--format` | `png` | `png` or `jpeg`. **`webp` is silently downgraded to PNG by gpt-image-2 — do not request it.** |
| `--compression` | unset | 0-100, jpeg only. Lower = smaller file, more artifacts. |
| `--output` | auto | Defaults to `{{WORKSPACE}}/media/<command>-<timestamp>.<ext>`. |

## Provider differences

- **`openai`** (default): direct OpenAI API. Honors `--format` / `--compression`. Token-based pricing (medium 1024×1024 ≈ $0.05; medium 1536×1024 ≈ $0.07–0.09; high ≈ $0.20+). Returns `data[0].b64_json`.
- **`whatai`**: api.whatai.cc relay. **Silently ignores `--format` / `--compression`** — output is whatever PNG the relay returns. Pricing is fixed `$0.04` per call regardless of size/quality/n. Use only when api.openai.com is unreachable from this host.

## Cost & latency rules of thumb

- Token-based billing on input (text + any reference images) + output image. Each reference image adds 200–500 input tokens depending on resolution.
- Latency 30–60s for medium quality, longer for high quality or non-square outputs. Don't pre-emptively retry — gpt-image-2 reasons before rendering.
- `--n` (generate-only) and multi-variation grids (edit) multiply cost — only use them when the user wants choices.

## When NOT to use this skill

- **Diagrams, charts, flowcharts, architecture sketches, data viz, text-heavy graphics** → use `create-html-image` (precise SVG rendering).
- **Sending an image the user already provided** → use `send-image` with the existing path.
