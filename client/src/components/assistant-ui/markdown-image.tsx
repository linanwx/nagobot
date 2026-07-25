import { useState, type ComponentPropsWithoutRef } from "react";
import { useTranslation } from "react-i18next";
import { ImageOff } from "lucide-react";

import { cn } from "@/lib/utils";

// Schemes that already address something the browser can fetch on its own.
// Everything else is a filesystem path the model wrote, which only the server
// can resolve.
const PASSTHROUGH = /^(https?:|data:|blob:)/i;

/**
 * mediaSrc maps what the model wrote in `![alt](…)` onto a URL.
 *
 * It deliberately does NO path interpretation. The send-image skill permits an
 * absolute path or one relative to the workspace, and only the server knows
 * where the workspace is — so the raw string goes to /api/media?path=… and the
 * server does the single authoritative resolve-and-contain.
 *
 * The tempting alternative — find the "/media/" segment here and take the tail
 * — fails open: "/home/me/photos/media/cover.jpg", a path outside the workspace
 * entirely, would map onto {workspace}/media/cover.jpg and silently render an
 * unrelated image. A wrong guess must surface as an error, not as the wrong
 * picture.
 */
export function mediaSrc(raw: string): string {
  const src = raw.trim();
  if (!src) return "";
  if (PASSTHROUGH.test(src)) return src;
  if (src.startsWith("/api/media")) return src;
  return `/api/media?path=${encodeURIComponent(src)}`;
}

type Props = ComponentPropsWithoutRef<"img">;

/**
 * MarkdownImage renders an inline `![alt](path)` from a reply.
 *
 * Inline is the point: the send-image skill lets the reference sit anywhere,
 * including mid-paragraph, and web is the only channel that can honour that —
 * Discord and WeCom upload a native attachment, which always lands at the end
 * and loses the position the model chose.
 *
 * On failure it shows the alt text and a reason rather than the browser's
 * broken-image glyph. The common failure is a convention violation (a file
 * outside {workspace}/media, which the server refuses with 403), and that is
 * worth saying out loud — a silent broken glyph reads as "the bot is buggy"
 * when the actual fact is "that file is not in a place the page can serve".
 */
export function MarkdownImage({ src, alt, className, ...rest }: Props) {
  const { t } = useTranslation();
  const [failed, setFailed] = useState(false);
  const resolved = typeof src === "string" ? mediaSrc(src) : "";

  if (!resolved || failed) {
    return (
      <span className="border-border/60 text-muted-foreground my-1 inline-flex max-w-full items-center gap-1.5 rounded-lg border border-dashed px-2 py-1 text-xs">
        <ImageOff className="size-3.5 shrink-0" />
        <span className="truncate">{alt || t("markdown.imageFailed")}</span>
      </span>
    );
  }

  return (
    <img
      src={resolved}
      alt={alt ?? ""}
      loading="lazy"
      onError={() => setFailed(true)}
      className={cn("my-2 h-auto max-w-full rounded-lg", className)}
      {...rest}
    />
  );
}
