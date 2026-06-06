---
name: send-docs
description: Send a local file (PDF, document, archive, code, log, generated HTML, anything non-image) to the user as a native channel attachment by writing standard Markdown link syntax `[label](path)` in your reply. Supported on Discord and WeCom; falls back to plain text on other channels.
---
# Send Docs

Send local files to the user by writing standard Markdown link syntax inline in your reply text. The platform parses your reply, finds links whose target is a real file on disk, and uploads each one as a native channel attachment — without modifying the text you wrote.

This is the non-image counterpart to `send-image`: same wiring, same workspace-relative path rules, but for arbitrary file types.

## Syntax

Standard Markdown link:

```
[label](path)
```

- `label` — short description (may be empty: `[](path)`).
- `path` — an absolute filesystem path (e.g. `{{WORKSPACE}}/media/report.pdf`).

The link can appear **anywhere** in your reply, including mid-paragraph. It does not need its own line.

## Examples

Inline in a sentence:

```
Here is the analysis [Q1 report](media/q1-report.pdf) — see page 3 for the spike.
```

On its own line:

```
Sure, here is the dataset.

[raw data](/Users/me/.nagobot/workspace/media/dataset.csv)
```

Multiple files in one message:

```
Two artefacts: [build log](media/build.log) and [bundle](media/dist.zip).
```

## What gets uploaded vs treated as plain text

The platform only uploads if the path resolves to a **real local file**. The following are left as plain Markdown links and **not** uploaded:

- URLs (`https://...`, `http://...`, anything containing `://`)
- `mailto:` / `tel:` schemes
- In-page anchors (`#section`)
- Paths to files that don't exist on disk
- Empty files (zero bytes)
- Directories

This means you can freely intermix hyperlinks (`[Discord docs](https://discord.com)`) and file attachments (`[my report](media/q1.pdf)`) in the same reply — only the latter trigger an upload.

## Image vs doc syntax

| Intent | Syntax | Skill |
|---|---|---|
| Send an image (rendered inline if the channel supports it) | `![alt](path)` | `send-image` |
| Send any other file as an attachment | `[label](path)` | `send-docs` (this) |

If you write `![alt](path)` to a non-image file, the image dispatcher rejects it (magic-byte check) and nothing is delivered. Use `[label](path)` for anything that isn't a normal raster image.

If you write `[label](path)` pointing to an image file, it is uploaded as a generic attachment (filename preserved). Discord shows it as an inline image preview either way, so usually prefer `![alt](path)` for images.

## Code Blocks Are Skipped

Link syntax inside a fenced code block (```` ``` ```` or `~~~`) or inline backticks (`` ` ``) is **not** parsed as a doc link. The Markdown stays as literal text.

```
Use `[label](path)` to send a file.
```

The line above is shown to the user as plain text — no upload.

## Channel Support

| Channel | Doc Send |
|---|---|
| Discord | supported (≤25 MB without Nitro boost; oversized uploads fail and are logged) |
| WeCom | supported (≤20 MB; uploaded via aibot media flow as `msgtype=file`) |
| Telegram | not yet |
| Feishu | not yet |
| Web / CLI / Socket | not yet |

On unsupported channels the Markdown link remains in the message and renders normally (a link to a path the user can't reach). **No upload happens.** Don't reference doc syntax when the user is on a channel that does not support doc send.

## Important Rules

- The original Markdown text is delivered to the user **unchanged**. The file is an additional attachment, not a replacement. Don't add notes like "(file attached)" — the user will see both the text and the attachment.
- Use absolute paths or paths relative to `{{WORKSPACE}}`. Other relative roots will not resolve.
- The file must already exist and be a regular non-empty file. Hallucinated paths fail silently — the link is delivered as text but no upload happens.
- Do not URL-encode paths. Use the path as it appears on disk.
- Filename presented to the recipient is the basename of `path` — choose a meaningful filename when generating the file (e.g. `q1-revenue.pdf`, not `out.pdf`).

## When to Use This Skill

- Sending a generated PDF / docx / xlsx / csv / json / log / archive.
- Sharing a code file or build artefact the user asked to inspect.
- Delivering an HTML file you'd rather attach than upload via `create-html` (when the user explicitly wants the file, not a hosted URL).

## When NOT to Use This Skill

- The file is a normal image (PNG / JPEG / GIF / WebP) → use `send-image` with `![alt](path)`.
- The user wants a hosted, shareable URL for an HTML page → use `create-html` (writes file → uploads via `upload-html` → returns URL).
- The "file" is actually a remote URL → just include it as a Markdown link; nothing will be uploaded and the user can click through.
