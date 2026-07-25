import { ChevronDownIcon, PinIcon, Trash2Icon, XIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { MarkdownImage } from "@/components/assistant-ui/markdown-image";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  deletePin,
  fetchPin,
  fetchPins,
  type PinEntry,
} from "@/lib/api";
import { relativeTime } from "@/lib/sessions";
import { cn } from "@/lib/utils";

// The session's pins: one row per markdown file in {sessionDir}/pins, each
// expanding in place to its rendered content.
//
// The list refreshes on a timer while the panel is open. That is not polish —
// pinning is asynchronous by design (the button queues an agentic turn that
// writes the file afterwards), so a user who pins and immediately opens this
// panel would otherwise be looking at a stale empty list with no way to know
// more is coming.
const refreshIntervalMs = 5000;

// Markdown for pin bodies. The registry MarkdownText renders through the
// assistant message part context, which a file read off disk does not have, so
// this surface gets its own map — the same reason event cards and user bubbles
// have theirs. Sized for a reading panel rather than a chat bubble.
const pinMarkdownComponents: Components = {
  h1: (p) => <h3 className="mt-3 mb-1.5 font-semibold first:mt-0" {...p} />,
  h2: (p) => <h3 className="mt-3 mb-1.5 font-semibold first:mt-0" {...p} />,
  h3: (p) => <h4 className="mt-2.5 mb-1 font-semibold first:mt-0" {...p} />,
  h4: (p) => <h4 className="mt-2.5 mb-1 font-medium first:mt-0" {...p} />,
  p: (p) => <p className="mb-2 leading-relaxed last:mb-0" {...p} />,
  ul: (p) => <ul className="mb-2 list-disc ps-5 last:mb-0" {...p} />,
  ol: (p) => <ol className="mb-2 list-decimal ps-5 last:mb-0" {...p} />,
  li: (p) => <li className="mb-0.5 last:mb-0" {...p} />,
  blockquote: (p) => (
    <blockquote
      className="border-border/60 text-muted-foreground mb-2 border-s-2 ps-3 last:mb-0"
      {...p}
    />
  ),
  a: (p) => (
    <a
      className="text-primary underline underline-offset-2"
      target="_blank"
      rel="noreferrer"
      {...p}
    />
  ),
  code: (p) => (
    <code className="bg-muted rounded px-1 py-px font-mono text-[0.9em]" {...p} />
  ),
  pre: (p) => (
    <pre
      className="bg-muted mb-2 overflow-x-auto rounded-md p-2.5 text-[0.85em] last:mb-0 [&_code]:bg-transparent [&_code]:p-0"
      {...p}
    />
  ),
  hr: () => <hr className="border-border/50 my-3" />,
  table: (p) => (
    <div className="mb-2 overflow-x-auto last:mb-0">
      <table className="text-[0.9em]" {...p} />
    </div>
  ),
  th: (p) => (
    <th className="border-border/50 border px-2 py-1 text-start" {...p} />
  ),
  td: (p) => <td className="border-border/50 border px-2 py-1" {...p} />,
  img: (p) => <MarkdownImage {...p} />,
};

