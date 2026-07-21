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
import { NagobotSocket, type SocketStatus, type StreamFrame } from "@/lib/ws";

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
  // Live-streaming element still in flight (tool executing, thinking growing).
  running?: boolean;
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
  running?: boolean;
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

// dispatchSendsToMessages renders the user-visible side of a dispatch call:
// each successfully delivered to=user / to=caller:user send becomes an
// assistant chat bubble (that text reached a human). The dispatch tool card
// itself is rendered separately like any other tool — this only adds the
// bubbles on top. Failed calls add nothing: a rejected call (the model then
// retries, so rendering it would duplicate the retry's sends) is skipped
// entirely, and a partial failure skips the sends its "- send #N" lines
// name. Other targets (session/subagent/...) get no extra rendering — the
// tool card already shows them.
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
    const send = s as { to?: string; body?: string; message?: string };
    const body = (send.body ?? send.message ?? "").trim();
    if (body === "") continue;
    const to = send.to ?? "";
    if (to !== "user" && to !== "caller:user") continue;

    out.push({
      id: localID("disp"),
      role: "assistant",
      text: body,
      createdAt,
      caller: to === "caller:user" ? lastCaller || undefined : undefined,
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
    // Trimmed heartbeat/dream turns are background noise the bot itself no
    // longer sees: Tier-1 marks the whole turn (heartbeat_trim on assistant/
    // tool messages, a "[heartbeat at …]" marker on the wake). Hide them
    // here — the raw-data dialog still shows everything.
    if (m.heartbeat_trim) continue;
    if (
      m.role === "user" &&
      (m.compressed?.startsWith("[heartbeat ") ||
        m.compressed?.startsWith("[progress "))
    )
      continue;
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
        // dispatch additionally surfaces its delivered user-facing sends as
        // chat bubbles — the human actually received that text.
        if (name === "dispatch") {
          out.push(
            ...dispatchSendsToMessages(
              tc,
              createdAt,
              lastCaller,
              result?.content,
            ),
          );
        }
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
  // Deduplicate ids defensively: assistant-ui's message repository THROWS on
  // a repeated id, and with no error boundary that white-screens the whole
  // app. Session data is normally unique, but one dirty line must not take
  // the page down.
  const seen = new Set<string>();
  return out.slice(-historyLimit).map((m) => {
    let id = m.id;
    while (seen.has(id)) id = `${id}+`;
    seen.add(id);
    return id === m.id ? m : { ...m, id };
  });
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
  if (m.running) custom.running = true;
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

    // Live turn state (per socket, so a session switch resets it): ids of the
    // in-flight thinking card and text bubble, plus tool cards by call id.
    // All message updates are immutable — assistant-ui caches conversions by
    // object identity, so a mutated-in-place message would never re-render.
    const live = {
      active: false,
      thinkingId: null as string | null,
      textId: null as string | null,
      tools: new Map<string, string>(),
    };

    let cancelled = false;
    // isMe: with a known sender_id, match against the viewer's identity keys.
    // Without one (data predating sender_id, or auth disabled), fall back to
    // the shape of the data: single-user messages carry no sender_name and
    // read as the viewer's own; named group-chat speakers stay "others".
    const isMe: IsMeFn = (senderID, senderName) => {
      if (senderID) return meKeysRef.current.has(senderID);
      return !senderName;
    };

    // resync re-reads session.jsonl and REPLACES the message list. Messages
    // delivered while this page was disconnected (mobile OS froze the PWA,
    // server fell back to Web Push) exist only on disk — the reconnected
    // socket never replays them, so without this the page resumes showing
    // its pre-freeze state forever. Live stream ids are reset; an in-flight
    // turn rebuilds its bubbles from the next snapshot-carrying frame.
    const resync = () => {
      fetchSession(sessionKey)
        .then((detail) => {
          if (cancelled) return;
          live.thinkingId = null;
          live.textId = null;
          live.tools.clear();
          // A turn_end missed while disconnected would leave the stop button
          // stuck until the failsafe timeout. Clear the running state here;
          // a turn genuinely still in flight re-arms it on its next frame.
          live.active = false;
          stopRunning();
          setMessages(sessionToChatMessages(detail.messages, isMe));
        })
        .catch(() => {
          // Keep whatever is on screen; the next reconnect/visible retries.
        });
    };

    // Resync on every reconnect after the first successful open, and whenever
    // the tab returns to the foreground (an iOS resume often revives the page
    // without any socket close event ever firing).
    let wasOpen = false;
    sock.onStatus = (s) => {
      setStatus(s);
      if (s === "open") {
        if (wasOpen) resync();
        wasOpen = true;
      }
    };
    const onVisibility = () => {
      if (document.visibilityState === "visible") resync();
    };
    document.addEventListener("visibilitychange", onVisibility);

    const replaceMsg = (id: string, patch: Partial<ChatMessage>) => {
      setMessages((prev) =>
        prev.map((m) => (m.id === id ? { ...m, ...patch } : m)),
      );
    };

    sock.onStream = (ev: StreamFrame) => {
      live.active = true;
      startRunning(); // a turn is visibly in flight; each event re-arms the timeout
      switch (ev.kind) {
        case "thinking": {
          const snap = ev.snapshot ?? "";
          if (snap === "") break;
          if (live.thinkingId) {
            replaceMsg(live.thinkingId, { resultText: snap });
          } else {
            const id = localID("live-think");
            live.thinkingId = id;
            setMessages((prev) => [
              ...prev,
              {
                id,
                role: "assistant",
                text: "",
                kind: "tool",
                toolName: "thinking",
                resultText: snap,
                running: true,
                createdAt: new Date(),
              },
            ]);
          }
          break;
        }
        case "text": {
          const snap = ev.snapshot ?? "";
          if (snap === "") break;
          if (live.textId) {
            replaceMsg(live.textId, { text: snap });
          } else {
            const id = localID("live-text");
            live.textId = id;
            setMessages((prev) => [
              ...prev,
              { id, role: "assistant", text: snap, createdAt: new Date() },
            ]);
          }
          break;
        }
        case "tool_call": {
          if (!ev.tool) break;
          const id = localID("live-tool");
          if (ev.tool_call_id) live.tools.set(ev.tool_call_id, id);
          setMessages((prev) => [
            ...prev,
            {
              id,
              role: "assistant",
              text: "",
              kind: "tool",
              toolName: ev.tool,
              argsText: prettyArgs(ev.args),
              running: true,
              createdAt: new Date(),
            },
          ]);
          break;
        }
        case "tool_result": {
          const id = ev.tool_call_id
            ? live.tools.get(ev.tool_call_id)
            : undefined;
          if (!id) break;
          if (ev.tool_call_id) live.tools.delete(ev.tool_call_id);
          replaceMsg(id, { resultText: ev.args ?? "", running: false });
          break;
        }
        case "round_end": {
          // The LLM call finished: close the thinking card. The text bubble
          // stays live — the authoritative "response" frame replaces it.
          if (live.thinkingId) {
            replaceMsg(live.thinkingId, { running: false });
            live.thinkingId = null;
          }
          break;
        }
        case "turn_end": {
          live.active = false;
          live.textId = null;
          if (live.thinkingId) {
            replaceMsg(live.thinkingId, { running: false });
            live.thinkingId = null;
          }
          // Any tool card without a result stays visible but stops pulsing.
          const stale = [...live.tools.values()];
          live.tools.clear();
          if (stale.length > 0) {
            setMessages((prev) =>
              prev.map((m) =>
                stale.includes(m.id) ? { ...m, running: false } : m,
              ),
            );
          }
          stopRunning();
          break;
        }
      }
    };

    sock.onResponse = (text) => {
      // Streamed turns end on turn_end; a lone response (non-streaming
      // provider or older daemon) still closes the spinner.
      if (!live.active) stopRunning();
      // Desktop nicety: tab open but hidden → system notification. Web Push
      // (sw.js) covers the no-tab case; this covers the backgrounded tab,
      // where the server sees a connected client and sends no push.
      if (
        document.hidden &&
        typeof Notification !== "undefined" &&
        Notification.permission === "granted"
      ) {
        try {
          new Notification(`nagobot · ${sessionKey}`, {
            body: text.length > 140 ? text.slice(0, 140) + "…" : text,
            tag: sessionKey,
          });
        } catch {
          // Some platforms (Android Chrome) only allow SW-shown notifications.
        }
      }
      // Replace the live streamed bubble in place (authoritative content for
      // the same round); otherwise append. The replacement KEEPS the live
      // bubble's id — swapping in a fresh id at the same spot makes
      // assistant-ui's store treat old and new as sibling branches of the
      // same parent and render a "< 2/2 >" branch picker.
      const liveId = live.textId;
      live.textId = null;
      const final: ChatMessage = {
        id: liveId ?? localID("resp"),
        role: "assistant",
        text,
        createdAt: new Date(),
      };
      setMessages((prev) => {
        if (liveId && prev.some((m) => m.id === liveId)) {
          return prev.map((m) => (m.id === liveId ? final : m));
        }
        return [...prev, final];
      });
    };
    // Another person watching this session spoke — show their bubble right
    // away (their page rendered it locally; ours would otherwise only see
    // the reply stream). The next resync replaces it with the persisted form.
    sock.onPeerMessage = (text, sender) => {
      setMessages((prev) => [
        ...prev,
        {
          id: localID("peer"),
          role: "user",
          text,
          createdAt: new Date(),
          senderName: sender || undefined,
          isMe: false,
        },
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

    setHistoryError(null);
    setHistoryLoading(true);
    // setHistoryLoading(false) lives in the SAME callback as setMessages —
    // not a .finally — so both land in one React commit. Split across two
    // promise callbacks they can commit separately, and the in-between frame
    // (loading done, messages still empty) flashes the welcome screen.
    fetchSession(sessionKey)
      .then((detail) => {
        if (cancelled) return;
        // Prepend history in front of whatever arrived live while the fetch
        // was in flight. Overwriting instead would orphan the stream state
        // machine's ids (live.textId etc. point at wiped messages), leaving
        // every later snapshot a no-op — the turn looks frozen after a
        // close-and-reopen mid-response.
        setMessages((prev) => [
          ...sessionToChatMessages(detail.messages, isMe),
          ...prev,
        ]);
        setHistoryLoading(false);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        // A brand-new session has no file yet; an empty thread is correct.
        if (!String(err).includes("404")) {
          setHistoryError(String(err));
        }
        setHistoryLoading(false);
      });

    return () => {
      cancelled = true;
      document.removeEventListener("visibilitychange", onVisibility);
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

  // takeOver reclaims the session after another page displaced this one
  // (status "replaced"): reconnect + rebind, which in turn bumps that page.
  const takeOver = useCallback(() => {
    socketRef.current?.resume();
  }, []);

  const runtime = useExternalStoreRuntime<ChatMessage>({
    messages,
    isRunning,
    convertMessage,
    onNew,
  });

  return {
    runtime,
    status,
    historyError,
    historyLoading,
    takeOver,
    // Known synchronously, unlike the runtime's internal thread state which
    // ingests external messages in a post-mount effect. The pane uses this to
    // suppress the welcome screen during that sync gap — otherwise a session
    // with history flashes "How can I help you today?" before the store
    // catches up.
    messageCount: messages.length,
  };
}
