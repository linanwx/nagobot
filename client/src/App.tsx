import { useCallback, useEffect, useMemo, useState } from "react";
import { ChatPane } from "@/components/chat-pane";
import { SessionSidebar } from "@/components/session-sidebar";
import { fetchSessions, type SessionEntry } from "@/lib/api";
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

export default function App() {
  // A fresh browser-minted key, so the app opens on the empty welcome screen
  // instead of loading someone else's session. Nothing is created server-side
  // until the first message — an unsent key dies with the tab. (It is also
  // deliberately NOT pushed into draftSessions: a phantom sidebar row on every
  // page load is noise. The explicit "+" button still shows its draft.)
  const [sessionKey, setSessionKey] = useState(newWebSessionKey);
  const [sessions, setSessions] = useState<SessionEntry[]>([]);
  // Browser-created sessions that have no session.jsonl yet — merged into the
  // sidebar until the server list includes them.
  const [draftSessions, setDraftSessions] = useState<SessionEntry[]>([]);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  // On narrow screens the list and the chat are two full-screen views; this
  // picks which one is showing. Desktop (md+) always shows both side by side.
  const [mobilePane, setMobilePane] = useState<"list" | "chat">("list");

  const refreshSessions = useCallback(() => {
    fetchSessions()
      .then((list) => {
        setSessions(list);
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

  const openSession = (key: string) => {
    setSessionKey(key);
    setMobilePane("chat");
  };

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
        error={sessionsError}
        hiddenOnMobile={mobilePane === "chat"}
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
        onBack={() => setMobilePane("list")}
        hiddenOnMobile={mobilePane === "list"}
      />
    </div>
  );
}
