import { MessageSquareText } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { SessionEntry } from "@/lib/api";
import { loadRecentSessions } from "@/lib/recent-sessions";
import { relativeTime, sessionLabel } from "@/lib/sessions";

const shown = 3;

// RecentSessions is the strip under "How can I help you today?" — the last few
// conversations this browser was in, as a way back into one without opening the
// sidebar.
//
// It reads storage once, on mount, and that is enough because the pane above it
// is keyed by session key: every navigation remounts this. Reading on each
// render would instead re-sort the list under the cursor the moment the open
// session's own timestamp were refreshed.
export function RecentSessions({
  sessions,
  sessionsLoaded,
  currentKey,
  onOpen,
}: {
  sessions: SessionEntry[];
  // Whether `sessions` is a real answer yet. An empty list before the first
  // fetch lands means "unknown", not "nothing exists", and filtering against it
  // would blank the strip for a moment on every load.
  sessionsLoaded: boolean;
  currentKey: string;
  onOpen: (key: string) => void;
}) {
  const { t } = useTranslation();
  const [stored] = useState(loadRecentSessions);

  const entries = useMemo(() => {
    const live = new Map(sessions.map((s) => [s.key, s]));
    return stored
      .filter((r) => r.key !== currentKey)
      // Once the server list is known, anything missing from it is gone —
      // deleted from another device, or on a deployment this browser also talks
      // to. Offering a dead session is worse than offering one fewer.
      .filter((r) => !sessionsLoaded || live.has(r.key))
      .slice(0, shown)
      .map((r) => ({
        key: r.key,
        label: live.get(r.key)?.summary || r.title || sessionLabel(r.key),
        at: live.get(r.key)?.updated_at,
      }));
  }, [stored, sessions, sessionsLoaded, currentKey]);

  if (entries.length === 0) return null;

  return (
    <div className="fade-in slide-in-from-bottom-2 animate-in fill-mode-both mx-auto w-full max-w-md px-4 duration-300">
      <div className="text-muted-foreground mb-1.5 px-2.5 text-xs font-medium">
        {t("thread.recent")}
      </div>
      <div className="flex flex-col">
        {entries.map((e) => (
          <button
            key={e.key}
            type="button"
            onClick={() => onOpen(e.key)}
            title={e.key}
            className="hover:bg-muted focus-visible:bg-muted flex h-9 items-center gap-2 rounded-md px-2.5 text-start text-sm transition-colors focus-visible:outline-none"
          >
            <MessageSquareText className="text-muted-foreground size-3.5 shrink-0" />
            <span className="min-w-0 flex-1 truncate">{e.label}</span>
            {e.at && (
              <span className="text-muted-foreground shrink-0 text-[10px]">
                {relativeTime(e.at)}
              </span>
            )}
          </button>
        ))}
      </div>
    </div>
  );
}
