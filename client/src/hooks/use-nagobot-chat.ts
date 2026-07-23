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
import { imageAttachmentAdapter } from "@/lib/attachment-adapter";
import i18n from "@/i18n";

// TurnPart is one ordered element of an assistant turn. It maps 1:1 onto
// assistant-ui's native message parts (reasoning / text / tool-call), so a
// whole agentic turn — thinking, tool chain, speech — lives in ONE assistant
// message. That single-message shape is what the thread's turnAnchor="top"
// layout machinery assumes: the anchored user message stays second-to-last
// for the entire turn, so the reserved blank below it shrinks gradually
// instead of collapsing when a second message would have appeared.
export type TurnPart =
  | { type: "reasoning"; text: string }
  | { type: "text"; text: string }
  | {
      type: "tool";
      callId: string;
      toolName: string;
      argsText?: string;
      // undefined while the call is executing — assistant-ui derives the
      // running spinner from a missing result on a running message.
      resultText?: string;
    };

export type ChatMessage = {
  id: string;
  role: "user" | "assistant";
  text: string;
  createdAt?: Date;
  source?: string;
  // "chat"  — a conversation bubble (human or assistant speech)
  // "event" — a system frame (wake payloads, cross-session traffic, errors)
  kind?: "chat" | "event";
  // Assistant turn content as native parts. When set, `text` is unused —
  // speech rides in the parts. Plain bubbles (dispatch sends, errors, peer
  // echoes) keep using `text`.
  parts?: TurnPart[];
  // Human speaker display name (multi-user channels / web usernames).
  senderName?: string;
  // Whether this user message was sent by the logged-in viewer ("me" aligns
  // right, other humans align left).
  isMe?: boolean;
  // Which session sent us this message (incoming cross-session traffic).
  caller?: string;
  // Where this session sent a message (outgoing dispatch).
  target?: string;
  // Attachments (served via /api/media/{name}).
  media?: MediaRef[];
  // Tier-1 compression replaced this content with a shorter version.
  compressed?: boolean;
};

// MessageMeta is the metadata.custom payload handed to the thread components.
export type MessageMeta = {
  kind?: "event";
  source?: string;
  caller?: string;
  target?: string;
  senderName?: string;
  isMe?: boolean;
  media?: MediaRef[];
  compressed?: boolean;
};

// Rendered-history page size: the Thread view is not virtualized, and old
// sessions can hold thousands of entries, so the pane starts with the last
// page and a "load earlier" control extends the window one page at a time.
const historyPageSize = 300;

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

// Tool card bodies (args / result) get a hard cap so a huge read_file result
// can't take over the thread even when expanded.
const toolBodyMaxChars = 6000;

function capToolText(text: string): string {
  if (text.length <= toolBodyMaxChars) return text;
  return text.slice(0, toolBodyMaxChars) + `\n… (${text.length} chars total)`;
}

