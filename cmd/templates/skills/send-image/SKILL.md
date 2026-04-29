---
name: send-image
description: Send images to the user by writing standard Markdown image syntax in your reply. Currently supported on Discord; falls back to plain text on other channels.
---
# Send Image

Send images to the user by writing standard Markdown image syntax inline in your reply text. The platform parses your reply, finds the image references, and uploads them to the channel as native attachments — without modifying the text you wrote.

## Syntax

Standard Markdown:

```
![alt text](path)
```

- `alt text` — short description (may be empty: `![](path)`)
- `path` — either an absolute filesystem path, or a path relative to your workspace root (e.g. `media/photo.jpg`)

The image markdown can appear **anywhere** in your reply, including mid-paragraph. It does not need its own line.

## Examples

Inline in a sentence:

```
Here is the result ![chart](media/result.png) — note the spike on day 3.
```

On its own line:

```
Sure, here is the screenshot.

![screenshot](/Users/me/.nagobot/workspace/media/img-20260101-123456.png)
```

Multiple images:

```
Comparing the two: ![before](media/before.jpg) vs ![after](media/after.jpg).
```

## Code Blocks Are Skipped

Image syntax inside a fenced code block (```` ``` ```` or `~~~`) or inline backticks (`` ` ``) is **not** sent as an image. The markdown stays as literal text. This lets you talk *about* image syntax without triggering an upload.

```
Use `![alt](path)` to send an image.
```

The line above is shown to the user as plain text — no upload.

## Channel Support

| Channel | Image Send |
|---|---|
| Discord | supported |
| WeCom | not yet |
| Telegram | not yet |
| Feishu | not yet |
| Web / CLI / Socket | not yet |

On unsupported channels the markdown remains in the message and is rendered (or left as text) by the channel's normal Markdown handling. **No upload happens.** Don't reference image markdown when the user is on a channel that does not support image send.

## Important Rules

- The original Markdown text is delivered to the user **unchanged**. The image is an additional attachment, not a replacement. Don't add notes like "(image attached)" — the user will see both the text and the image.
- Use absolute paths or paths relative to `{{WORKSPACE}}`. Other relative roots will not resolve.
- The file must already exist and contain real image bytes (PNG / JPEG / GIF / WebP / etc.). Hallucinated paths fail silently — the markdown is delivered as text but no image is uploaded.
- Do not URL-encode paths. Use the path as it appears on disk.
