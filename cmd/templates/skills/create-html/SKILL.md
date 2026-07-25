---
name: create-html
description: Build a self-contained HTML file, upload it via `nagobot upload-html`, and share the returned URL — or revise a page already published in this conversation and republish it over the same URL. Two flavors — precise SVG diagrams/charts/visualizations, and long-form layout pages (articles, reports, infographics, dashboards, presentation slides, landing pages). Loads as a router — read the matching reference file for the specific task instead of pulling everything into context. Best for documents, code, presentations / PPT, and interactive or dynamic display; prefer it over `gpt-image-2` when the result is structured (diagrams / charts) or needs interaction or animation.
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

## Revising a page you already published

If the page exists — you published it earlier in this conversation and the user is asking to change it — **edit the same file and republish over the same URL**:

1. `edit_file` (or `write_file`) the SAME `{{WORKSPACE}}/media/<name>.html` you uploaded before. Never create `<name>-v2.html`, `<name>-updated.html`, or a dated copy to hold the revision.
2. Republish, passing the URL the earlier upload returned:
```
exec: {{WORKSPACE}}/bin/nagobot upload-html <file-path> --replace <the-url-you-shared>
```
3. Tell the user the page was updated. The URL is unchanged — anything they bookmarked or forwarded now shows the new version, and they do not need a new link.

Rules:

- **One page = one local file, for its whole life.** The local filename is the handle to the published page; a second file means a second page.
- **`--replace` needs the URL**, and it must be a URL a previous `upload-html` actually returned. It fails loudly if that page does not exist — it will not invent one. If you cannot find the URL (an old conversation, a page someone else published), do not guess: publish a new page without `--replace` and hand over the new URL.
- **Publishing without `--replace` always mints a NEW URL.** The old page stays exactly as it was at its old URL, so the user is left holding a link to a stale version. That is why a revision must use `--replace`.

## Critical rules (apply to both)

- **Everything must be inline.** No external CSS, JS, fonts, or images. No `<link href>`, no `<script src>`, no `@import url()`. External resources WILL fail in the hosted environment — non-negotiable.
- **`<meta charset="utf-8">`** in `<head>`.
- **System fonts only**: `font-family: system-ui, -apple-system, sans-serif`. Add `"Noto Sans SC", "PingFang SC"` for Chinese text.
- **Output complete code.** Never truncate with "rest remains the same".
- Save to `{{WORKSPACE}}/media/` with a descriptive filename (e.g. `architecture-diagram.html`, `q1-revenue-report.html`). Name it for what the page IS, never for which version it is — no `-v2`, `-final`, `-updated`, no date suffix. Revisions overwrite the file in place (see above).

## When NOT to use this skill

- **Photorealistic images / generated art** → use the `image` skill (text-to-image / image edit via gpt-image-2).
- **Sending a file the user already has** → use `send-image` or hand the path off directly.
- **A quick text answer that doesn't need a hosted page** → just reply in chat.
