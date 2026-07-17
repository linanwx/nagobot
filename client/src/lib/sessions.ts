import type { SessionEntry } from "@/lib/api";

// A session is a child when another session's key is a strict ":"-prefix of
// its key (e.g. "cli" makes "cli:prethink" and "cli:threads:x" children).
// This is structural — no hardcoded suffix list.

function keySet(all: SessionEntry[]): Set<string> {
  return new Set(all.map((e) => e.key));
}

function hasParentIn(key: string, keys: Set<string>): boolean {
  let idx = key.lastIndexOf(":");
  while (idx > 0) {
    if (keys.has(key.slice(0, idx))) return true;
    idx = key.lastIndexOf(":", idx - 1);
  }
  return false;
}

export function topLevelSessions(all: SessionEntry[]): SessionEntry[] {
  const keys = keySet(all);
  return all
    .filter((e) => !hasParentIn(e.key, keys))
    .sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
}

export function childSessionsOf(all: SessionEntry[], parent: string): SessionEntry[] {
  const prefix = parent + ":";
  return all
    .filter((e) => e.key.startsWith(prefix))
    .sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
}

// Longest existing ancestor of key, or null when key is top-level.
export function parentSessionOf(all: SessionEntry[], key: string): string | null {
  const keys = keySet(all);
  let idx = key.lastIndexOf(":");
  while (idx > 0) {
    const candidate = key.slice(0, idx);
    if (keys.has(candidate)) return candidate;
    idx = key.lastIndexOf(":", idx - 1);
  }
  return null;
}

// formatMessageTime renders a message timestamp compactly: time-only for
// today, month-day + time within the year, full date otherwise.
export function formatMessageTime(d: Date): string {
  const t = d.getTime();
  if (Number.isNaN(t) || t <= 0) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return hm;
  const md = `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  if (d.getFullYear() === now.getFullYear()) return `${md} ${hm}`;
  return `${d.getFullYear()}-${md} ${hm}`;
}

export function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  // Zero-value timestamps (year 1) come through for sessions that predate
  // updated_at tracking; showing "739812d" helps nobody.
  if (Number.isNaN(then) || then <= 0) return "";
  const mins = Math.floor((Date.now() - then) / 60_000);
  if (mins < 1) return "now";
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}