// sessionToChatMessages maps session.jsonl — the sole UI history source —
// into the message store. Every entry surfaces somewhere:
//   - each assistant turn (reasoning, tool chain, speech — everything
//     between wakes) → ONE assistant message with native parts, matching
//     the live-stream shape so layout and rendering are identical
//   - dispatch(to=user | caller:user) sends → extra chat bubbles (that text
//     actually reached a human)
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
  // duplicate every send); tool parts show them as the call's output.
  const toolResults = new Map<string, ApiMessage>();
  for (const m of api) {
    if (m.role === "tool" && m.tool_call_id) {
      toolResults.set(m.tool_call_id, m);
    }
  }

  // Consecutive assistant entries (the LLM iterations of one turn) accumulate
  // into a single parts-based message; anything else flushes it first.
  let turn: ChatMessage | null = null;
  const flushTurn = () => {
    if (turn && (turn.parts?.length ?? 0) > 0) out.push(turn);
    turn = null;
  };
  const turnParts = (m: ApiMessage, createdAt: Date | undefined): TurnPart[] => {
    turn ??= {
      id: m.id || localID("turn"),
      role: "assistant",
      text: "",
      createdAt,
      parts: [],
    };
    return (turn.parts ??= []);
  };

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
      // Trimmed reasoning (Tier-1 >2h send-time exclusion) is preserved in the
      // stored session but the bot itself no longer sees it — skip the part,
      // matching the heartbeat_trim hide above. The raw-data dialog still
      // shows it.
      if (
        !m.reasoning_trimmed &&
        m.reasoning_content &&
        m.reasoning_content.trim() !== ""
      ) {
        turnParts(m, createdAt).push({
          type: "reasoning",
          text: m.reasoning_content,
        });
      }
      if (raw.trim() !== "") {
        turnParts(m, createdAt).push({ type: "text", text: raw });
      }
      for (const tc of m.tool_calls ?? []) {
        const name = tc.function?.name;
        if (!name) continue;
        const result = tc.id ? toolResults.get(tc.id) : undefined;
        turnParts(m, createdAt).push({
          type: "tool",
          callId: tc.id || localID("tool"),
          toolName: name,
          argsText: prettyArgs(tc.function?.arguments),
          resultText: result?.content ?? "",
        });
        // dispatch additionally surfaces its delivered user-facing sends as
        // chat bubbles — the human actually received that text. The bubbles
        // are their own messages, so the turn splits chronologically here.
        if (name === "dispatch") {
          const bubbles = dispatchSendsToMessages(
            tc,
            createdAt,
            lastCaller,
            result?.content,
          );
          if (bubbles.length > 0) {
            flushTurn();
            out.push(...bubbles);
          }
        }
      }
      continue;
    }
    flushTurn();

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
      compressed,
    });
  }
  flushTurn();
  // Deduplicate ids defensively: assistant-ui's message repository THROWS on
  // a repeated id, and with no error boundary that white-screens the whole
  // app. Session data is normally unique, but one dirty line must not take
  // the page down.
  const seen = new Set<string>();
  return out.map((m) => {
    let id = m.id;
    while (seen.has(id)) id = `${id}+`;
    seen.add(id);
    return id === m.id ? m : { ...m, id };
  });
}

// toNativeContent maps TurnParts onto assistant-ui's native content parts.
// A missing tool result stays undefined — on a running message that is what
// drives the tool's spinner; empty text/reasoning parts are dropped by
// assistant-ui itself.
function toNativeContent(parts: TurnPart[]): ThreadMessageLike["content"] {
  return parts.map((p) => {
    if (p.type === "tool") {
      return {
        type: "tool-call" as const,
        toolCallId: p.callId,
        toolName: p.toolName,
        argsText: p.argsText ? capToolText(p.argsText) : "",
        result:
          p.resultText !== undefined
            ? capToolText(p.resultText) || "(no output)"
            : undefined,
      };
    }
    return { type: p.type, text: p.text };
  });
}

const convertMessage = (m: ChatMessage): ThreadMessageLike => {
  const custom: MessageMeta = {};
  if (m.kind === "event") custom.kind = m.kind;
  if (m.source) custom.source = m.source;
  if (m.caller) custom.caller = m.caller;
  if (m.target) custom.target = m.target;
  if (m.senderName) custom.senderName = m.senderName;
  if (m.isMe !== undefined) custom.isMe = m.isMe;
  if (m.media) custom.media = m.media;
  if (m.compressed) custom.compressed = true;
  return {
    id: m.id,
    role: m.role,
    content: m.parts
      ? toNativeContent(m.parts)
      : [{ type: "text", text: m.text }],
    createdAt: m.createdAt,
    metadata: Object.keys(custom).length > 0 ? { custom } : undefined,
  };
};

