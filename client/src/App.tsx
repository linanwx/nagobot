import { useEffect, useMemo, useState } from "react";
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
  const [sessionKey, setSessionKey] = useState("cli");
  const [sessions, setSessions] = useState<SessionEntry[]>([]);
  // Browser-created sessions that have no session.jsonl yet — merged into the
  // sidebar until the server list includes them.
  const [draftSessions, setDraftSessions] = useState<SessionEntry[]>([]);
  const [sessionsError, setSessionsError] = useState<string | null>(null);
  // On narrow screens the list and the chat are two full-screen views; this
  // picks which one is showing. Desktop (md+) always shows both side by side.
  const [mobilePane, setMobilePane] = useState<"list" | "chat">("list");

  useEffect(() => {
    let cancelled = false;
    const load = () => {
      fetchSessions()
        .then((list) => {
          if (cancelled) return;
          setSessions(list);
          setSessionsError(null);
        })
        .catch((err: unknown) => {
          if (!cancelled) setSessionsError(String(err));
        });
    };
    load();
    const timer = setInterval(load, refreshIntervalMs);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

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

  const openSession = (key: string) => {
    setSessionKey(key);
    setMobilePane("chat");
  };

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
    <div className="flex h-dvh bg-background text-foreground">
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
        childSessions={children}
        parentSession={parent}
        onOpenSession={openSession}
        onBack={() => setMobilePane("list")}
        hiddenOnMobile={mobilePane === "list"}
      />
    </div>
  );
}
