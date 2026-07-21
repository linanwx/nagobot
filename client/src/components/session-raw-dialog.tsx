import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  fetchSession,
  type ApiMessage,
  type SessionDetail,
} from "@/lib/api";
import { cn } from "@/lib/utils";

// Raw session.jsonl inspector, styled after the pre-React dashboard: one card
// per message with a role-colored edge, every persisted field surfaced —
// frontmatter as a key/value grid, reasoning and tool calls collapsible,
// compressed messages toggling between what the bot sees and the original.

const roleEdge: Record<string, string> = {
  user: "border-s-sky-400",
  assistant: "border-s-emerald-400",
  system: "border-s-amber-400",
  tool: "border-s-violet-400",
};

const roleBadge: Record<string, string> = {
  user: "bg-sky-400 text-sky-950",
  assistant: "bg-emerald-400 text-emerald-950",
  system: "bg-amber-400 text-amber-950",
  tool: "bg-violet-400 text-violet-950",
};

function fmtTok(n: number): string {
  return n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n);
}

function fmtTime(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  return d.toLocaleString();
}

// Wake payloads are YAML frontmatter + body. One level of nesting is enough:
// the dashboard did the same, and deeper nesting falls back to plain text.
function splitFrontmatter(
  content: string,
): { fields: [string, string][]; body: string } | null {
  const m = /^---\n([\s\S]*?)\n---\n?/.exec(content);
  if (!m) return null;
  const fields: [string, string][] = [];
  for (const line of m[1].split("\n")) {
    const idx = line.indexOf(":");
    if (idx > 0) {
      fields.push([line.slice(0, idx).trim(), line.slice(idx + 1).trim()]);
    }
  }
  if (fields.length === 0) return null;
  return { fields, body: content.slice(m[0].length) };
}

function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function Chip({
  className,
  children,
  title,
  onClick,
}: {
  className?: string;
  children: React.ReactNode;
  title?: string;
  onClick?: () => void;
}) {
  const Tag = onClick ? "button" : "span";
  return (
    <Tag
      type={onClick ? "button" : undefined}
      onClick={onClick}
      title={title}
      className={cn(
        "rounded px-1.5 py-px font-mono text-[10px] leading-4",
        onClick && "cursor-pointer",
        className,
      )}
    >
      {children}
    </Tag>
  );
}

// Long bodies clamp to a preview height; the full text stays in the DOM and a
// click reveals it (the dashboard's msg-body-truncated behavior).
const clampChars = 1200;

function RawText({ text, className }: { text: string; className?: string }) {
  const [expanded, setExpanded] = useState(false);
  const long = text.length > clampChars;
  return (
    <div className={className}>
      <pre
        className={cn(
          "font-mono text-xs leading-relaxed break-words whitespace-pre-wrap",
          long && !expanded && "max-h-48 overflow-hidden",
        )}
      >
        {text}
      </pre>
      {long && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="text-muted-foreground hover:text-foreground mt-1 text-[10px] underline underline-offset-2"
        >
          {expanded ? "collapse" : `expand (${text.length} chars)`}
        </button>
      )}
    </div>
  );
}

function Collapsible({
  summary,
  children,
}: {
  summary: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <details className="border-border/50 border-t">
      <summary className="text-muted-foreground hover:text-foreground cursor-pointer px-3 py-1.5 text-[11px] select-none">
        {summary}
      </summary>
      <div className="px-3 pb-2">{children}</div>
    </details>
  );
}

function BodyView({ text }: { text: string }) {
  const fm = splitFrontmatter(text);
  if (!fm) return <RawText text={text} className="px-3 py-2" />;
  // Nested frontmatter: system-sender wakes wrap another frontmatter block.
  const nested = splitFrontmatter(fm.body);
  return (
    <div className="px-3 py-2">
      <FieldGrid fields={fm.fields} />
      {nested ? (
        <>
          <FieldGrid fields={nested.fields} className="mt-2" />
          {nested.body.trim() !== "" && (
            <RawText text={nested.body} className="mt-2" />
          )}
        </>
      ) : (
        fm.body.trim() !== "" && <RawText text={fm.body} className="mt-2" />
      )}
    </div>
  );
}

function FieldGrid({
  fields,
  className,
}: {
  fields: [string, string][];
  className?: string;
}) {
  return (
    <div
      className={cn(
        "grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-mono text-[11px]",
        className,
      )}
    >
      {fields.map(([k, v], i) => (
        // Duplicate keys are possible in merged wake payloads; index the key.
        <div key={`${k}-${i}`} className="contents">
          <span className="text-muted-foreground">{k}</span>
          <span className="break-words">{v}</span>
        </div>
      ))}
    </div>
  );
}

