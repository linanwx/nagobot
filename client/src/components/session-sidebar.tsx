import {
  Archive,
  ArchiveRestore,
  ChevronRight,
  Funnel,
  MoreHorizontal,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { SidebarFooter } from "@/components/sidebar-footer";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import type { SessionEntry } from "@/lib/api";
import { relativeTime, sessionLabel } from "@/lib/sessions";
import { cn } from "@/lib/utils";

const collapseStorageKey = "sidebar-collapsed-folders";
const filterStorageKey = "sidebar-filter-settings";
// Desktop-only: whether the whole sidebar is collapsed to a thin rail.
const railStorageKey = "sidebar-rail-collapsed";

// Tailwind's `md` breakpoint, the line where the drawer gives way to the
// permanent column.
const desktopQuery = "(min-width: 48rem)";

// The sidebar exists to reach ACTIVE conversations quickly, so by default it
// hides maintenance noise: cron sessions (knowledge updates, tidyup), CLI
// sessions (operator terminal traffic), anything quiet for over a week, and
// sessions someone archived. All are opt-in via the funnel menu.
//
// showArchived is the only one whose underlying state is server-side and shared
// (see /api/archive): the flag is global, this checkbox is just this browser's
// choice of whether to look at the filed-away rows.
type FilterSettings = {
  showCron: boolean;
  showCli: boolean;
  showOld: boolean;
  showArchived: boolean;
};

const defaultFilters: FilterSettings = {
  showCron: false,
  showCli: false,
  showOld: false,
  showArchived: false,
};

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

function loadRailCollapsed(): boolean {
  return localStorage.getItem(railStorageKey) === "true";
}

function SessionRow({
  session,
  selected,
  onSelect,
  onArchive,
}: {
  session: SessionEntry;
  selected: string;
  onSelect: (key: string) => void;
  onArchive: (key: string, archived: boolean) => void;
}) {
  const { t } = useTranslation();
  const active = selected === session.key || selected.startsWith(session.key + ":");
  // A summarized session is identified by its summary alone — the raw
  // session id adds nothing a human recognizes, so it moves to the tooltip.
  const label = session.summary || sessionLabel(session.key);
  // Geometry, colors and slot names copied from the assistant-ui thread-list
  // item: an h-8 rounded row whose trigger fills it, plus a trailing "..."
  // menu that only surfaces on hover / focus / while open. The row is a div
  // rather than a button for the same reason the native one is — the menu
  // trigger is a button, and a button cannot nest inside a button.
  return (
    <div
      data-slot="aui_thread-list-item"
      data-active={active || undefined}
      className={cn(
        "group relative flex h-8 w-full items-center rounded-md transition-colors",
        active
          ? "bg-muted"
          : "hover:bg-muted has-focus-visible:bg-muted has-data-[state=open]:bg-muted",
      )}
    >
      <button
        type="button"
        onClick={() => onSelect(session.key)}
        title={session.key}
        data-slot="aui_thread-list-item-trigger"
        className="flex h-full min-w-0 flex-1 items-center gap-2 rounded-md px-2.5 text-start text-sm focus-visible:outline-none"
      >
        <span
          data-slot="aui_thread-list-item-title"
          className="min-w-0 flex-1 truncate"
        >
          {label}
        </span>
        <span className="shrink-0 text-[10px] text-muted-foreground">
          {relativeTime(session.updated_at)}
        </span>
      </button>
      {/* Reveal is opacity, not `visible`/`hidden`, and that is deliberate: the
          menu is what surfaces it on focus, so hiding it in a way that also
          drops it from the tab order would leave a keyboard user with no way to
          reach archiving at all. Touch devices never fire :hover (Tailwind v4
          gates hover: behind `(hover: hover)`), so below md it stays visible —
          the drawer is the only way in on a phone. */}
      <div className="shrink-0 pe-1.5">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              data-slot="aui_thread-list-item-more"
              className={cn(
                "data-[state=open]:bg-accent size-6 p-0 transition-opacity",
                "opacity-0 max-md:opacity-100 group-hover:opacity-100",
                "focus-visible:opacity-100 data-[state=open]:opacity-100",
              )}
              title={t("sidebar.sessionMenu")}
            >
              <MoreHorizontal className="size-3.5" />
              <span className="sr-only">{t("sidebar.sessionMenu")}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" side="right" sideOffset={6}>
            <DropdownMenuItem
              onSelect={() => onArchive(session.key, !session.archived)}
            >
              {session.archived ? (
                <>
                  <ArchiveRestore className="size-4" />
                  {t("sidebar.unarchive")}
                </>
              ) : (
                <>
                  <Archive className="size-4" />
                  {t("sidebar.archive")}
                </>
              )}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
}

export function SessionSidebar({
  sessions,
  selected,
  onSelect,
  onCreate,
  onArchive,
  error,
  sheetOpen,
  onSheetOpenChange,
}: {
  sessions: SessionEntry[];
  selected: string;
  onSelect: (key: string) => void;
  // Start a fresh browser-created session (random web:* key).
  onCreate: () => void;
  // File a session away (or bring it back) for every viewer.
  onArchive: (key: string, archived: boolean) => void;
  error: string | null;
  // Mobile only: the sidebar rides in a drawer opened from the chat header.
  // md+ ignores both and renders the permanent column.
  sheetOpen: boolean;
  onSheetOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(loadCollapsed);
  const [filters, setFilters] = useState<FilterSettings>(loadFilters);
  // Desktop-only rail collapse — hides the sidebar to a thin edge strip.
  const [railCollapsed, setRailCollapsed] = useState<boolean>(loadRailCollapsed);

  useEffect(() => {
    localStorage.setItem(collapseStorageKey, JSON.stringify(collapsed));
  }, [collapsed]);

  useEffect(() => {
    localStorage.setItem(railStorageKey, String(railCollapsed));
  }, [railCollapsed]);

  useEffect(() => {
    localStorage.setItem(filterStorageKey, JSON.stringify(filters));
  }, [filters]);

  // A drawer opened on a phone must not survive a rotate or resize into the
  // desktop layout: its only trigger is the md:hidden hamburger, so nothing
  // would be left to dismiss the overlay and the page would sit behind it.
  useEffect(() => {
    const mq = window.matchMedia(desktopQuery);
    const closeOnDesktop = () => {
      if (mq.matches) onSheetOpenChange(false);
    };
    closeOnDesktop();
    mq.addEventListener("change", closeOnDesktop);
    return () => mq.removeEventListener("change", closeOnDesktop);
  }, [onSheetOpenChange]);

  const visible = useMemo(() => {
    const cutoff = Date.now() - oldSessionMs;
    return sessions.filter((s) => {
      if (!filters.showArchived && s.archived) return false;
      if (!filters.showCron && folderOf(s.key) === "cron") return false;
      if (!filters.showCli && folderOf(s.key) === "cli") return false;
      if (!filters.showOld) {
        const t = new Date(s.updated_at).getTime();
        if (!Number.isNaN(t) && t < cutoff) return false;
      }
      return true;
    });
  }, [sessions, filters]);

  const folders = useMemo(() => groupByFolder(visible), [visible]);
  const hiddenCount = sessions.length - visible.length;

  // One body, two shells (permanent column on desktop, drawer on mobile).
  // Radix unmounts a closed Sheet, so only one copy is ever in the DOM and the
  // collapse/filter state above is shared by both.
  const body = (
    <>
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <span className="text-sm font-semibold tracking-wide">nagobot</span>
        <span className="flex-1" />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="size-7 shrink-0 relative"
              title={t("sidebar.filter")}
            >
              <Funnel className="size-4" />
              {hiddenCount > 0 && (
                <span className="bg-primary absolute top-0.5 right-0.5 size-1.5 rounded-full" />
              )}
              <span className="sr-only">{t("sidebar.filter")}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-60">
            <DropdownMenuLabel className="text-muted-foreground text-xs font-normal">
              {hiddenCount > 0
                ? t("sidebar.hidden", { count: hiddenCount })
                : t("sidebar.showingAll")}
            </DropdownMenuLabel>
            <DropdownMenuCheckboxItem
              checked={filters.showCron}
              onCheckedChange={(v) =>
                setFilters((prev) => ({ ...prev, showCron: v === true }))
              }
            >
              {t("sidebar.showCron")}
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem
              checked={filters.showCli}
              onCheckedChange={(v) =>
                setFilters((prev) => ({ ...prev, showCli: v === true }))
              }
            >
              {t("sidebar.showCli")}
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem
              checked={filters.showOld}
              onCheckedChange={(v) =>
                setFilters((prev) => ({ ...prev, showOld: v === true }))
              }
            >
              {t("sidebar.showOld")}
            </DropdownMenuCheckboxItem>
            <DropdownMenuCheckboxItem
              checked={filters.showArchived}
              onCheckedChange={(v) =>
                setFilters((prev) => ({ ...prev, showArchived: v === true }))
              }
            >
              {t("sidebar.showArchived")}
            </DropdownMenuCheckboxItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <Button
          variant="ghost"
          size="icon"
          className="hidden size-7 shrink-0 md:inline-flex"
          onClick={() => setRailCollapsed(true)}
          title={t("sidebar.collapse")}
        >
          <PanelLeftClose className="size-4" />
          <span className="sr-only">{t("sidebar.collapse")}</span>
        </Button>
      </div>
      <nav
        data-slot="aui_thread-list-root"
        className="flex flex-1 flex-col gap-0.5 overflow-y-auto overscroll-contain px-2 py-2"
      >
        {/* Native thread-list "New Thread" affordance: a full-width ghost row
            with a leading Plus, at the top of the list rather than a header icon. */}
        <Button
          variant="ghost"
          data-slot="aui_thread-list-new"
          className="h-8 w-full justify-start gap-2 rounded-md px-2.5 text-sm font-normal hover:bg-muted"
          onClick={onCreate}
        >
          <Plus className="size-4 shrink-0" />
          <span className="whitespace-nowrap">{t("sidebar.newSession")}</span>
        </Button>

        {error && <p className="px-2 py-1 text-xs text-destructive">{error}</p>}

        {folders.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1 text-xs">
            {t("sidebar.noRecent")}
            {hiddenCount > 0 && t("sidebar.hiddenByFilters", { count: hiddenCount })}
          </p>
        ) : (
          folders.map((folder) => {
            // collapsed[name] === true means the folder is closed; Collapsible's
            // `open` is the inverse. localStorage keeps the same closed-set shape.
            const open = !(collapsed[folder.name] ?? false);
            return (
              <Collapsible
                key={folder.name}
                open={open}
                onOpenChange={(next) =>
                  setCollapsed((prev) => ({ ...prev, [folder.name]: !next }))
                }
                className="mb-1"
              >
                <CollapsibleTrigger
                  data-slot="aui_thread-list-group-label"
                  className="group flex w-full items-center gap-1 rounded-md px-2.5 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted/60"
                >
                  <ChevronRight className="size-3 transition-transform group-data-[state=open]:rotate-90" />
                  {folder.name}
                  <span className="ml-auto pr-0.5 font-normal tabular-nums">
                    {folder.sessions.length}
                  </span>
                </CollapsibleTrigger>
                <CollapsibleContent>
                  {folder.sessions.map((s) => (
                    <SessionRow
                      key={s.key}
                      session={s}
                      selected={selected}
                      onSelect={onSelect}
                      onArchive={onArchive}
                    />
                  ))}
                </CollapsibleContent>
              </Collapsible>
            );
          })
        )}
      </nav>
      <SidebarFooter />
    </>
  );

  return (
    <>
      {/* Desktop: a permanent column, collapsible to a rail. Collapsing is a
          width transition on ONE element with the body always mounted —
          swapping between two different subtrees leaves the browser nothing to
          interpolate, which is why this used to snap. The body keeps its full
          w-72 so narrowing the aside CLIPS it instead of reflowing every row
          mid-animation; it fades out and goes inert on the way. */}
      <aside
        className={cn(
          "relative hidden h-full shrink-0 overflow-hidden border-r bg-sidebar text-sidebar-foreground transition-[width] duration-200 md:block",
          railCollapsed ? "md:w-12" : "md:w-72",
        )}
      >
        <div
          className={cn(
            "flex h-full w-72 flex-col transition-opacity duration-150",
            railCollapsed && "pointer-events-none opacity-0",
          )}
          inert={railCollapsed}
        >
          {body}
        </div>
        {/* Rail affordance, fading in over the body once it has cleared out. */}
        <div
          className={cn(
            "absolute inset-y-0 left-0 flex w-12 flex-col items-center py-2 transition-opacity duration-200",
            railCollapsed ? "opacity-100 delay-100" : "pointer-events-none opacity-0",
          )}
          inert={!railCollapsed}
        >
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            onClick={() => setRailCollapsed(false)}
            title={t("sidebar.expand")}
          >
            <PanelLeftOpen className="size-4" />
            <span className="sr-only">{t("sidebar.expand")}</span>
          </Button>
        </div>
      </aside>

      {/* Mobile: the same body in a drawer over the chat. */}
      <Sheet open={sheetOpen} onOpenChange={onSheetOpenChange}>
        <SheetContent
          aria-describedby={undefined}
          className="bg-sidebar text-sidebar-foreground"
        >
          <SheetTitle className="sr-only">{t("sidebar.title")}</SheetTitle>
          {body}
        </SheetContent>
      </Sheet>
    </>
  );
}
