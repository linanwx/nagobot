import { ArrowUpRight, ChevronDownIcon, Clock } from "lucide-react";
import { useEffect, useState } from "react";
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
import {
  fetchSessionCron,
  type SessionCron,
  type SessionCronJob,
} from "@/lib/api";
import { cn } from "@/lib/utils";

// The cron jobs tied to this session. A job has no owner field, so the server
// derives the tie from where the job runs (inject mode: the target session;
// independent mode: its own cron:<id>) and where it reports (independent mode's
// wake_session). Read-only: jobs are made and removed by the bot through
// `nagobot cron`, and this panel is how a person sees what is scheduled here.

function Tag({ tone, children }: { tone: "on" | "off"; children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded px-1.5 py-px text-[10px] font-medium",
        tone === "on" && "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
        tone === "off" && "bg-muted text-muted-foreground",
      )}
    >
      {children}
    </span>
  );
}

// "in 5m" / "in 3h" / "in 2d" — the mirror of relativeTime for future instants.
function untilTime(iso: string): string {
  const mins = Math.round((new Date(iso).getTime() - Date.now()) / 60_000);
  if (Number.isNaN(mins)) return "";
  if (mins < 1) return "now";
  if (mins < 60) return `in ${mins}m`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return `in ${hours}h`;
  return `in ${Math.round(hours / 24)}d`;
}

const absoluteFormat = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  weekday: "short",
  hour: "2-digit",
  minute: "2-digit",
});

function formatAbsolute(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : absoluteFormat.format(d);
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-2 text-xs">
      <span className="text-muted-foreground w-20 shrink-0">{label}</span>
      <span className="min-w-0 flex-1 break-all">{children}</span>
    </div>
  );
}

function SessionLink({
  sessionKey,
  currentKey,
  onOpenSession,
}: {
  sessionKey: string;
  currentKey: string;
  onOpenSession: (key: string) => void;
}) {
  const { t } = useTranslation();
  if (sessionKey === currentKey) {
    return <span className="font-mono">{sessionKey}</span>;
  }
  return (
    <Button
      variant="link"
      size="xs"
      className="h-auto p-0 font-mono text-xs max-md:h-auto"
      onClick={() => onOpenSession(sessionKey)}
      title={t("cron.open")}
    >
      {sessionKey}
      <ArrowUpRight className="size-3" />
    </Button>
  );
}

function CronRow({
  job,
  sessionKey,
  onOpenSession,
}: {
  job: SessionCronJob;
  sessionKey: string;
  onOpenSession: (key: string) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const firstLine = job.task.split("\n", 1)[0];

  return (
    <div className="border-border shrink-0 overflow-hidden rounded-md border">
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            className="hover:bg-muted/50 dark:hover:bg-muted/50 h-auto w-full items-start justify-start gap-1.5 rounded-none px-2 py-1.5 text-start font-normal whitespace-normal max-md:h-auto max-md:min-h-10"
          >
            <ChevronDownIcon
              className={cn(
                "text-muted-foreground mt-0.5 size-3.5 shrink-0 transition-transform",
                !open && "-rotate-90",
              )}
            />
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="truncate font-mono text-sm font-medium">{job.id}</span>
                <Tag tone="off">{t(job.mode === "inject" ? "cron.inject" : "cron.independent")}</Tag>
                {job.kind === "at" && <Tag tone="off">{t("cron.oneTime")}</Tag>}
                {job.relation === "reports_here" && <Tag tone="on">{t("cron.reportsHere")}</Tag>}
                {job.builtin && <Tag tone="off">{t("cron.builtin")}</Tag>}
              </span>
              <span className="text-muted-foreground mt-0.5 block truncate text-xs">
                {firstLine}
              </span>
            </span>
            <span className="text-muted-foreground shrink-0 pt-0.5 text-end text-[11px]">
              {job.kind === "cron" && (
                <span className="block font-mono">{job.schedule}</span>
              )}
              {job.next_run ? (
                <span className="block" title={job.next_run}>
                  {t("cron.next", {
                    when: `${formatAbsolute(job.next_run)} · ${untilTime(job.next_run)}`,
                  })}
                </span>
              ) : (
                <span className="block">{t("cron.noNext")}</span>
              )}
            </span>
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <div className="border-border/60 flex flex-col gap-1.5 border-t px-3 py-2">
            <Field label={t("cron.task")}>
              <span className="whitespace-pre-wrap break-words">{job.task}</span>
            </Field>
            <Field label={t("cron.runsIn")}>
              <SessionLink
                sessionKey={job.run_session}
                currentKey={sessionKey}
                onOpenSession={onOpenSession}
              />
            </Field>
            {job.mode === "independent" && job.wake_session && (
              <Field label={t("cron.reportsTo")}>
                <SessionLink
                  sessionKey={job.wake_session}
                  currentKey={sessionKey}
                  onOpenSession={onOpenSession}
                />
              </Field>
            )}
            {job.agent && (
              <Field label={t("cron.agent")}>
                <span className="font-mono">{job.agent}</span>
              </Field>
            )}
            {job.created_at && (
              <Field label={t("cron.created")}>{formatAbsolute(job.created_at)}</Field>
            )}
          </div>
        </CollapsibleContent>
      </Collapsible>
    </div>
  );
}

export function CronDialog({
  sessionKey,
  open,
  onOpenChange,
  onOpenSession,
}: {
  sessionKey: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpenSession: (key: string) => void;
}) {
  const { t } = useTranslation();
  const [data, setData] = useState<SessionCron | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setData(null);
    setError("");
    fetchSessionCron(sessionKey)
      .then((d) => {
        if (!cancelled) setData(d);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [open, sessionKey]);

  const openSession = (key: string) => {
    onOpenChange(false);
    onOpenSession(key);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85dvh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Clock className="size-4" />
            {t("cron.title")}
          </DialogTitle>
          <DialogDescription className="font-mono text-xs">
            {sessionKey}
            {data && ` · ${t("cron.count", { count: data.jobs.length })}`}
          </DialogDescription>
        </DialogHeader>
        {error && <p className="text-destructive text-xs">{error}</p>}
        {data === null ? (
          !error && <p className="text-muted-foreground text-sm">{t("common.loading")}</p>
        ) : data.jobs.length === 0 ? (
          <p className="text-muted-foreground text-sm">{t("cron.empty")}</p>
        ) : (
          <>
            <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto overscroll-contain pe-1">
              {data.jobs.map((j) => (
                <CronRow
                  key={j.id}
                  job={j}
                  sessionKey={sessionKey}
                  onOpenSession={openSession}
                />
              ))}
            </div>
            <p className="text-muted-foreground text-[11px]">
              {t("cron.timezone", { tz: data.timezone })}
            </p>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
