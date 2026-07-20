import {
  useExternalStoreRuntime,
  type AppendMessage,
  type ThreadMessageLike,
} from "@assistant-ui/react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useAuth } from "@/components/auth-gate";
import {
  extractMediaRefs,
  fetchSession,
  parseWakePayload,
  splitSpeakerPrefix,
  type ApiMessage,
  type ApiToolCall,
  type MediaRef,
} from "@/lib/api";
import { NagobotSocket, type SocketStatus } from "@/lib/ws";

export type ChatMessage = {
  id: string;
  role: "user" | "assistant";
  text: string;
  createdAt?: Date;
  source?: string;
  // "chat"  — a conversation bubble (human or assistant speech)
  // "event" — a system frame (wake payloads, cross-session traffic, errors)
  // "tool"  — a single tool invocation, rendered as a collapsible card
  kind?: "chat" | "event" | "tool";
  // Human speaker display name (multi-user channels / web usernames).
  senderName?: string;
  // Whether this user message was sent by the logged-in viewer ("me" aligns
  // right, other humans align left).
  isMe?: boolean;
  // Which session sent us this message (incoming cross-session traffic).
  caller?: string;
  // Where this session sent a message (outgoing dispatch).
  target?: string;
  // kind:"tool" payload.
  toolName?: string;
  argsText?: string;
  resultText?: string;
  // Attachments (served via /api/media/{name}).
  media?: MediaRef[];
  // Upfront media description / transcription from the wake frontmatter.
  mediaPreview?: string;
  // Tier-1 compression replaced this content with a shorter version.
  compressed?: boolean;
};

// MessageMeta is the metadata.custom payload handed to the thread components.
export type MessageMeta = {
  kind?: "event" | "tool";
  source?: string;
  caller?: string;
  target?: string;
  senderName?: string;
  isMe?: boolean;
  toolName?: string;
  argsText?: string;
  resultText?: string;
  media?: MediaRef[];
  mediaPreview?: string;
  compressed?: boolean;
};

// Cap rendered history: the Thread view is not virtualized, and old sessions
// can hold thousands of entries.
const historyLimit = 300;

// A turn with no response frame (e.g. a silent dispatch({}) end) would leave
// the spinner on forever without this.
const runningTimeoutMs = 180_000;

