import { useCallback, useEffect, useMemo, useState } from "react";
import { ChatPane } from "@/components/chat-pane";
import { SessionSidebar } from "@/components/session-sidebar";
import { fetchSessions, setSessionArchived, type SessionEntry } from "@/lib/api";
import { childSessionsOf, parentSessionOf, topLevelSessions } from "@/lib/sessions";

const refreshIntervalMs = 30_000;

// newWebSessionKey mints the key for a browser-created session. The web:
// prefix keeps these out of the cli/discord/cron namespaces; the dispatcher
// materializes the session on its first message.
function newWebSessionKey(): string {
  const bytes = new Uint8Array(4);
  crypto.getRandomValues(bytes);
  const id = [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
  return `web:${id}`;
}

// The query key that addresses a session. A session key contains a colon
// ("web:2118acc7"), so it always rides encoded — and every read goes through
// URLSearchParams, which decodes, so the two sides cannot disagree about
// encoding. public/sw.js compares the same way for the same reason.
const sessionParam = "s";

function sessionFromURL(): string | null {
  const raw = new URLSearchParams(window.location.search).get(sessionParam);
  const key = raw?.trim() ?? "";
  return key === "" ? null : key;
}

// syncURL points the address bar at the open session. replaceState, not
// pushState: switching sessions is not navigation, and one history entry per
// sidebar click would turn the back button into an undo stack for the list.
function syncURL(key: string | null) {
  const url = new URL(window.location.href);
  if (key) url.searchParams.set(sessionParam, key);
  else url.searchParams.delete(sessionParam);
  window.history.replaceState(null, "", url.toString());
}

export default function App() {
  // The session named by the URL, captured once at mount. Held separately from
  // sessionKey because it is what gets validated below — by the time the first
  // session list lands the user may already have moved on.
  const [urlSession] = useState(sessionFromURL);
  // An address bar naming a session opens it; otherwise a fresh browser-minted
  // key, so the app opens on the empty welcome screen instead of loading
  // someone else's session. Nothing is created server-side until the first
  // message — an unsent key dies with the tab. (It is also deliberately NOT
  // pushed into draftSessions: a phantom sidebar row on every page load is
  // noise. The explicit "+" button still shows its draft.)
  const [sessionKey, setSessionKey] = useState(
    () => sessionFromURL() ?? newWebSessionKey(),
  );
  const [sessions, setSessions] = useState<SessionEntry[]>([]);
  // Distinct from sessions.length: an empty list is a real answer (a fresh
  // deployment), and the URL check below must not mistake "not fetched yet"
  // for it.
  const [sessionsLoaded, setSessionsLoaded] = useState(false);
  // Browser-created sessions that have no session.jsonl yet — merged into the
  // sidebar until the server list includes them.
  const [draftSessions, setDraftSessions] = useState<SessionEntry[]>([]);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  // On narrow screens the chat owns the viewport and the session list rides in
  // a drawer over it. Desktop (md+) ignores this and shows both side by side.
  const [sheetOpen, setSheetOpen] = useState(false);

  const refreshSessions = useCallback(() => {
    fetchSessions()
      .then((list) => {
        setSessions(list);
        setSessionsLoaded(true);
        setSessionsError(null);
      })
      .catch((err: unknown) => {
        setSessionsError(String(err));
      });
  }, []);

  useEffect(() => {
    refreshSessions();
    const timer = setInterval(refreshSessions, refreshIntervalMs);
    return () => clearInterval(timer);
  }, [refreshSessions]);

  const merged = useMemo(() => {
    const known = new Set(sessions.map((s) => s.key));
    const drafts = draftSessions.filter((d) => !known.has(d.key));
    return [...drafts, ...sessions];
  }, [sessions, draftSessions]);

  const topLevel = useMemo(() => topLevelSessions(merged), [merged]);
  const children = useMemo(
    () => childSessionsOf(sessions, sessionKey),
    [sessions, sessionKey],
  );
  const parent = useMemo(
    () => parentSessionOf(sessions, sessionKey),
    [sessions, sessionKey],
  );
  // The open session's summary (drafts and unsummarized sessions have none) —
  // the chat header prefers it over the opaque session id.
  const currentSummary = useMemo(
    () => sessions.find((s) => s.key === sessionKey)?.summary,
    [sessions, sessionKey],
  );

  const openSession = useCallback((key: string) => {
    setSessionKey(key);
    syncURL(key);
    setSheetOpen(false);
  }, []);

  // A URL can name a session that does not exist — a notification for a session
  // since deleted, a link shared from another deployment, a hand-typed key. Fall
  // back to a fresh session and drop it from the address bar.
  //
  // Checked once, against the first list that arrives, and only while the page
  // is still on the key it opened with. Re-validating on every 30s refresh would
  // yank a session out from under a reader the moment it were archived; checking
  // unconditionally would undo a switch the user made while the fetch was in
  // flight. A draft session created in this tab is intentionally not exempt:
  // reloading such a URL lands on an equally empty new session.
  const [urlChecked, setUrlChecked] = useState(false);
  useEffect(() => {
    if (urlChecked || urlSession === null || !sessionsLoaded) return;
    setUrlChecked(true);
    if (sessionKey !== urlSession) return;
    if (sessions.some((s) => s.key === urlSession)) return;
    setSessionKey(newWebSessionKey());
    syncURL(null);
  }, [urlChecked, urlSession, sessionsLoaded, sessions, sessionKey]);

  // A notification click on an already-open page cannot navigate it: the
  // service worker has no reach into React state, so it focuses the window and
  // posts the session here instead. See public/sw.js.
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    const onMessage = (e: MessageEvent) => {
      const data = e.data as { type?: string; session?: string } | null;
      if (data?.type === "open-session" && data.session) openSession(data.session);
    };
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () =>
      navigator.serviceWorker.removeEventListener("message", onMessage);
  }, [openSession]);

  // The open session just sent its first message, so it now exists (or is about
  // to) server-side. Surface it in the sidebar immediately with a draft row —
  // otherwise the session you are actively typing in stays missing for up to a
  // full refresh interval. `merged` drops the draft once the server list carries
  // the key, which makes this idempotent for already-known sessions.
  const handleFirstSend = useCallback(
    (key: string) => {
      setDraftSessions((prev) => {
        if (prev.some((d) => d.key === key)) return prev;
        const now = new Date().toISOString();
        return [
          { key, created_at: now, updated_at: now, message_count: 0 },
          ...prev,
        ];
      });
      refreshSessions();
    },
    [refreshSessions],
  );

  // Archiving is server-side and global, but the row has to leave (or rejoin)
  // the list on the click rather than at the next 30s refresh. The local flag
  // is applied first and the refresh below reconciles it; a failed write is
  // surfaced in the sidebar's error slot and reverted by that same refresh, so
  // the list never keeps a state the server rejected.
  const archiveSession = useCallback(
    (key: string, archived: boolean) => {
      setSessions((prev) =>
        prev.map((s) => (s.key === key ? { ...s, archived } : s)),
      );
      setSessionArchived(key, archived)
        .then(() => setSessionsError(null))
        .catch((err: unknown) => setSessionsError(String(err)))
        .finally(refreshSessions);
    },
    [refreshSessions],
  );

  const createSession = () => {
    const key = newWebSessionKey();
    const now = new Date().toISOString();
    setDraftSessions((prev) => [
      { key, created_at: now, updated_at: now, message_count: 0 },
      ...prev,
    ]);
    openSession(key);
  };

  return (
    // viewport-fit=cover lets the page bleed under the iOS notch/home
    // indicator — and switches OFF the browser's own insetting, so every edge
    // we want protected has to be restored here by hand. Top and the two
    // horizontal insets are compensated on this root; the horizontal pair is
    // what keeps content out of the sensor housing and the rounded corners in
    // LANDSCAPE, where iOS reports 44-59px there (portrait reports 0, which is
    // why omitting them looked harmless). The bottom is deliberately NOT
    // compensated here — it is applied inside the sidebar footer and composer
    // instead, so their backgrounds still fill the bar down to the screen edge.
    // Height is dvh MINUS the keyboard: Android shrinks dvh by itself so
    // --keyboard-inset stays 0 there, while iOS never shrinks it and the
    // variable carries the whole keyboard height (see lib/keyboard-inset.ts).
    <div
      className="flex bg-background text-foreground"
      style={{
        height: "calc(100dvh - var(--keyboard-inset, 0px))",
        paddingTop: "env(safe-area-inset-top)",
        paddingLeft: "env(safe-area-inset-left)",
        paddingRight: "env(safe-area-inset-right)",
      }}
    >
      <SessionSidebar
        sessions={topLevel}
        selected={sessionKey}
        onSelect={openSession}
        onCreate={createSession}
        onArchive={archiveSession}
        error={sessionsError}
        sheetOpen={sheetOpen}
        onSheetOpenChange={setSheetOpen}
      />
      {/* key remounts the pane so each session gets a fresh socket + history */}
      <ChatPane
        key={sessionKey}
        sessionKey={sessionKey}
        summary={currentSummary}
        onFirstSend={handleFirstSend}
        childSessions={children}
        parentSession={parent}
        onOpenSession={openSession}
        onOpenSidebar={() => setSheetOpen(true)}
      />
    </div>
  );
}