function MessageCard({ msg, index }: { msg: ApiMessage; index: number }) {
  // Compressed messages default to the compressed text — what the bot
  // actually sees in context — with a toggle back to the original.
  const [showOriginal, setShowOriginal] = useState(false);
  const role = msg.role || "system";
  const displayText = msg.compressed && !showOriginal
    ? msg.compressed
    : (msg.content ?? "");

  return (
    <div
      className={cn(
        "border-border overflow-hidden rounded-md border border-s-4",
        roleEdge[role] ?? "border-s-border",
        msg.heartbeat_trim && "opacity-60",
      )}
    >
      <div className="flex flex-wrap items-center gap-1.5 px-3 pt-2 pb-1">
        <span className="text-muted-foreground/60 font-mono text-[10px]">
          #{index}
        </span>
        <Chip className={cn("font-semibold", roleBadge[role] ?? "bg-muted")}>
          {role}
        </Chip>
        {msg.timestamp && (
          <span
            className="text-muted-foreground text-[10px]"
            title={msg.timestamp}
          >
            {fmtTime(msg.timestamp)}
          </span>
        )}
        {msg.source && <Chip className="bg-muted">{msg.source}</Chip>}
        {msg.name && (
          <Chip className="bg-violet-500/15 text-violet-700 dark:text-violet-300">
            {msg.name}
          </Chip>
        )}
        {msg.compressed && (
          <Chip
            onClick={() => setShowOriginal((v) => !v)}
            title="Toggle original/compressed view"
            className="bg-violet-500/20 text-violet-700 dark:text-violet-300"
          >
            {showOriginal ? "showing original" : "showing compressed"}
          </Chip>
        )}
        {msg.heartbeat_trim && (
          <Chip className="bg-amber-500/20 text-amber-700 dark:text-amber-300">
            heartbeat-trim
          </Chip>
        )}
        {msg.skip_trim && <Chip className="bg-muted">skip-trim</Chip>}
        {msg.id && (
          <span
            className="text-muted-foreground/50 max-w-40 truncate font-mono text-[10px]"
            title={msg.id}
          >
            {msg.id}
          </span>
        )}
        {(msg.tokens ?? 0) > 0 && (
          <Chip className="bg-muted ms-auto" title="context tokens / original tokens">
            {msg.compressed_tokens && msg.compressed_tokens !== msg.tokens
              ? `${fmtTok(msg.compressed_tokens)}/${fmtTok(msg.tokens ?? 0)} tok`
              : `${fmtTok(msg.tokens ?? 0)} tok`}
          </Chip>
        )}
      </div>

      {displayText !== "" && <BodyView text={displayText} />}

      {msg.media && msg.media.length > 0 && (
        <div className="px-3 pb-2">
          {msg.media.map((m) => (
            <div key={m} className="text-muted-foreground font-mono text-[10px] break-all">
              {m}
            </div>
          ))}
        </div>
      )}

      {msg.reasoning_content && (
        <Collapsible
          summary={
            <>
              Reasoning
              {(msg.reasoning_tokens ?? 0) > 0 &&
                ` (${fmtTok(msg.reasoning_tokens ?? 0)} tok)`}
              {msg.reasoning_trimmed && (
                <Chip className="bg-amber-500/20 text-amber-700 dark:text-amber-300 ms-1.5">
                  trimmed
                </Chip>
              )}
            </>
          }
        >
          <RawText text={msg.reasoning_content} />
        </Collapsible>
      )}

      {msg.reasoning_details != null && (
        <Collapsible summary="Reasoning details">
          <RawText text={JSON.stringify(msg.reasoning_details, null, 2)} />
        </Collapsible>
      )}

      {msg.tool_calls && msg.tool_calls.length > 0 && (
        <Collapsible
          summary={`Tool calls: ${msg.tool_calls
            .map((tc) => tc.function?.name || "unknown")
            .join(", ")}`}
        >
          <div className="flex flex-col gap-2">
            {msg.tool_calls.map((tc, i) => (
              <div key={tc.id ?? i}>
                <div className="font-mono text-[11px] font-semibold">
                  {tc.function?.name || "unknown"}
                  {tc.id && (
                    <span className="text-muted-foreground/60 ms-2 font-normal">
                      {tc.id}
                    </span>
                  )}
                </div>
                {tc.function?.arguments && (
                  <RawText text={prettyJSON(tc.function.arguments)} />
                )}
              </div>
            ))}
          </div>
        </Collapsible>
      )}

      {role === "tool" && msg.tool_call_id && (
        <div className="border-border/50 text-muted-foreground/60 border-t px-3 py-1 font-mono text-[10px] break-all">
          call_id: {msg.tool_call_id}
        </div>
      )}
    </div>
  );
}

export function SessionRawDialog({
  sessionKey,
  open,
  onOpenChange,
}: {
  sessionKey: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [detail, setDetail] = useState<SessionDetail | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setDetail(null);
    setError(null);
    fetchSession(sessionKey)
      .then((d) => {
        if (!cancelled) setDetail(d);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [open, sessionKey]);

  const totalTokens =
    detail?.messages.reduce(
      (sum, m) => sum + (m.compressed_tokens ?? m.tokens ?? 0),
      0,
    ) ?? 0;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85dvh] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Raw session data</DialogTitle>
          <DialogDescription className="font-mono text-xs">
            {sessionKey} · session.jsonl
            {detail &&
              ` · ${detail.messages.length} messages · ~${fmtTok(totalTokens)} tok in context`}
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <div className="text-destructive flex items-center gap-3 text-sm">
            <span>{error}</span>
            <Button size="sm" variant="outline" onClick={() => onOpenChange(false)}>
              Close
            </Button>
          </div>
        ) : detail == null ? (
          <p className="text-muted-foreground text-sm">Loading…</p>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overscroll-contain pe-1">
            {detail.messages.map((m, i) => (
              <MessageCard key={m.id || i} msg={m} index={i} />
            ))}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
