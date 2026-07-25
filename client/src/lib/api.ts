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
  type?: string;
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
  // Native media markers ("<<media:image/jpeg:/abs/path>>") on user messages.
  media?: string[];
  // Tier-1 compressed replacement of the content — presence marks the
  // message as compressed.
  compressed?: string;
  // Opaque provider reasoning details (e.g. Gemini thought signatures).
  reasoning_details?: unknown;
  // Tier-1 flags: reasoning excluded at send time / whole heartbeat turn
  // removed at send time / result exempt from compression.
  reasoning_trimmed?: boolean;
  heartbeat_trim?: boolean;
  skip_trim?: boolean;
  reasoning_tokens?: number;
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

// --- live system prompt (rebuilt from the in-memory thread) ---

export type SystemPromptInfo = {
  key: string;
  prompt: string;
  // False when no thread is loaded for the key — threads are GC'd after 3h
  // idle, and the prompt only exists as a build off live thread state.
  available: boolean;
  tokens: number;
};

export async function fetchSystemPrompt(key: string): Promise<SystemPromptInfo> {
  const res = await fetch(`/api/sessions/${sessionPath(key)}/system-prompt`);
  if (!res.ok) {
    throw new Error(`GET /api/sessions/${key}/system-prompt: ${res.status}`);
  }
  const data = await res.json();
  return {
    key,
    prompt: typeof data?.prompt === "string" ? data.prompt : "",
    available: data?.available === true,
    tokens: typeof data?.tokens === "number" ? data.tokens : 0,
  };
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
  // Stable sender identity — `sender_id` ("discord:1480..." or
  // "person:p_xxx" for authenticated web users). The UI aligns "me" with it.
  senderID?: string;
  // The session that sent us this message — `caller_session_key`.
  caller?: string;
  // Channel media summary — `media` ("[Media: photo] image_path: … caption: …").
  media?: string;
  // Upfront image description / audio transcription — `media_preview`.
  mediaPreview?: string;
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

  // Frontmatter string values may be single-quoted (YAML) — strip the quotes
  // for display.
  const unquote = (v: string | undefined): string | undefined => {
    if (v && v.length >= 2 && v.startsWith("'") && v.endsWith("'")) {
      return v.slice(1, -1).replace(/''/g, "'");
    }
    return v;
  };

  return {
    body: body.trim(),
    source: field("source"),
    sender: field("sender"),
    senderName: unquote(field("sender_name")),
    senderID: unquote(field("sender_id")),
    caller: field("caller_session_key"),
    media: unquote(field("media")),
    mediaPreview: unquote(field("media_preview")),
    type: field("type"),
  };
}

// --- media attachments ---

export type MediaRef = {
  // Basename served at /api/media/{name}.
  name: string;
  kind: "image" | "audio" | "file";
};

function mediaKindFromMime(mime: string): MediaRef["kind"] {
  if (mime.startsWith("image/")) return "image";
  if (mime.startsWith("audio/")) return "audio";
  return "file";
}

function mediaKindFromKey(key: string): MediaRef["kind"] {
  if (key === "image_path") return "image";
  if (key === "audio_path" || key === "voice_path") return "audio";
  return "file";
}

// extractMediaRefs collects the message's attachments from both carriers:
// native media markers ("<<media:image/jpeg:/abs/path>>") and the wake
// frontmatter `media` summary ("[Media: photo] image_path: /abs/path …",
// possibly several folded with " | "). Deduped by basename — a vision-capable
// model gets the same file in both places.
export function extractMediaRefs(
  markers: string[] | undefined,
  mediaField: string | undefined,
): MediaRef[] {
  const out: MediaRef[] = [];
  const seen = new Set<string>();
  const push = (path: string, kind: MediaRef["kind"]) => {
    const name = path.split("/").pop() ?? "";
    if (name === "" || seen.has(name)) return;
    seen.add(name);
    out.push({ name, kind });
  };
  for (const marker of markers ?? []) {
    const m = marker.match(/^<<media:([^:]+):(.+)>>$/);
    if (m) push(m[2], mediaKindFromMime(m[1]));
  }
  if (mediaField) {
    for (const m of mediaField.matchAll(
      /(image_path|audio_path|voice_path|file_path|document_path):\s*([^\s|]+)/g,
    )) {
      push(m[2], mediaKindFromKey(m[1]));
    }
  }
  return out;
}

