import { useEffect, useMemo, useState } from "react";
import { ChatPane } from "@/components/chat-pane";
import { SessionSidebar } from "@/components/session-sidebar";
import { fetchSessions, type SessionEntry } from "@/lib/api";
import { childSessionsOf, parentSessionOf, topLevelSessions } from "@/lib/sessions";

const refreshIntervalMs = 30_000;

export default function App() {
  const [sessionKey, setSessionKey] = useState("cli");
  const [sessions, setSessions] = useState<SessionEntry[]>([]);
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

  const topLevel = useMemo(() => topLevelSessions(sessions), [sessions]);
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

  return (
    <div className="flex h-dvh bg-background text-foreground">
      <SessionSidebar
        sessions={topLevel}
        selected={sessionKey}
        onSelect={openSession}
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
