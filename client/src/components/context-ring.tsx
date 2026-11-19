import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { fetchSessionStats, type SessionStats } from "@/lib/api";
import { cn } from "@/lib/utils";

// How full this session's context is, sized the way the compression tiers size
// it (system prompt + session as sent + tool definitions — the server's
// thread.RequestTokens). The ring is the request against the effective window;
// its three ticks are where Tier 2 compression, Tier 3 compression and the
// last-resort trim begin.
//
// It refetches when the chat hook says session.jsonl may have changed (a
// persisted-entry frame or a turn end), debounced: a tool-heavy round persists
// several entries within a second, and one read after they land is enough. The
// server pass is cheap because per-message token counts are memoized.
const refetchDebounceMs = 1500;

function fmtTok(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function fmtPct(p: number): string {
  return p.toFixed(p > 0 && p < 10 ? 1 : 0) + "%";
}

const statusTone = {
  ok: "text-primary",
  warning: "text-amber-500",
  pressure: "text-destructive",
} as const;

const ringSize = 18;
const ringStroke = 2.5;
const ringRadius = (ringSize - ringStroke) / 2;
const ringCircumference = 2 * Math.PI * ringRadius;

function Ring({ stats, error }: { stats: SessionStats | null; error: string }) {
  const pct = stats ? Math.min(stats.usage_percent, 100) : 0;
  const ticks = stats
    ? [stats.tier2_trigger_percent, stats.tier3_trigger_percent, stats.trim_trigger_percent].filter(
        (p) => p > 0 && p < 100,
      )
    : [];
  const c = ringSize / 2;
  return (
    <svg
      width={ringSize}
      height={ringSize}
      viewBox={`0 0 ${ringSize} ${ringSize}`}
      className={cn(
        "-rotate-90",
        error
          ? "text-destructive"
          : stats
            ? statusTone[stats.pressure_status]
            : "text-muted-foreground",
      )}
      aria-hidden
    >
      <circle
        cx={c}
        cy={c}
        r={ringRadius}
        fill="none"
        strokeWidth={ringStroke}
        className="stroke-muted"
        strokeDasharray={error ? "2 2" : undefined}
      />
      {stats && pct > 0 && (
        <circle
          cx={c}
          cy={c}
          r={ringRadius}
          fill="none"
          stroke="currentColor"
          strokeWidth={ringStroke}
          strokeLinecap="round"
          strokeDasharray={`${(pct / 100) * ringCircumference} ${ringCircumference}`}
        />
      )}
      {ticks.map((p) => {
        const a = (p / 100) * 2 * Math.PI;
        const r0 = ringRadius - ringStroke / 2 - 0.25;
        const r1 = ringRadius + ringStroke / 2 + 0.25;
        return (
          <line
            key={p}
            x1={c + r0 * Math.cos(a)}
            y1={c + r0 * Math.sin(a)}
            x2={c + r1 * Math.cos(a)}
            y2={c + r1 * Math.sin(a)}
            className="stroke-foreground/70"
            strokeWidth={0.9}
          />
        );
      })}
    </svg>
  );
}

function Row({
  label,
  value,
  hint,
  swatch,
}: {
  label: string;
  value: string;
  hint?: string;
  swatch?: string;
}) {
  return (
    <div className="flex items-baseline gap-2 py-1 text-sm">
      {swatch && <span className={cn("size-2.5 shrink-0 self-center rounded-sm", swatch)} />}
      <span className="min-w-0 flex-1">
        {label}
        {hint && <span className="text-muted-foreground block text-xs">{hint}</span>}
      </span>
      <span className="shrink-0 font-mono tabular-nums">{value}</span>
    </div>
  );
}

function UsageDetails({ stats }: { stats: SessionStats }) {
  const { t } = useTranslation();
  const w = stats.context_window_tokens;
  const part = (n: number) => (w > 0 ? Math.min((n / w) * 100, 100) : 0);
  const markers = [
    { key: "tier2", pct: stats.tier2_trigger_percent, label: t("context.tier2") },
    { key: "tier3", pct: stats.tier3_trigger_percent, label: t("context.tier3") },
    { key: "trim", pct: stats.trim_trigger_percent, label: t("context.trim") },
  ].filter((m) => m.pct > 0);
  const tokensAt = (pct: number) => fmtTok(Math.round((pct / 100) * w));

  return (
    <div className="flex flex-col gap-4">
      <div>
        <div className="flex items-baseline gap-2">
          <span
            className={cn(
              "text-2xl font-semibold tabular-nums",
              statusTone[stats.pressure_status],
            )}
          >
            {fmtPct(stats.usage_percent)}
          </span>
          <span className="text-muted-foreground font-mono text-sm tabular-nums">
            {fmtTok(stats.used_tokens)} / {fmtTok(w)}
          </span>
        </div>
        <div className="relative mt-3 mb-5">
          <div className="bg-muted flex h-2.5 overflow-hidden rounded-full">
            <div className="bg-chart-3" style={{ width: `${part(stats.system_prompt_tokens)}%` }} />
            <div className="bg-chart-2" style={{ width: `${part(stats.message_tokens)}%` }} />
            <div className="bg-chart-1" style={{ width: `${part(stats.tool_defs_tokens)}%` }} />
          </div>
          {markers.map((m) => (
            <div
              key={m.key}
              className="absolute -top-1 h-4.5 w-px bg-foreground/70"
              style={{ left: `${m.pct}%` }}
              title={`${m.label} · ${fmtPct(m.pct)}`}
            />
          ))}
        </div>
      </div>

      <div className="divide-border/60 divide-y">
        <Row
          swatch="bg-chart-3"
          label={t("context.systemPrompt")}
          value={stats.is_runtime ? fmtTok(stats.system_prompt_tokens) : "—"}
        />
        <Row
          swatch="bg-chart-2"
          label={t("context.messages", { count: stats.message_count })}
          hint={
            stats.tokens_saved > 0
              ? t("context.saved", {
                  tokens: fmtTok(stats.tokens_saved),
                  count: stats.compressed_messages,
                })
              : undefined
          }
          value={fmtTok(stats.message_tokens)}
        />
        <Row
          swatch="bg-chart-1"
          label={t("context.toolDefs")}
          value={stats.is_runtime ? fmtTok(stats.tool_defs_tokens) : "—"}
        />
      </div>

      <div className="divide-border/60 divide-y">
        {markers.map((m) => (
          <Row
            key={m.key}
            label={m.label}
            hint={t(`context.${m.key}Hint`)}
            value={`${fmtPct(m.pct)} · ${tokensAt(m.pct)}`}
          />
        ))}
      </div>

      {!stats.is_runtime && (
        <p className="text-muted-foreground bg-muted/50 rounded-md px-3 py-2 text-xs">
          {t("context.partial")}
        </p>
      )}
    </div>
  );
}

export function ContextRing({
  sessionKey,
  refreshTick,
}: {
  sessionKey: string;
  // Changes whenever the session file may have grown; any change refetches.
  refreshTick: number;
}) {
  const { t } = useTranslation();
  const [stats, setStats] = useState<SessionStats | null>(null);
  const [error, setError] = useState("");
  const [open, setOpen] = useState(false);
  const seq = useRef(0);
  const readKey = useRef<string | null>(null);

  // A switch of session drops the old numbers at once; they belong to a
  // different conversation.
  useEffect(() => {
    setStats(null);
    setError("");
  }, [sessionKey]);

  useEffect(() => {
    const load = () => {
      const mine = ++seq.current;
      fetchSessionStats(sessionKey)
        .then((s) => {
          if (mine !== seq.current) return;
          setStats(s);
          setError("");
        })
        .catch((e: unknown) => {
          if (mine !== seq.current) return;
          setError(e instanceof Error ? e.message : String(e));
        });
    };
    // First read for a session is immediate; later ones wait for the burst of
    // persisted entries to settle.
    const first = readKey.current !== sessionKey;
    const id = setTimeout(() => {
      readKey.current = sessionKey;
      load();
    }, first ? 0 : refetchDebounceMs);
    return () => clearTimeout(id);
  }, [sessionKey, refreshTick]);

  // Opening the details is a reason to want fresh numbers.
  useEffect(() => {
    if (!open) return;
    const mine = ++seq.current;
    fetchSessionStats(sessionKey)
      .then((s) => {
        if (mine === seq.current) setStats(s);
      })
      .catch((e: unknown) => {
        if (mine === seq.current) setError(e instanceof Error ? e.message : String(e));
      });
  }, [open, sessionKey]);

  const summary = error
    ? t("context.failed", { error })
    : stats
      ? t("context.summary", {
          pct: fmtPct(stats.usage_percent),
          used: fmtTok(stats.used_tokens),
          window: fmtTok(stats.context_window_tokens),
        })
      : t("common.loading");

  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="sm"
            className="text-muted-foreground gap-1.5 px-1.5 font-normal"
            onClick={() => setOpen(true)}
            aria-label={summary}
          >
            <Ring stats={stats} error={error} />
            {stats && !error && (
              <span className="text-xs tabular-nums max-md:hidden">
                {fmtPct(stats.usage_percent)}
              </span>
            )}
          </Button>
        </TooltipTrigger>
        <TooltipContent side="bottom">{summary}</TooltipContent>
      </Tooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("context.title")}</DialogTitle>
            <DialogDescription className="font-mono text-xs">{sessionKey}</DialogDescription>
          </DialogHeader>
          {error && <p className="text-destructive text-xs">{t("context.failed", { error })}</p>}
          {stats ? (
            <UsageDetails stats={stats} />
          ) : (
            !error && <p className="text-muted-foreground text-sm">{t("common.loading")}</p>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
