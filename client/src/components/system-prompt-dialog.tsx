import {
  Check,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  Copy,
  RefreshCw,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
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
import { fetchSystemPrompt, type SystemPromptInfo } from "@/lib/api";
import { splitPromptSections } from "@/lib/prompt-sections";

// Live system prompt inspector, restoring the pre-React dashboard's
// "SYSTEM PROMPT (LIVE)" panel. Deliberately raw: the prompt is markdown, but
// rendering it would hide exactly what this view exists to show — literal
// section boundaries, stray whitespace, an unresolved {{PLACEHOLDER}}.
//
// It is split into collapsed sections (lib/prompt-sections.ts) because the
// whole thing runs to 60K+ chars: one <pre> was a wall nobody could navigate,
// least of all on a phone. Each section still shows its exact raw text.
// Collapsed sections are not in the DOM, so browser find only searches what
// is open; "Expand all" restores the old whole-text search, and Copy always
// copies the complete prompt.
//
// The value is a *rebuild off current thread state*, not a recording of what
// any past turn actually sent: {{DATE}}, USER.md, the skill list and the
// memory index all resolve at build time. It is the approximation the old UI
// showed too — right for "why is the bot behaving like this", wrong for
// "what exactly went out at 14:03". The header says so.

function fmtTok(n: number): string {
  return n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n);
}

export function SystemPromptDialog({
  sessionKey,
  open,
  onOpenChange,
}: {
  sessionKey: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [info, setInfo] = useState<SystemPromptInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  // Bumped by the refresh button; the prompt is rebuilt server-side on every
  // request, so a refetch is the only way to see state that changed since the
  // dialog opened (a skill reload, a USER.md rewrite, a date rollover).
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetchSystemPrompt(sessionKey)
      .then((d) => {
        if (!cancelled) setInfo(d);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, sessionKey, reloadKey]);

  // Drop the previous session's prompt when the dialog is reopened elsewhere,
  // so a stale body can never be shown under a new key's header.
  useEffect(() => {
    if (!open) setInfo(null);
  }, [open]);

  const copy = useCallback(() => {
    const text = info?.prompt;
    if (!text || typeof navigator === "undefined" || !navigator.clipboard) return;
    navigator.clipboard.writeText(text).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      },
      () => {},
    );
  }, [info]);

  const hasPrompt = info != null && info.available && info.prompt !== "";

  const sections = useMemo(
    () => (hasPrompt ? splitPromptSections(info.prompt) : []),
    [hasPrompt, info],
  );
  // Open state is per section index; a refetch can change the section list,
  // so it resets with the prompt rather than pointing at the wrong section.
  const [openSet, setOpenSet] = useState<Set<number>>(new Set());
  useEffect(() => setOpenSet(new Set()), [sections]);
  const allOpen = sections.length > 0 && openSet.size === sections.length;
  const toggleAll = () =>
    setOpenSet(allOpen ? new Set() : new Set(sections.map((_, i) => i)));
  const setSectionOpen = (i: number, o: boolean) =>
    setOpenSet((prev) => {
      const next = new Set(prev);
      if (o) next.add(i);
      else next.delete(i);
      return next;
    });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85dvh] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("chat.systemPrompt")}</DialogTitle>
          <DialogDescription className="font-mono text-xs">
            {sessionKey}
            {hasPrompt &&
              ` · ${info.prompt.length} chars · ~${fmtTok(info.tokens)} tok`}
          </DialogDescription>
        </DialogHeader>

        <div className="text-muted-foreground flex shrink-0 items-center gap-2 text-xs">
          <span className="min-w-0 flex-1">{t("chat.systemPromptLive")}</span>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 shrink-0 px-2"
            disabled={loading}
            onClick={() => setReloadKey((v) => v + 1)}
          >
            <RefreshCw className="size-3.5" />
            {t("common.refresh")}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 shrink-0 px-2"
            disabled={!hasPrompt}
            onClick={toggleAll}
            aria-label={allOpen ? t("chat.collapseAll") : t("chat.expandAll")}
            title={allOpen ? t("chat.collapseAll") : t("chat.expandAll")}
          >
            {allOpen ? (
              <ChevronsDownUp className="size-3.5" />
            ) : (
              <ChevronsUpDown className="size-3.5" />
            )}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="h-7 shrink-0 px-2"
            disabled={!hasPrompt}
            onClick={copy}
          >
            {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            {copied ? t("common.copied") : t("common.copy")}
          </Button>
        </div>

        {error ? (
          <p className="text-destructive text-sm">{error}</p>
        ) : info == null ? (
          <p className="text-muted-foreground text-sm">{t("common.loading")}</p>
        ) : !info.available ? (
          // Threads are GC'd after 3h idle, and the prompt is built off live
          // thread state — there is nothing on disk to fall back to. Say why
          // rather than showing a blank panel.
          <p className="text-muted-foreground rounded-md border border-dashed p-4 text-sm">
            {t("chat.systemPromptUnavailable")}
          </p>
        ) : (
          <div className="min-h-0 flex-1 divide-y overflow-y-auto overscroll-contain rounded-md border">
            {sections.map((sec, i) => (
              <Collapsible
                key={i}
                open={openSet.has(i)}
                onOpenChange={(o) => setSectionOpen(i, o)}
              >
                <CollapsibleTrigger className="hover:bg-muted/50 group flex w-full items-center gap-2 px-3 py-2 text-left text-sm">
                  <ChevronRight className="text-muted-foreground size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
                  <span className="min-w-0 flex-1 truncate font-medium">
                    {sec.title || "—"}
                  </span>
                  {sec.blockType && sec.blockType !== sec.title && (
                    <span className="text-muted-foreground hidden shrink-0 font-mono text-[11px] sm:inline">
                      {sec.blockType}
                    </span>
                  )}
                  <span className="text-muted-foreground shrink-0 font-mono text-[11px] tabular-nums">
                    {fmtTok(sec.text.length)}
                  </span>
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <pre className="bg-muted/30 border-t px-3 py-2 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap">
                    {sec.text}
                  </pre>
                </CollapsibleContent>
              </Collapsible>
            ))}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
