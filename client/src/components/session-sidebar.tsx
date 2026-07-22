import { ChevronRight, Funnel, Plus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { SidebarFooter } from "@/components/sidebar-footer";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { SessionEntry } from "@/lib/api";
import { relativeTime } from "@/lib/sessions";
import { cn } from "@/lib/utils";

const collapseStorageKey = "sidebar-collapsed-folders";
const filterStorageKey = "sidebar-filter-settings";

// The sidebar exists to reach ACTIVE conversations quickly, so by default it
// hides maintenance noise: cron sessions (knowledge updates, tidyup) and
// anything quiet for over a week. Both are opt-in via the funnel menu.
type FilterSettings = {
  showCron: boolean;
  showOld: boolean;
};

const defaultFilters: FilterSettings = { showCron: false, showOld: false };

const oldSessionMs = 7 * 24 * 60 * 60 * 1000;

function loadFilters(): FilterSettings {
  try {
    return { ...defaultFilters, ...JSON.parse(localStorage.getItem(filterStorageKey) ?? "{}") };
  } catch {
    return defaultFilters;
  }
}

type Folder = {
  name: string;
  sessions: SessionEntry[];
};

// Folder = channel prefix ("discord:123" → "discord"); keys without a colon
// ("cli", "main") form single-session folders named after themselves.
function folderOf(key: string): string {
  const idx = key.indexOf(":");
  return idx === -1 ? key : key.slice(0, idx);
}

function sessionLabel(key: string): string {
  const idx = key.indexOf(":");
  return idx === -1 ? key : key.slice(idx + 1);
}

// Folders ordered by their most recent session (sessions arrive time-sorted),
// except "web" — the browser's own sessions — which is always pinned first.
function groupByFolder(sessions: SessionEntry[]): Folder[] {
  const byName = new Map<string, SessionEntry[]>();
  for (const s of sessions) {
    const name = folderOf(s.key);
    const list = byName.get(name) ?? [];
    list.push(s);
    byName.set(name, list);
  }
  const folders = [...byName.entries()].map(([name, list]) => ({ name, sessions: list }));
  const webIdx = folders.findIndex((f) => f.name === "web");
  if (webIdx > 0) folders.unshift(...folders.splice(webIdx, 1));
  return folders;
}

function loadCollapsed(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(collapseStorageKey) ?? "{}");
  } catch {
    return {};
  }
}

function SessionRow({
  session,
  selected,
  onSelect,
}: {
  session: SessionEntry;
  selected: string;
  onSelect: (key: string) => void;
}) {
  const active = selected === session.key || selected.startsWith(session.key + ":");
  // A summarized session is identified by its summary alone — the raw
  // session id adds nothing a human recognizes, so it moves to the tooltip.
  const label = session.summary || sessionLabel(session.key);
  return (
    <button
      type="button"
      onClick={() => onSelect(session.key)}
      title={session.key}
      className={cn(
        "flex w-full flex-col gap-0.5 rounded-md px-2 py-1.5 text-left",
        active
          ? "bg-sidebar-accent text-sidebar-accent-foreground"
          : "hover:bg-sidebar-accent/50",
      )}
    >
      <span className="flex items-baseline gap-2">
        <span className="min-w-0 flex-1 truncate text-sm">{label}</span>
        <span className="shrink-0 text-[10px] text-muted-foreground">
          {relativeTime(session.updated_at)}
        </span>
      </span>
    </button>
  );
}