export function mediaURL(name: string): string {
  return `/api/media/${encodeURIComponent(name)}`;
}

// uploadMedia POSTs a file's raw bytes to /api/media and returns the basename
// the server stored it under. That name is then attached to the next WS
// "message" frame so the backend can build a media_summary for it. Cookie auth
// rides the same-origin request automatically.
export async function uploadMedia(file: File): Promise<{ name: string }> {
  const res = await fetch("/api/media", {
    method: "POST",
    headers: { "Content-Type": file.type || "application/octet-stream" },
    body: file,
  });
  if (!res.ok) {
    throw new Error(`upload failed (${res.status}): ${await res.text()}`);
  }
  return res.json();
}

// fetchQuote asks the daemon to condense text into ONE line of markdown quote,
// leading "> " marker included, for a reply. The whole line comes back ready to
// use: the client never builds or parses quote syntax, which is what keeps the
// generator swappable — replacing it is a server-side change only.
//
// Errors are thrown, never smoothed over: there is no client-side fallback
// quote, and a mangled one would be worse than telling the user it failed.
export async function fetchQuote(
  sessionKey: string,
  text: string,
): Promise<string> {
  const res = await fetch("/api/quote", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ session_id: sessionKey, text }),
  });
  if (!res.ok) {
    throw new Error((await res.text()).trim() || `quote failed (${res.status})`);
  }
  const data = await res.json();
  const quote = typeof data?.quote === "string" ? data.quote.trim() : "";
  if (!quote) throw new Error("quote came back empty");
  return quote;
}

// --- pins ({sessionDir}/pins/*.md) ---

export type PinEntry = {
  // File name inside the session's pins directory; the id everywhere else.
  name: string;
  // Frontmatter `title`, or the file name when the pin has none.
  title: string;
  summary?: string;
  size: number;
  modified: string;
};

export type PinDetail = PinEntry & { content: string };

// createPin asks the daemon to file a message into the session's pins. The
// answer is an acknowledgement, not a result: the write happens in an agentic
// turn afterwards, so a freshly pinned message shows up in the list a little
// later (which is what the panel's polling is for).
export async function createPin(
  sessionKey: string,
  text: string,
): Promise<void> {
  const res = await fetch("/api/pin", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ session_id: sessionKey, text }),
  });
  if (!res.ok) {
    throw new Error((await res.text()).trim() || `pin failed (${res.status})`);
  }
}

export async function fetchPins(sessionKey: string): Promise<PinEntry[]> {
  const res = await fetch(
    `/api/pins?session_id=${encodeURIComponent(sessionKey)}`,
  );
  // 404 = daemon predates the endpoint; an empty collection reads the same to
  // the user and beats an error banner on every poll.
  if (res.status === 404) return [];
  if (!res.ok) throw new Error(`GET /api/pins: ${res.status}`);
  const data = await res.json();
  return Array.isArray(data?.pins) ? (data.pins as PinEntry[]) : [];
}

export async function fetchPin(
  sessionKey: string,
  name: string,
): Promise<PinDetail> {
  const res = await fetch(
    `/api/pins?session_id=${encodeURIComponent(sessionKey)}&name=${encodeURIComponent(name)}`,
  );
  if (!res.ok) {
    throw new Error(
      (await res.text()).trim() || `GET /api/pins/${name}: ${res.status}`,
    );
  }
  return res.json();
}

export async function deletePin(
  sessionKey: string,
  name: string,
): Promise<void> {
  const res = await fetch(
    `/api/pins?session_id=${encodeURIComponent(sessionKey)}&name=${encodeURIComponent(name)}`,
    { method: "DELETE" },
  );
  if (!res.ok) {
    throw new Error(
      (await res.text()).trim() || `delete pin failed (${res.status})`,
    );
  }
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