function PinRow({
  sessionKey,
  pin,
  onDeleted,
}: {
  sessionKey: string;
  pin: PinEntry;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);

  // Content is fetched on first expand and kept: a pin is a file that only
  // changes when the agent merges into it, so re-fetching on every toggle would
  // buy nothing. The list poll is what surfaces a merge, via `modified`.
  useEffect(() => {
    if (!open || content !== null) return;
    let cancelled = false;
    fetchPin(sessionKey, pin.name)
      .then((d) => {
        if (!cancelled) setContent(d.content);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [open, content, sessionKey, pin.name]);

  // A merge rewrites the file in place; the poll notices via `modified` and the
  // cached body has to go with it, or an expanded row shows the pre-merge text
  // forever.
  useEffect(() => {
    setContent(null);
    setError("");
  }, [pin.modified]);

  const onDelete = async () => {
    setDeleting(true);
    try {
      await deletePin(sessionKey, pin.name);
      setConfirming(false);
      onDeleted();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
      setConfirming(false);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="border-border shrink-0 overflow-hidden rounded-md border">
      <Collapsible open={open} onOpenChange={setOpen}>
        <div className="flex items-start gap-1 px-2 py-1.5">
          <CollapsibleTrigger asChild>
            <button
              type="button"
              className="hover:bg-muted/50 -my-0.5 flex min-w-0 flex-1 items-start gap-1.5 rounded px-1 py-0.5 text-start"
            >
              <ChevronDownIcon
                className={cn(
                  "text-muted-foreground mt-0.5 size-3.5 shrink-0 transition-transform",
                  !open && "-rotate-90",
                )}
              />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">
                  {pin.title}
                </span>
                {pin.summary && (
                  <span className="text-muted-foreground mt-0.5 block text-xs">
                    {pin.summary}
                  </span>
                )}
              </span>
            </button>
          </CollapsibleTrigger>
          <span
            className="text-muted-foreground shrink-0 pt-1 text-[10px]"
            title={pin.name}
          >
            {relativeTime(pin.modified)}
          </span>
          {open && (
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => setOpen(false)}
              title={t("pins.close")}
            >
              <XIcon />
              <span className="sr-only">{t("pins.close")}</span>
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-muted-foreground hover:text-destructive"
            onClick={() => setConfirming(true)}
            title={t("pins.delete")}
          >
            <Trash2Icon />
            <span className="sr-only">{t("pins.delete")}</span>
          </Button>
        </div>
        <CollapsibleContent>
          <div className="border-border/60 border-t px-3 py-2 text-sm">
            {error ? (
              <p className="text-destructive text-xs">{error}</p>
            ) : content === null ? (
              <p className="text-muted-foreground text-xs">
                {t("common.loading")}
              </p>
            ) : (
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                components={pinMarkdownComponents}
              >
                {stripFrontmatter(content)}
              </ReactMarkdown>
            )}
          </div>
        </CollapsibleContent>
      </Collapsible>

      <AlertDialog open={confirming} onOpenChange={setConfirming}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("pins.deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("pins.deleteBody", { title: pin.title })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>
              {t("common.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={deleting}
              onClick={(e) => {
                // Radix closes the dialog on Action by default; the delete is
                // async and can fail, so the close is driven by the result.
                e.preventDefault();
                void onDelete();
              }}
            >
              {t("pins.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

// The frontmatter is metadata, and the row header already shows both fields it
// carries. Rendering it too would put a stray "--- title: … ---" block above
// every pin.
function stripFrontmatter(content: string): string {
  const m = /^---\r?\n[\s\S]*?\r?\n---\r?\n?/.exec(content);
  return m ? content.slice(m[0].length) : content;
}

export function PinsDialog({
  sessionKey,
  open,
  onOpenChange,
}: {
  sessionKey: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [pins, setPins] = useState<PinEntry[] | null>(null);
  const [error, setError] = useState("");
  // Kept in a ref so the polling effect doesn't restart on every load.
  const alive = useRef(true);

  const load = useCallback(async () => {
    try {
      const list = await fetchPins(sessionKey);
      if (alive.current) {
        setPins(list);
        setError("");
      }
    } catch (e: unknown) {
      if (alive.current) setError(e instanceof Error ? e.message : String(e));
    }
  }, [sessionKey]);

  useEffect(() => {
    if (!open) return;
    alive.current = true;
    setPins(null);
    setError("");
    void load();
    const id = setInterval(() => void load(), refreshIntervalMs);
    return () => {
      alive.current = false;
      clearInterval(id);
    };
  }, [open, load]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85dvh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <PinIcon className="size-4" />
            {t("pins.title")}
          </DialogTitle>
          <DialogDescription className="font-mono text-xs">
            {sessionKey} · pins/
            {pins && ` · ${t("pins.count", { count: pins.length })}`}
          </DialogDescription>
        </DialogHeader>
        {error && <p className="text-destructive text-xs">{error}</p>}
        {pins === null ? (
          <p className="text-muted-foreground text-sm">{t("common.loading")}</p>
        ) : pins.length === 0 ? (
          <p className="text-muted-foreground text-sm">{t("pins.empty")}</p>
        ) : (
          <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overscroll-contain pe-1">
            {pins.map((p) => (
              <PinRow
                key={p.name}
                sessionKey={sessionKey}
                pin={p}
                onDeleted={() => void load()}
              />
            ))}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
