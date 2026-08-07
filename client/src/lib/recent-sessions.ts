// Which conversations this browser was last in.
//
// Deliberately per-browser and never sent anywhere: the server already knows
// every session and orders them by `updated_at`, which is when the SESSION last
// moved — a cron job writing at 4am outranks the thing you were reading a
// minute ago. This list answers a different question ("what was I just doing on
// this device"), and it is the only one the welcome screen wants.

const storageKey = "recent-sessions";

// Three are shown. More are kept because the strip filters at render time — the
// open session is dropped, and so is anything the server no longer lists — and
// a stored list of exactly three would empty out as soon as it filtered one.
const storedLimit = 12;

export type RecentSession = {
  key: string;
  // The session's summary as it read when it was last visited. A fallback for
  // when the server list has not arrived (or no longer carries the session);
  // the live summary wins whenever there is one.
  title?: string;
  at: number;
};

function isRecent(value: unknown): value is RecentSession {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.key === "string" &&
    v.key !== "" &&
    typeof v.at === "number" &&
    (v.title === undefined || typeof v.title === "string")
  );
}

export function loadRecentSessions(): RecentSession[] {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(storageKey) ?? "[]");
    if (!Array.isArray(raw)) return [];
    // Entries are validated individually rather than trusted wholesale: this is
    // data a previous version of the app wrote, and one malformed row must not
    // cost the whole list.
    return raw.filter(isRecent).sort((a, b) => b.at - a.at);
  } catch {
    return [];
  }
}

// recordRecentSession moves a session to the front of the list. Safe to call
// repeatedly for the session already open — the timestamp is what "recent"
// means, so refreshing it while the session is on screen is correct.
export function recordRecentSession(key: string, title?: string): void {
  try {
    const rest = loadRecentSessions().filter((r) => r.key !== key);
    const next = [{ key, title, at: Date.now() }, ...rest].slice(0, storedLimit);
    localStorage.setItem(storageKey, JSON.stringify(next));
  } catch {
    // A full or disabled localStorage costs the convenience, never the session.
  }
}
