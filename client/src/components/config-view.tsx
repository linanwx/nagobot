import { ChevronRight } from "lucide-react";
import { useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

// Structured, read-only renderer for the daemon config (GET /api/config),
// mirroring the layout of the legacy 8080 page: titled sections, key-value
// rows, collapsible nested groups, and cron jobs as cards with badges.

type Dict = Record<string, unknown>;

function isDict(v: unknown): v is Dict {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function scalarText(v: unknown): string {
  if (v === null || v === undefined || v === "") return "—";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

function KVRow({ k, v }: { k: string; v: unknown }) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1">
      <span className="text-muted-foreground shrink-0 text-xs">{k}</span>
      <span className="min-w-0 text-end font-mono text-xs break-all">
        {scalarText(v)}
      </span>
    </div>
  );
}

// Collapsible group for nested objects/arrays, collapsed by default.
function NestedGroup({ label, value }: { label: string; value: unknown }) {
  const [open, setOpen] = useState(false);
  const rows: [string, unknown][] = isDict(value)
    ? Object.entries(value)
    : Array.isArray(value)
      ? value.map((v, i) => [String(i), v] as [string, unknown])
      : [["value", value]];
  return (
    <div className="py-0.5">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="text-foreground/80 hover:text-foreground flex w-full items-center gap-1 py-0.5 text-xs font-medium"
      >
        <ChevronRight
          className={cn("size-3 shrink-0 transition-transform", open && "rotate-90")}
        />
        {label}
        <span className="text-muted-foreground ms-auto font-normal">
          {rows.length}
        </span>
      </button>
      {open && (
        <div className="border-border/60 ms-1.5 border-s ps-3">
          {rows.map(([k, v]) =>
            isDict(v) || Array.isArray(v) ? (
              <NestedGroup key={k} label={k} value={v} />
            ) : (
              <KVRow key={k} k={k} v={v} />
            ),
          )}
        </div>
      )}
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mb-4 last:mb-0">
      <h3 className="text-muted-foreground mb-1 text-[11px] font-semibold tracking-wider uppercase">
        {title}
      </h3>
      <div className="rounded-md border px-3 py-1.5">{children}</div>
    </section>
  );
}

function Badge({
  children,
  tone,
}: {
  children: ReactNode;
  tone?: "accent" | "muted";
}) {
  return (
    <span
      className={cn(
        "rounded px-1.5 py-px text-[10px]",
        tone === "accent"
          ? "bg-sky-500/15 text-sky-700 dark:text-sky-300"
          : "bg-muted text-muted-foreground",
      )}
    >
      {children}
    </span>
  );
}

type CronJob = {
  id?: string;
  kind?: string;
  expr?: string;
  at_time?: string;
  agent?: string;
  silent?: boolean;
  task?: string;
  wake_session?: string;
  created_at?: string;
};

const zeroTime = "0001-01-01T00:00:00Z";

function CronCard({ job }: { job: CronJob }) {
  return (
    <div className="border-border/60 mb-2 rounded-md border p-2 last:mb-0">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="font-mono text-xs font-medium">{job.id ?? "unknown"}</span>
        {job.kind && (
          <Badge>{job.kind === "at" ? "one-shot" : "recurring"}</Badge>
        )}
        {job.at_time && job.at_time !== zeroTime ? (
          <Badge>{new Date(job.at_time).toLocaleString()}</Badge>
        ) : job.expr ? (
          <Badge>{job.expr}</Badge>
        ) : null}
        {job.agent && <Badge tone="accent">{job.agent}</Badge>}
        {job.silent && <Badge>silent</Badge>}
      </div>
      {job.task && (
        <p className="text-muted-foreground mt-1 line-clamp-3 text-xs">
          {job.task}
        </p>
      )}
      {(job.wake_session || (job.created_at && job.created_at !== zeroTime)) && (
        <p className="text-muted-foreground/70 mt-1 flex flex-wrap gap-2 text-[10px]">
          {job.wake_session && <span>→ {job.wake_session}</span>}
          {job.created_at && job.created_at !== zeroTime && (
            <span>created {new Date(job.created_at).toLocaleDateString()}</span>
          )}
        </p>
      )}
    </div>
  );
}

type ModelRule = {
  type?: string;
  name?: string;
  provider?: string;
  modelType?: string;
};

// Model routing rules read better as a table than as JSON rows.
function ModelRulesTable({ rules }: { rules: ModelRule[] }) {
  return (
    <div className="w-full overflow-x-auto py-1">
      <table className="w-full text-xs">
        <thead>
          <tr className="text-muted-foreground text-start text-[10px] uppercase">
            <th className="py-0.5 pe-2 text-start font-medium">type</th>
            <th className="py-0.5 pe-2 text-start font-medium">name</th>
            <th className="py-0.5 pe-2 text-start font-medium">provider</th>
            <th className="py-0.5 text-start font-medium">model</th>
          </tr>
        </thead>
        <tbody>
          {rules.map((r, i) => (
            <tr key={i} className="border-border/40 border-t">
              <td className="py-1 pe-2">
                <Badge>{r.type ?? "?"}</Badge>
              </td>
              <td className="py-1 pe-2 font-mono">{r.name ?? "—"}</td>
              <td className="py-1 pe-2 font-mono">{r.provider ?? "—"}</td>
              <td className="py-1 font-mono break-all">{r.modelType ?? "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function objectSection(title: string, obj: Dict, skip: Set<string> = new Set()) {
  const entries = Object.entries(obj).filter(([k]) => !skip.has(k));
  if (entries.length === 0) return null;
  return (
    <Section title={title}>
      {entries.map(([k, v]) =>
        isDict(v) || Array.isArray(v) ? (
          <NestedGroup key={k} label={k} value={v} />
        ) : (
          <KVRow key={k} k={k} v={v} />
        ),
      )}
    </Section>
  );
}

export function ConfigView({ config }: { config: unknown }) {
  if (!isDict(config)) {
    return (
      <pre className="bg-muted/50 overflow-auto rounded-md p-3 font-mono text-xs">
        {JSON.stringify(config, null, 2)}
      </pre>
    );
  }

  const thread = isDict(config.thread) ? config.thread : null;
  const modelRules =
    thread && Array.isArray(thread.models) ? (thread.models as ModelRule[]) : null;
  const channels = isDict(config.channels) ? config.channels : null;
  const cron = Array.isArray(config.cron) ? (config.cron as CronJob[]) : null;
  const handled = new Set(["thread", "channels", "cron"]);

  return (
    <div>
      {thread && objectSection("Thread", thread, new Set(["models"]))}
      {modelRules && modelRules.length > 0 && (
        <Section title={`Model routing (${modelRules.length})`}>
          <ModelRulesTable rules={modelRules} />
        </Section>
      )}
      {channels && objectSection("Channels", channels)}
      {cron && cron.length > 0 && (
        <Section title={`Cron jobs (${cron.length})`}>
          <div className="py-1">
            {cron.map((job, i) => (
              <CronCard key={job.id ?? i} job={job} />
            ))}
          </div>
        </Section>
      )}
      {Object.entries(config)
        .filter(([k]) => !handled.has(k))
        .map(([k, v]) =>
          isDict(v) ? (
            <div key={k}>{objectSection(k, v)}</div>
          ) : (
            <Section key={k} title={k}>
              <KVRow k={k} v={v} />
            </Section>
          ),
        )}
    </div>
  );
}
