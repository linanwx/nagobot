// REST client for the nagobot web channel API.

export type SessionEntry = {
  key: string;
  created_at: string;
  updated_at: string;
  message_count: number;
  has_heartbeat?: boolean;
  summary?: string;
};

export type ApiToolCall = {
  id?: string;
  function?: {
    name?: string;
    arguments?: string;
  };
};

export type ApiMessage = {
  role: string;
  content?: string;
  reasoning_content?: string;
  name?: string;
  id?: string;
  timestamp?: string;
  source?: string;
  tool_calls?: ApiToolCall[];
  // On role:"tool" results, the id of the tool call this answers.
  tool_call_id?: string;
  tokens?: number;
  compressed_tokens?: number;
};

export type SessionDetail = {
  key: string;
  messages: ApiMessage[];
  created_at: string;
  updated_at: string;
};

// The server routes /api/sessions/{key...} by converting "/" back to ":",
// so "discord:123" is addressed as /api/sessions/discord/123.
function sessionPath(key: string): string {
  return key.split(":").map(encodeURIComponent).join("/");
}

export async function fetchSessions(): Promise<SessionEntry[]> {
  const res = await fetch("/api/sessions");
  if (!res.ok) throw new Error(`GET /api/sessions: ${res.status}`);
  return res.json();
}

export async function fetchSession(key: string): Promise<SessionDetail> {
  const res = await fetch(`/api/sessions/${sessionPath(key)}`);
  if (!res.ok) throw new Error(`GET /api/sessions/${key}: ${res.status}`);
  return res.json();
}

// --- chat.jsonl (the clean user-facing conversation log) ---

export type ChatLogEntry = {
  role: string;
  // Structured attribution: the human speaker's name on user entries; on
  // assistant entries, the origin that drove a bot-initiated message (a
  // caller session key or wake source like "cron"). Absent on old entries.
  sender?: string;
  content: string;
  ts?: string;
};

// fetchChat returns null when the session has no chat.jsonl (server responds
// 404) or when the daemon predates the /chat endpoint (an HTML/404 fallback);
// callers then fall back to the session.jsonl mapping.
export async function fetchChat(key: string): Promise<ChatLogEntry[] | null> {
  const res = await fetch(`/api/sessions/${sessionPath(key)}/chat`);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`GET /api/sessions/${key}/chat: ${res.status}`);
  const data = await res.json();
  if (!data || !Array.isArray(data.messages)) return null;
  return data.messages as ChatLogEntry[];
}

// --- daemon configuration (read-only, secrets redacted server-side) ---

export async function fetchConfig(): Promise<unknown> {
  const res = await fetch("/api/config");
  if (!res.ok) throw new Error(`GET /api/config: ${res.status}`);
  return res.json();
}

// --- global prompt files ({workspace}/system/*.md) ---

export type PromptFileEntry = {
  name: string;
  // Server-curated display label and one-line description of the file's role
  // in the runtime (the list is a whitelist of runtime-injected files).
  label: string;
  description: string;
  size: number;
  modified: string;
};

export async function fetchPromptFiles(): Promise<PromptFileEntry[]> {
  const res = await fetch("/api/prompts");
  // 404 = daemon predates the endpoint; surface the empty state, not an error.
  if (res.status === 404) return [];
  if (!res.ok) throw new Error(`GET /api/prompts: ${res.status}`);
  const data = await res.json();
  return Array.isArray(data?.files) ? (data.files as PromptFileEntry[]) : [];
}

export async function fetchPromptFile(name: string): Promise<string> {
  // Names may contain a subdirectory ("sections/tools.md") — encode per
  // segment so the path keeps its shape.
  const path = name.split("/").map(encodeURIComponent).join("/");
  const res = await fetch(`/api/prompts/${path}`);
  if (!res.ok) throw new Error(`GET /api/prompts/${name}: ${res.status}`);
  const data = await res.json();
  return typeof data?.content === "string" ? data.content : "";
}

// Wake payloads are YAML frontmatter + markdown body. For display we keep the
// body and pull the display-relevant fields out of the frontmatter.
export type WakeInfo = {
  body: string;
  source?: string;
  sender?: string;
  // Human speaker display name (group-chat username) — `sender_name`.
  senderName?: string;
  // The session that sent us this message — `caller_session_key`.
  caller?: string;
  type?: string;
};

export function parseWakePayload(content: string): WakeInfo {
  // Wake payloads can stack multiple YAML blocks (an outer routing block, then
  // e.g. a heartbeat system block); strip every leading block for display but
  // collect fields from all of them (first occurrence wins).
  let body = content;
  let frontmatter = "";
  while (body.startsWith("---\n")) {
    const end = body.indexOf("\n---", 4);
    if (end === -1) break;
    frontmatter += body.slice(4, end) + "\n";
    body = body.slice(end + 4).replace(/^\n+/, "");
  }
  if (frontmatter === "") return { body: content };

  const field = (name: string): string | undefined => {
    const m = frontmatter.match(new RegExp(`^${name}:\\s*(.+)$`, "m"));
    return m?.[1]?.trim();
  };

  return {
    body: body.trim(),
    source: field("source"),
    sender: field("sender"),
    senderName: field("sender_name"),
    caller: field("caller_session_key"),
    type: field("type"),
  };
}

// splitSpeakerPrefix extracts the legacy "[Name]: " speaker prefix that group
// chats prepend to message text (data written before `sender_name` existed).
// Returns the name and the text without the prefix, or name "" when there is
// no prefix.
export function splitSpeakerPrefix(text: string): {
  name: string;
  rest: string;
} {
  const m = text.match(/^\[([^\]\n]{1,64})\]: /);
  if (!m) return { name: "", rest: text };
  return { name: m[1], rest: text.slice(m[0].length) };
}