export function SessionSidebar({
  sessions,
  selected,
  onSelect,
  onCreate,
  error,
  hiddenOnMobile,
}: {
  sessions: SessionEntry[];
  selected: string;
  onSelect: (key: string) => void;
  // Start a fresh browser-created session (random web:* key).
  onCreate: () => void;
  error: string | null;
  // Mobile shows list OR chat, never both; md+ always shows the sidebar.
  hiddenOnMobile?: boolean;
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(loadCollapsed);
  const [filters, setFilters] = useState<FilterSettings>(loadFilters);

  useEffect(() => {
    localStorage.setItem(collapseStorageKey, JSON.stringify(collapsed));
  }, [collapsed]);

  useEffect(() => {
    localStorage.setItem(filterStorageKey, JSON.stringify(filters));
  }, [filters]);

  const visible = useMemo(() => {
    const cutoff = Date.now() - oldSessionMs;
    return sessions.filter((s) => {
      if (!filters.showCron && folderOf(s.key) === "cron") return false;
      if (!filters.showOld) {
        const t = new Date(s.updated_at).getTime();
        if (!Number.isNaN(t) && t < cutoff) return false;
      }
      return true;
    });
  }, [sessions, filters]);

  const folders = useMemo(() => groupByFolder(visible), [visible]);
  const hiddenCount = sessions.length - visible.length;

  return (
    <aside
      className={cn(
        "h-full w-full shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground md:flex md:w-72",
        hiddenOnMobile ? "hidden" : "flex",
      )}
    >
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <span className="text-sm font-semibold tracking-wide">nagobot</span>
        <span className="flex-1" />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="size-7 shrink-0 relative"
              title="Filter sessions"
            >
              <Funnel className="size-4" />
              {hiddenCount > 0 && (
                <span className="bg-primary absolute top-0.5 right-0.5 size-1.5 rounded-full" />
              )}
              <span className="sr-only">Filter sessions</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-60">
            <DropdownMenuLabel className="text-muted-foreground text-xs font-normal">
              {hiddenCount > 0
                ? `${hiddenCount} session${hiddenCount === 1 ? "" : "s"} hidden`
                : "Showing all sessions"}
            </DropdownMenuLabel>
            <DropdownMenuCheckboxItem
              checked={filters.showCron}
              onCheckedChange={(v) =>
                setFilters((prev) => ({ ...prev, showCron: v === true }))
              }
            >
              Show scheduled (cron) sessions
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem
              checked={filters.showOld}
              onCheckedChange={(v) =>
                setFilters((prev) => ({ ...prev, showOld: v === true }))
              }
            >
              Show sessions older than 7 days
            </DropdownMenuCheckboxItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <Button
          variant="ghost"
          size="icon"
          className="size-7 shrink-0"
          onClick={onCreate}
          title="New session"
        >
          <Plus className="size-4" />
          <span className="sr-only">New session</span>
        </Button>
      </div>
      <nav className="flex-1 overflow-y-auto overscroll-contain px-2 py-2">
        {error && <p className="px-2 py-1 text-xs text-destructive">{error}</p>}

        {folders.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1 text-xs">
            No recent sessions
            {hiddenCount > 0 && ` (${hiddenCount} hidden by filters)`}
          </p>
        ) : (
          folders.map((folder) => {
            const isCollapsed = collapsed[folder.name] ?? false;
            return (
              <div key={folder.name} className="mb-1">
                <button
                  type="button"
                  onClick={() =>
                    setCollapsed((prev) => ({
                      ...prev,
                      [folder.name]: !isCollapsed,
                    }))
                  }
                  className="flex w-full items-center gap-1 rounded-md px-1 py-1 text-xs font-medium uppercase tracking-wider text-muted-foreground hover:bg-sidebar-accent/50"
                >
                  <ChevronRight
                    className={cn(
                      "size-3 transition-transform",
                      !isCollapsed && "rotate-90",
                    )}
                  />
                  {folder.name}
                  <span className="ml-auto pr-1 font-normal">
                    {folder.sessions.length}
                  </span>
                </button>
                {!isCollapsed &&
                  folder.sessions.map((s) => (
                    <SessionRow
                      key={s.key}
                      session={s}
                      selected={selected}
                      onSelect={onSelect}
                    />
                  ))}
              </div>
            );
          })
        )}
      </nav>
      <SidebarFooter />
    </aside>
  );
}
