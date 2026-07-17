import {
  useExternalStoreRuntime,
  type AppendMessage,
  type ThreadMessageLike,
} from "@assistant-ui/react";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchChat,
  fetchSession,
  parseWakePayload,
  splitSpeakerPrefix,
  type ApiMessage,
  type ApiToolCall,
  type ChatLogEntry,
} from "@/lib/api";
import { NagobotSocket, type SocketStatus } from "@/lib/ws";

export type ChatMessage = {
  id: string;
  role: "user" | "assistant";
  text: string;
  createdAt?: Date;
  source?: string;
  // "event" renders as a centered system notice instead of a chat bubble
  // (wake payloads, inter-session traffic, provider errors).
  kind?: "chat" | "event";
  // Human speaker display name (multi-user channels).
  senderName?: string;
  // Which session sent us this message (incoming cross-session traffic).
  caller?: string;
  // Where this session sent a message (outgoing dispatch).
  target?: string;
};

// MessageMeta is the metadata.custom payload handed to the thread components.
export type MessageMeta = {
  kind?: "event";
  source?: string;
  caller?: string;
  target?: string;
  senderName?: string;
};

// Cap rendered history: the Thread view is not virtualized, and old sessions
// can hold thousands of entries.
const historyLimit = 200;

// A turn with no response frame (e.g. a silent dispatch({}) end) would leave
// the spinner on forever without this.
const runningTimeoutMs = 180_000;

let nextLocalID = 0;
function localID(prefix: string): string {
  nextLocalID += 1;
  return `${prefix}-${nextLocalID}`;
}

