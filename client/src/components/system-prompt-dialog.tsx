import { Check, Copy, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { fetchSystemPrompt, type SystemPromptInfo } from "@/lib/api";

// Live system prompt inspector, restoring the pre-React dashboard's
// "SYSTEM PROMPT (LIVE)" panel. Deliberately raw: the prompt is markdown, but
// rendering it would hide exactly what this view exists to show — literal
// section boundaries, stray whitespace, an unresolved {{PLACEHOLDER}}. Browser
// find works over the <pre> because the whole text stays in the DOM.
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
          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain rounded-md border p-3">
            <pre className="font-mono text-xs leading-relaxed break-words whitespace-pre-wrap">
              {info.prompt}
            </pre>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
