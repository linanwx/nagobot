---
name: create-html
description: Build a self-contained HTML file, upload it via `nagobot upload-html`, and share the returned URL. Two flavors — precise SVG diagrams/charts/visualizations, and long-form layout pages (articles, reports, infographics, dashboards, presentation slides, landing pages). Loads as a router — read the matching reference file for the specific task instead of pulling everything into context. Best for documents, code, presentations / PPT, and interactive or dynamic display; prefer it over `gpt-image-2` when the result is structured (diagrams / charts) or needs interaction or animation.
---
# Create HTML (image / singlepage)

Both flavors produce one self-contained HTML file, upload it through `nagobot upload-html` (Cloudflare R2 backed), and return a URL to share with the user.

## Pick the right reference file

Read only the file for the task at hand — do NOT read both.

| Task | Reference file |
|---|---|
| Precise diagram / chart / data viz / architecture sketch — visual-first, SVG-driven | `{{SKILLDIR}}/image.md` |
| Long-form page — article, report, infographic, dashboard, slide, landing page | `{{SKILLDIR}}/singlepage.md` |

If the user wants a graphic where layout precision matters more than reading flow → `image.md`. If the user wants a page that reads top-to-bottom with sections, headings, paragraphs → `singlepage.md`.

## Shared workflow

1. Write the file with `write_file` to `{{WORKSPACE}}/media/<descriptive-name>.html`.
2. Upload it:
```
exec: {{WORKSPACE}}/bin/nagobot upload-html <file-path>
```
3. Share the returned URL with the user.

## Critical rules (apply to both)

- **Everything must be inline.** No external CSS, JS, fonts, or images. No `<link href>`, no `<script src>`, no `@import url()`. External resources WILL fail in the hosted environment — non-negotiable.
- **`<meta charset="utf-8">`** in `<head>`.
- **System fonts only**: `font-family: system-ui, -apple-system, sans-serif`. Add `"Noto Sans SC", "PingFang SC"` for Chinese text.
- **Output complete code.** Never truncate with "rest remains the same".
- Save to `{{WORKSPACE}}/media/` with a descriptive filename (e.g. `architecture-diagram.html`, `q1-revenue-report.html`).

## When NOT to use this skill

- **Photorealistic images / generated art** → use the `image` skill (text-to-image / image edit via gpt-image-2).
- **Sending a file the user already has** → use `send-image` or hand the path off directly.
- **A quick text answer that doesn't need a hosted page** → just reply in chat.
