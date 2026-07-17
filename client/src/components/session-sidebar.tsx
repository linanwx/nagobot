import { ChevronRight } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { SidebarFooter } from "@/components/sidebar-footer";
import { Input } from "@/components/ui/input";
import type { SessionEntry } from "@/lib/api";
import { relativeTime } from "@/lib/sessions";
import { cn } from "@/lib/utils";

const collapseStorageKey = "sidebar-collapsed-folders";

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

// Folders ordered by their most recent session (sessions arrive time-sorted).
function groupByFolder(sessions: SessionEntry[]): Folder[] {
  const byName = new Map<string, SessionEntry[]>();
  for (const s of sessions) {
    const name = folderOf(s.key);
    const list = byName.get(name) ?? [];
    list.push(s);
    byName.set(name, list);
  }
  return [...byName.entries()].map(([name, list]) => ({ name, sessions: list }));
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
  showFolderBadge,
}: {
  session: SessionEntry;
  selected: string;
  onSelect: (key: string) => void;
  showFolderBadge?: boolean;
}) {
  const active = selected === session.key || selected.startsWith(session.key + ":");
  return (
    <button
      type="button"
      onClick={() => onSelect(session.key)}
      className={cn(
        "flex w-full flex-col gap-0.5 rounded-md px-2 py-1.5 text-left",
        active
          ? "bg-sidebar-accent text-sidebar-accent-foreground"
          : "hover:bg-sidebar-accent/50",
      )}
    >
      <span className="flex items-baseline gap-2">
        {showFolderBadge && (
          <span className="shrink-0 rounded bg-muted px-1 py-px text-[10px] uppercase tracking-wide text-muted-foreground">
            {folderOf(session.key)}
          </span>
        )}
        <span className="min-w-0 flex-1 truncate text-sm">
          {sessionLabel(session.key)}
        </span>
        <span className="shrink-0 text-[10px] text-muted-foreground">
          {relativeTime(session.updated_at)}
        </span>
      </span>
      {session.summary && (
        <span className="truncate text-xs text-muted-foreground">
          {session.summary}
        </span>
      )}
    </button>
  );
}

export function SessionSidebar({
  sessions,
  selected,
  onSelect,
  error,
  hiddenOnMobile,
}: {
  sessions: SessionEntry[];
  selected: string;
  onSelect: (key: string) => void;
  error: string | null;
  // Mobile shows list OR chat, never both; md+ always shows the sidebar.
  hiddenOnMobile?: boolean;
}) {
  const [filter, setFilter] = useState("");
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(loadCollapsed);

  useEffect(() => {
    localStorage.setItem(collapseStorageKey, JSON.stringify(collapsed));
  }, [collapsed]);

  const folders = useMemo(() => groupByFolder(sessions), [sessions]);

  const query = filter.trim().toLowerCase();
  const matches = useMemo(() => {
    if (query === "") return null;
    return sessions.filter(
      (s) =>
        s.key.toLowerCase().includes(query) ||
        (s.summary ?? "").toLowerCase().includes(query),
    );
  }, [sessions, query]);

  return (
    <aside
      className={cn(
        "h-full w-full shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground md:flex md:w-72",
        hiddenOnMobile ? "hidden" : "flex",
      )}
    >
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <span className="text-sm font-semibold tracking-wide">nagobot</span>
        <Input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter…"
          className="h-7 flex-1 text-xs"
        />
      </div>
      <nav className="flex-1 overflow-y-auto overscroll-contain px-2 py-2">
        {error && <p className="px-2 py-1 text-xs text-destructive">{error}</p>}

        {matches ? (
          // Filter mode: flat results across all folders.
          matches.length === 0 ? (
            <p className="px-2 py-1 text-xs text-muted-foreground">No matches</p>
          ) : (
            matches.map((s) => (
              <SessionRow
                key={s.key}
                session={s}
                selected={selected}
                onSelect={onSelect}
                showFolderBadge
              />
            ))
          )
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