let nextLocalID = 0;
function localID(prefix: string): string {
  nextLocalID += 1;
  return `${prefix}-${nextLocalID}`;
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

// IsMeFn decides whether a user message belongs to the logged-in viewer.
export type IsMeFn = (
  senderID: string | undefined,
  senderName: string | undefined,
) => boolean;

// dispatchSendsToMessages parses a dispatch tool call's sends into messages:
// to=user / to=caller:user deliveries become assistant chat bubbles (that
// text reached the user), everything else becomes an outgoing event card
// with its target. lastCaller resolves the "caller:*" targets to the session
// that most recently woke us. Returns [] for dispatch({}) (silent turn end).
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

// prettyArgs re-serializes a tool call's JSON arguments for display —
// multi-line, unescaped — falling back to the raw string on parse failure.
function prettyArgs(raw: string | undefined): string {
  const s = (raw ?? "").trim();
  if (s === "" || s === "{}") return "";
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

// sessionToChatMessages maps session.jsonl — the sole UI history source —
// into the message store. Every entry surfaces somewhere:
//   - assistant content and dispatch(to=user | caller:user) sends → bubbles
//     (the only two outlets that ever reach a human)
//   - other tool calls (with their paired results) → collapsible tool cards
//   - system-sender wakes (cron / heartbeat / compression / cross-session)
//     → subdued event cards, never mistaken for human speech
//   - human wakes → user bubbles, "me" resolved via sender_id
export function sessionToChatMessages(
  api: ApiMessage[],
  isMe: IsMeFn,
): ChatMessage[] {
  const out: ChatMessage[] = [];
  let lastCaller = "";
  // Tool results, keyed by the tool call they answer — dispatch uses them to
  // drop rejected calls (the model retries those, so rendering both would
  // duplicate every send); tool cards show them as the call's output.
  const toolResults = new Map<string, ApiMessage>();
  for (const m of api) {
    if (m.role === "tool" && m.tool_call_id) {
      toolResults.set(m.tool_call_id, m);
    }
  }

  for (const m of api) {
    if (m.role !== "user" && m.role !== "assistant") continue;
    const createdAt = m.timestamp ? new Date(m.timestamp) : undefined;
    const raw = m.content ?? "";
    const compressed = Boolean(m.compressed);

    if (m.role === "assistant") {
      if (m.reasoning_content && m.reasoning_content.trim() !== "") {
        out.push({
          id: localID("think"),
          role: "assistant",
          text: "",
          createdAt,
          kind: "tool",
          toolName: "thinking",
          resultText: m.reasoning_content,
        });
      }
      if (raw.trim() !== "") {
        out.push({
          id: m.id || localID("hist"),
          role: "assistant",
          text: raw,
          createdAt,
          compressed,
        });
      }
      for (const tc of m.tool_calls ?? []) {
        const name = tc.function?.name;
        if (!name) continue;
        const result = tc.id ? toolResults.get(tc.id) : undefined;
        if (name === "dispatch") {
          out.push(
            ...dispatchSendsToMessages(
              tc,
              createdAt,
              lastCaller,
              result?.content,
            ),
          );
          continue;
        }
        out.push({
          id: tc.id || localID("tool"),
          role: "assistant",
          text: "",
          createdAt,
          kind: "tool",
          toolName: name,
          argsText: prettyArgs(tc.function?.arguments),
          resultText: result?.content ?? "",
          compressed: Boolean(result?.compressed),
        });
      }
      continue;
    }

    // role === "user": a wake payload — human speech, or a system frame.
    const wake = parseWakePayload(raw);
    const media = extractMediaRefs(m.media, wake.media);
    if (wake.body === "" && media.length === 0) continue;
    let text = wake.body;
    const source = wake.source ?? m.source;
    let kind: ChatMessage["kind"] = "chat";
    let senderName: string | undefined;
    let caller: string | undefined;
    let me: boolean | undefined;
    if (wake.caller) lastCaller = wake.caller;
    if (wake.sender && wake.sender !== "user") {
      kind = "event";
      caller = wake.caller;
    } else {
      const s = stripSpeaker(text, wake.senderName);
      senderName = s.name || undefined;
      text = s.text;
      me = isMe(wake.senderID, senderName);
    }

    out.push({
      id: m.id || localID("hist"),
      role: "user",
      text,
      createdAt,
      source,
      kind,
      senderName,
      isMe: me,
      caller,
      media: media.length > 0 ? media : undefined,
      mediaPreview: wake.mediaPreview,
      compressed,
    });
  }
  return out.slice(-historyLimit);
}

const convertMessage = (m: ChatMessage): ThreadMessageLike => {
  const custom: MessageMeta = {};
  if (m.kind === "event" || m.kind === "tool") custom.kind = m.kind;
  if (m.source) custom.source = m.source;
  if (m.caller) custom.caller = m.caller;
  if (m.target) custom.target = m.target;
  if (m.senderName) custom.senderName = m.senderName;
  if (m.isMe !== undefined) custom.isMe = m.isMe;
  if (m.toolName) custom.toolName = m.toolName;
  if (m.argsText) custom.argsText = m.argsText;
  if (m.resultText) custom.resultText = m.resultText;
  if (m.media) custom.media = m.media;
  if (m.mediaPreview) custom.mediaPreview = m.mediaPreview;
  if (m.compressed) custom.compressed = true;
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

  // The viewer's identity keys: their person ID plus every channel identity
  // bound to them. A history message is "mine" when its sender_id matches.
  const { me } = useAuth();
  const meKeys = useMemo(() => {
    const keys = new Set<string>();
    if (me?.person_id) keys.add(`person:${me.person_id}`);
    for (const id of me?.identities ?? []) keys.add(id);
    return keys;
  }, [me]);
  const meKeysRef = useRef(meKeys);
  meKeysRef.current = meKeys;

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
    // isMe: with a known sender_id, match against the viewer's identity keys.
    // Without one (data predating sender_id, or auth disabled), fall back to
    // the shape of the data: single-user messages carry no sender_name and
    // read as the viewer's own; named group-chat speakers stay "others".
    const isMe: IsMeFn = (senderID, senderName) => {
      if (senderID) return meKeysRef.current.has(senderID);
      return !senderName;
    };
    fetchSession(sessionKey)
      .then((detail) => {
        if (!cancelled) {
          setMessages(sessionToChatMessages(detail.messages, isMe));
        }
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
        {
          id: localID("user"),
          role: "user",
          text,
          createdAt: new Date(),
          isMe: true,
        },
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