export function useNagobotChat(
  sessionKey: string,
  onFirstSend?: (key: string) => void,
) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  // How many trailing entries of the full mapped history are rendered. The
  // pane is keyed by sessionKey, so a session switch resets this to one page.
  const [renderLimit, setRenderLimit] = useState(historyPageSize);
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

  // Held in refs so onNew keeps a stable identity: the pane is remounted per
  // session (key={sessionKey}), so "first send" is naturally per-session.
  const onFirstSendRef = useRef(onFirstSend);
  onFirstSendRef.current = onFirstSend;
  const firstSendNotified = useRef(false);

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

    // Live turn state (per socket, so a session switch resets it). The whole
    // in-flight turn is ONE assistant message whose parts array grows as
    // frames arrive — thinking → reasoning part, tool_call/tool_result →
    // tool part, text → text part. Indices point into that parts array
    // (parts are append-only within a turn, so indices are stable).
    // All message updates are immutable — assistant-ui caches conversions by
    // object identity, so a mutated-in-place message would never re-render.
    const live = {
      active: false,
      turnId: null as string | null,
      partCount: 0,
      thinkingIdx: null as number | null,
      textIdx: null as number | null,
      tools: new Map<string, number>(),
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
    // its pre-freeze state forever. Live stream state is reset; an in-flight
    // turn rebuilds its message from the next snapshot-carrying frame.
    const resync = () => {
      fetchSession(sessionKey)
        .then((detail) => {
          if (cancelled) return;
          live.turnId = null;
          live.partCount = 0;
          live.thinkingIdx = null;
          live.textIdx = null;
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

    // ensureTurn opens the turn's single assistant message on the first
    // frame; every later frame edits its parts array in place (immutably).
    const ensureTurn = () => {
      if (live.turnId) return;
      const id = localID("live-turn");
      live.turnId = id;
      live.partCount = 0;
      live.thinkingIdx = null;
      live.textIdx = null;
      live.tools.clear();
      setMessages((prev) => [
        ...prev,
        { id, role: "assistant", text: "", parts: [], createdAt: new Date() },
      ]);
    };
    const appendPart = (part: TurnPart): number => {
      ensureTurn();
      const id = live.turnId;
      const idx = live.partCount++;
      setMessages((prev) =>
        prev.map((m) =>
          m.id === id ? { ...m, parts: [...(m.parts ?? []), part] } : m,
        ),
      );
      return idx;
    };
    const patchPart = (idx: number, patch: Partial<TurnPart>) => {
      const id = live.turnId;
      if (!id) return;
      setMessages((prev) =>
        prev.map((m) =>
          m.id === id
            ? {
                ...m,
                parts: (m.parts ?? []).map((p, i) =>
                  i === idx ? ({ ...p, ...patch } as TurnPart) : p,
                ),
              }
            : m,
        ),
      );
    };

    sock.onStream = (ev: StreamFrame) => {
      live.active = true;
      startRunning(); // a turn is visibly in flight; each event re-arms the timeout
      switch (ev.kind) {
        case "thinking": {
          const snap = ev.snapshot ?? "";
          if (snap === "") break;
          if (live.thinkingIdx !== null) {
            patchPart(live.thinkingIdx, { text: snap });
          } else {
            live.thinkingIdx = appendPart({ type: "reasoning", text: snap });
          }
          break;
        }
        case "text": {
          const snap = ev.snapshot ?? "";
          if (snap === "") break;
          if (live.textIdx !== null) {
            patchPart(live.textIdx, { text: snap });
          } else {
            live.textIdx = appendPart({ type: "text", text: snap });
          }
          break;
        }
        case "tool_call": {
          if (!ev.tool) break;
          const idx = appendPart({
            type: "tool",
            callId: ev.tool_call_id || localID("live-tool"),
            toolName: ev.tool,
            argsText: prettyArgs(ev.args),
          });
          if (ev.tool_call_id) live.tools.set(ev.tool_call_id, idx);
          break;
        }
        case "tool_result": {
          const idx = ev.tool_call_id
            ? live.tools.get(ev.tool_call_id)
            : undefined;
          if (idx === undefined) break;
          if (ev.tool_call_id) live.tools.delete(ev.tool_call_id);
          patchPart(idx, { resultText: ev.args ?? "" });
          break;
        }
        case "round_end": {
          // The LLM call finished: the next round's thinking/text open fresh
          // parts. The current text part stays addressable — the
          // authoritative "response" frame replaces its content.
          live.thinkingIdx = null;
          break;
        }
        case "turn_end": {
          // Spinners stop by themselves: once isRunning drops, the message
          // status goes complete and every part renders settled — pending
          // tool parts included. The turn message itself stays as-is until
          // the next resync replaces it with the persisted form.
          live.active = false;
          live.turnId = null;
          live.thinkingIdx = null;
          live.textIdx = null;
          live.tools.clear();
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
      // Replace the live streamed text part in place (authoritative content
      // for the same round); otherwise append — into the live turn when one
      // is open (keeps the turn a single message, which the anchored layout
      // depends on), as a standalone bubble when not.
      if (live.turnId) {
        if (live.textIdx !== null) {
          patchPart(live.textIdx, { text });
        } else {
          appendPart({ type: "text", text });
        }
        // The next round (if any) opens a fresh text part.
        live.textIdx = null;
      } else {
        setMessages((prev) => [
          ...prev,
          {
            id: localID("resp"),
            role: "assistant",
            text,
            createdAt: new Date(),
          },
        ]);
      }
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
      // Fold the error into the open live turn when there is one — a
      // standalone message after the turn would break the anchored layout.
      if (live.turnId) {
        appendPart({ type: "text", text: `⚠️ ${message}` });
      } else {
        setMessages((prev) => [
          ...prev,
          {
            id: localID("err"),
            role: "assistant",
            text: `⚠️ ${message}`,
            createdAt: new Date(),
          },
        ]);
      }
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

      // Attachments are uploaded by the adapter before onNew runs; each carries
      // an image part whose `image` is /api/media/{name}. Recover the basename
      // to forward on the WS frame and to echo the thumbnail optimistically.
      const media: MediaRef[] = [];
      for (const att of message.attachments ?? []) {
        for (const part of att.content ?? []) {
          if (part.type === "image" && typeof part.image === "string") {
            const base = part.image.split("/").pop();
            if (base) media.push({ name: decodeURIComponent(base), kind: "image" });
          }
        }
      }

      if (text === "" && media.length === 0) return;

      const sent =
        socketRef.current?.send(
          text,
          media.map((m) => ({ name: m.name })),
        ) ?? false;
      setMessages((prev) => [
        ...prev,
        {
          id: localID("user"),
          role: "user",
          text,
          createdAt: new Date(),
          isMe: true,
          media: media.length > 0 ? media : undefined,
        },
        ...(sent
          ? []
          : [
              {
                id: localID("err"),
                role: "assistant" as const,
                text: i18n.t("chat.notConnected"),
                createdAt: new Date(),
              },
            ]),
      ]);
      if (sent) {
        startRunning();
        if (!firstSendNotified.current) {
          firstSendNotified.current = true;
          onFirstSendRef.current?.(sessionKey);
        }
      }
    },
    [startRunning, sessionKey],
  );

  // takeOver reclaims the session after another page displaced this one
  // (status "replaced"): reconnect + rebind, which in turn bumps that page.
  const takeOver = useCallback(() => {
    socketRef.current?.resume();
  }, []);

  // Only the trailing window is handed to the (non-virtualized) thread view;
  // "load earlier" widens the window a page at a time. Live updates target
  // recent messages, so they are always inside the window.
  const visibleMessages = useMemo(
    () => (messages.length > renderLimit ? messages.slice(-renderLimit) : messages),
    [messages, renderLimit],
  );
  const loadEarlier = useCallback(() => {
    setRenderLimit((limit) => limit + historyPageSize);
  }, []);

  const runtime = useExternalStoreRuntime<ChatMessage>({
    messages: visibleMessages,
    isRunning,
    convertMessage,
    onNew,
    adapters: { attachments: imageAttachmentAdapter },
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
    messageCount: visibleMessages.length,
    // Entries above the rendered window (0 = everything is shown).
    earlierCount: messages.length - visibleMessages.length,
    loadEarlier,
  };
}