// formatDuration renders a tool-activity span compactly: "42s", "3m", "1h05m".
function formatDuration(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h${String(m % 60).padStart(2, "0")}m`;
}

// Maintenance turns are conversation-invisible: heartbeat pulses and
// compression runs are daemon internals, not chat.
function isMaintenanceSource(source: string | undefined): boolean {
  if (!source) return false;
  return source.startsWith("heartbeat") || source === "compression";
}

// stripSpeaker resolves the speaker name for a user message: prefer the
// structured sender, fall back to the legacy "[Name]: " content prefix, and
// strip the prefix from the displayed text either way (the structured field
// and the prefix carry the same name).
function stripSpeaker(
  text: string,
  structured: string | undefined,
): { name: string; text: string } {
  const { name, rest } = splitSpeakerPrefix(text);
  if (structured) return { name: structured, text: name ? rest : text };
  return { name, text: name ? rest : text };
}

// chatLogToChatMessages maps chat.jsonl — the authoritative user-facing log —
// into the message store. Assistant entries that are wake-style system frames
// (e.g. provider errors) render as events.
export function chatLogToChatMessages(entries: ChatLogEntry[]): ChatMessage[] {
  const out: ChatMessage[] = [];
  for (const e of entries) {
    if (e.role !== "user" && e.role !== "assistant") continue;
    const raw = e.content ?? "";
    if (raw.trim() === "") continue;

    let text = raw;
    let kind: ChatMessage["kind"] = "chat";
    let source: string | undefined;
    let senderName: string | undefined;
    let caller: string | undefined;
    if (raw.startsWith("---\n")) {
      const wake = parseWakePayload(raw);
      if (wake.sender === "system") {
        kind = "event";
        source = wake.source ?? wake.type;
        caller = wake.caller;
        text = wake.body;
      }
    }
    if (kind === "chat") {
      if (e.role === "user") {
        const s = stripSpeaker(text, e.sender);
        senderName = s.name || undefined;
        text = s.text;
      } else if (e.sender) {
        // Assistant entry with an origin: a bot-initiated message driven by a
        // cron/session wake rather than a direct user turn.
        caller = e.sender;
      }
    }
    if (text === "") continue;

    out.push({
      id: localID("chat"),
      role: e.role,
      text,
      createdAt: e.ts ? new Date(e.ts) : undefined,
      source,
      kind,
      senderName,
      caller,
    });
  }
  return out.slice(-historyLimit);
}

// dispatchSendsToMessages parses a dispatch tool call's sends into messages:
// to=user deliveries become assistant chat bubbles (that text reached the
// user), everything else becomes an outgoing event card with its target.
// lastCaller resolves the "caller:*" targets to the session that most
// recently woke us. Returns [] for dispatch({}) (silent turn end).
//
// result is the paired tool-result content: a rejected call (the model then
// retries, so rendering it would duplicate the retry's sends) is skipped
// entirely, and a partial failure skips the sends its "- send #N" lines name.
function dispatchSendsToMessages(
  tc: ApiToolCall,
  createdAt: Date | undefined,
  lastCaller: string,
  result: string | undefined,
): ChatMessage[] {
  if (result?.includes("outcome: validation-error")) return [];
  const failed = new Set<number>();
  if (result?.includes("outcome: partial-failure")) {
    for (const m of result.matchAll(/- send #(\d+)/g)) {
      failed.add(Number(m[1]));
    }
  }

  let args: unknown;
  try {
    args = JSON.parse(tc.function?.arguments || "{}");
  } catch {
    return [];
  }
  if (typeof args !== "object" || args === null) return [];
  const sends = (args as { sends?: unknown }).sends;
  if (!Array.isArray(sends)) return [];

  const out: ChatMessage[] = [];
  for (const [index, s] of sends.entries()) {
    if (failed.has(index + 1)) continue;
    if (typeof s !== "object" || s === null) continue;
    const send = s as {
      to?: string;
      body?: string;
      message?: string;
      params?: Record<string, string>;
      // Legacy pre-params layout kept top-level addressing fields.
      session_key?: string;
      channel?: string;
      user_id?: string;
      agent?: string;
    };
    const body = (send.body ?? send.message ?? "").trim();
    if (body === "") continue;
    const p = send.params ?? {};
    const to = send.to ?? "";

    if (to === "user" || to === "caller:user") {
      out.push({
        id: localID("disp"),
        role: "assistant",
        text: body,
        createdAt,
        caller: to === "caller:user" ? lastCaller || undefined : undefined,
      });
      continue;
    }

    let target = "";
    if (to === "session") {
      target =
        p.session_key ||
        send.session_key ||
        [p.channel || send.channel, p.user_id || send.user_id]
          .filter(Boolean)
          .join(":");
    } else if (to === "caller:session") {
      target = lastCaller || "caller session";
    } else if (to === "subagent" || to === "fork") {
      const agent = p.agent || send.agent;
      target = agent ? `${to} (${agent})` : to;
    } else {
      target = to;
    }

    out.push({
      id: localID("disp"),
      role: "assistant",
      text: body,
      createdAt,
      kind: "event",
      source: "dispatch",
      target: target || undefined,
    });
  }
  return out;
}

// historyToChatMessages is the fallback for sessions without a chat.jsonl
// (e.g. cron runners): map session.jsonl, showing system-sender wakes as
// events rather than human speech, and dispatch tool calls as the outgoing
// messages they delivered.
export function historyToChatMessages(api: ApiMessage[]): ChatMessage[] {
  const out: ChatMessage[] = [];
  let lastCaller = "";
  // Tool results, keyed by the tool call they answer — used to drop dispatch
  // calls the daemon rejected (the model retries those, so rendering both
  // would duplicate every send). Result timestamps bound the end of a tool
  // activity span (the call's own timestamp is when it was issued).
  const toolResults = new Map<string, string>();
  const toolResultAt = new Map<string, Date>();
  for (const m of api) {
    if (m.role === "tool" && m.tool_call_id) {
      toolResults.set(m.tool_call_id, m.content ?? "");
      if (m.timestamp) toolResultAt.set(m.tool_call_id, new Date(m.timestamp));
    }
  }

  // Non-dispatch tool calls accumulate across consecutive tool-only turns and
  // flush as ONE compact activity line ("web_search ×5 · web_fetch ×2") before
  // the next visible message — the user perceives the work without the raw
  // call payloads.
  let pendingTools = new Map<string, number>();
  let pendingToolsAt: Date | undefined;
  let pendingToolsEnd: Date | undefined;
  const flushTools = () => {
    if (pendingTools.size === 0) return;
    let text =
      "⚙ " +
      [...pendingTools.entries()]
        .map(([name, count]) => (count > 1 ? `${name} ×${count}` : name))
        .join(" · ");
    // A visible span tells the reader this was a long-running stretch of
    // work, not an instant lookup; sub-5s spans are noise.
    if (pendingToolsAt && pendingToolsEnd) {
      const span = pendingToolsEnd.getTime() - pendingToolsAt.getTime();
      if (span >= 5_000) text += ` · ${formatDuration(span)}`;
    }
    out.push({
      id: localID("tools"),
      role: "assistant",
      text,
      createdAt: pendingToolsAt,
      kind: "event",
      source: "tools",
    });
    pendingTools = new Map();
    pendingToolsAt = undefined;
    pendingToolsEnd = undefined;
  };

  for (const m of api) {
    if (m.role !== "user" && m.role !== "assistant") continue;
    if (isMaintenanceSource(m.source)) continue;
    const createdAt = m.timestamp ? new Date(m.timestamp) : undefined;
    const raw = m.content ?? "";

    if (m.role === "assistant") {
      if (raw.trim() !== "") {
        flushTools();
        out.push({
          id: m.id || localID("hist"),
          role: "assistant",
          text: raw,
          createdAt,
        });
      }
      let hasDispatch = false;
      for (const tc of m.tool_calls ?? []) {
        const name = tc.function?.name;
        if (!name) continue;
        if (name === "dispatch") {
          hasDispatch = true;
          continue;
        }
        pendingTools.set(name, (pendingTools.get(name) ?? 0) + 1);
        pendingToolsAt ??= createdAt;
        const end = (tc.id && toolResultAt.get(tc.id)) || createdAt;
        if (end && (!pendingToolsEnd || end > pendingToolsEnd)) {
          pendingToolsEnd = end;
        }
      }
      if (hasDispatch) {
        // This turn's tools ran before its dispatch delivered.
        flushTools();
        for (const tc of m.tool_calls ?? []) {
          if (tc.function?.name !== "dispatch") continue;
          const result = tc.id ? toolResults.get(tc.id) : undefined;
          out.push(
            ...dispatchSendsToMessages(tc, createdAt, lastCaller, result),
          );
        }
      }
      continue;
    }

    flushTools();

    if (raw.trim() === "") continue;
    const wake = parseWakePayload(raw);
    if (wake.body === "") continue;
    let text = wake.body;
    const source = wake.source ?? m.source;
    let kind: ChatMessage["kind"] = "chat";
    let senderName: string | undefined;
    let caller: string | undefined;
    if (wake.caller) lastCaller = wake.caller;
    if (wake.sender && wake.sender !== "user") {
      kind = "event";
      caller = wake.caller;
    } else {
      const s = stripSpeaker(text, wake.senderName);
      senderName = s.name || undefined;
      text = s.text;
    }

    out.push({
      id: m.id || localID("hist"),
      role: "user",
      text,
      createdAt,
      source,
      kind,
      senderName,
      caller,
    });
  }
  flushTools();
  return out.slice(-historyLimit);
}

const convertMessage = (m: ChatMessage): ThreadMessageLike => {
  const custom: MessageMeta = {};
  if (m.kind === "event") custom.kind = "event";
  if (m.source) custom.source = m.source;
  if (m.caller) custom.caller = m.caller;
  if (m.target) custom.target = m.target;
  if (m.senderName) custom.senderName = m.senderName;
  return {
    id: m.id,
    role: m.role,
    content: [{ type: "text", text: m.text }],
    createdAt: m.createdAt,
    metadata: Object.keys(custom).length > 0 ? { custom } : undefined,
  };
};

export function useNagobotChat(sessionKey: string) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [isRunning, setIsRunning] = useState(false);
  const [status, setStatus] = useState<SocketStatus>("connecting");
  const [historyError, setHistoryError] = useState<string | null>(null);
  // True until the history fetch settles — the pane shows a spinner instead
  // of a misleading empty-thread welcome.
  const [historyLoading, setHistoryLoading] = useState(true);

  const socketRef = useRef<NagobotSocket | null>(null);
  const runningTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const stopRunning = useCallback(() => {
    setIsRunning(false);
    if (runningTimer.current) {
      clearTimeout(runningTimer.current);
      runningTimer.current = null;
    }
  }, []);

  const startRunning = useCallback(() => {
    setIsRunning(true);
    if (runningTimer.current) clearTimeout(runningTimer.current);
    runningTimer.current = setTimeout(() => {
      runningTimer.current = null;
      setIsRunning(false);
    }, runningTimeoutMs);
  }, []);

  // One socket per mounted chat pane. The pane is keyed by sessionKey, so a
  // session switch remounts and reconnects cleanly.
  useEffect(() => {
    const sock = new NagobotSocket();
    socketRef.current = sock;

    sock.onStatus = setStatus;
    sock.onResponse = (text) => {
      stopRunning();
      setMessages((prev) => [
        ...prev,
        { id: localID("resp"), role: "assistant", text, createdAt: new Date() },
      ]);
    };
    sock.onError = (message) => {
      stopRunning();
      setMessages((prev) => [
        ...prev,
        {
          id: localID("err"),
          role: "assistant",
          text: `⚠️ ${message}`,
          createdAt: new Date(),
        },
      ]);
    };

    sock.bind(sessionKey);
    sock.connect();

    let cancelled = false;
    setHistoryError(null);
    setHistoryLoading(true);
    // chat.jsonl first (the user-facing log, including dispatch-delivered
    // replies); sessions without one fall back to the session.jsonl mapping.
    fetchChat(sessionKey)
      .then(async (chat) => {
        if (chat !== null) return chatLogToChatMessages(chat);
        const detail = await fetchSession(sessionKey);
        return historyToChatMessages(detail.messages);
      })
      .then((msgs) => {
        if (!cancelled) setMessages(msgs);
      })
      .catch((err: unknown) => {
        // A brand-new session has no file yet; an empty thread is correct.
        if (!cancelled && !String(err).includes("404")) {
          setHistoryError(String(err));
        }
      })
      .finally(() => {
        if (!cancelled) setHistoryLoading(false);
      });

    return () => {
      cancelled = true;
      sock.close();
      socketRef.current = null;
    };
  }, [sessionKey, stopRunning]);

  const onNew = useCallback(
    async (message: AppendMessage) => {
      const text = message.content
        .filter((p) => p.type === "text")
        .map((p) => p.text)
        .join("\n")
        .trim();
      if (text === "") return;

      const sent = socketRef.current?.send(text) ?? false;
      setMessages((prev) => [
        ...prev,
        { id: localID("user"), role: "user", text, createdAt: new Date() },
        ...(sent
          ? []
          : [
              {
                id: localID("err"),
                role: "assistant" as const,
                text: "⚠️ Not connected to the daemon — message was not sent.",
                createdAt: new Date(),
              },
            ]),
      ]);
      if (sent) startRunning();
    },
    [startRunning],
  );

  const runtime = useExternalStoreRuntime<ChatMessage>({
    messages,
    isRunning,
    convertMessage,
    onNew,
  });

  return { runtime, status, historyError, historyLoading };
}
