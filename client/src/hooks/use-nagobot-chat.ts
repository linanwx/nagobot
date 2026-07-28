import {
  useExternalStoreRuntime,
  type AppendMessage,
  type ExternalThreadQueueAdapter,
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
  type MediaRef,
} from "@/lib/api";
import {
  clientMessageID,
  NagobotSocket,
  type SocketStatus,
  type StreamFrame,
} from "@/lib/ws";
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
  // speech rides in the parts. Plain bubbles (errors, peer echoes) keep
  // using `text`.
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
//
// The window's top edge is pinned to a MESSAGE ID, not to a count, and this is
// load-bearing: a count-based trailing window (`slice(-limit)`) slides forward
// on every append, dropping the oldest rendered entry. assistant-ui renders
// messages under INDEX-based keys, so a slide rewrites the content of every
// rendered slot and changes the content height ABOVE the viewport. Chrome
// papers over that with CSS scroll anchoring; WebKit has no `overflow-anchor`
// at all (`CSS.supports("overflow-anchor", "auto")` is false there), so on iOS
// every send threw the viewport thousands of pixels back into old history for
// a few hundred ms, until the top-anchor's pin scroll dragged it forward again
// (measured on a 425-entry session in WebKit at 390x844: the message sitting at
// the top edge jumped from -651px to +6368px on send; with the window pinned it
// never leaves the top edge). Pinning by id means the window only ever grows
// downward as messages arrive, and only "load earlier" moves its top edge.
//
// The size is a render budget, and it was measured rather than guessed. Opening
// a 1176-entry session (460 chat messages after the projection) on an M-series
// Mac took 860ms from click to a settled DOM, of which the fetch was 47ms and
// JSON.parse 5ms — everything else was React mounting the page. Widening by 160
// more messages cost a further 616ms, so the cost is linear at roughly 3ms per
// message with no virtualization to flatten it. At 300 that first paint was the
// single largest term in the whole load: larger than the network leg and an
// order of magnitude larger than the server, which answers a full history read
// in 25-67ms. 60 still fills more than a screenful, and the price is that a
// reader walking back through a long session clicks "load earlier" more often —
// which is cheap, because it moves an id rather than fetching anything (the
// whole session is already in memory).
const historyPageSize = 60;

// A turn with no response frame (e.g. a silent dispatch({}) end) would leave
// the spinner on forever without this.
const runningTimeoutMs = 180_000;

// PendingMessage is a message the daemon has (the WS frame is out) but has not
// written to session.jsonl yet — rendered as a composer chip, not a bubble.
// See ComposerQueueBar for why placement has to wait for the server.
type PendingMessage = {
  id: string;
  // What the chip shows: the typed text, without the folded-in quote line.
  prompt: string;
  // The wire form (quote included) — what to look for when the server's own
  // entry does not carry this chip's id (see entryPlacesPending).
  text: string;
  media?: MediaRef[];
  createdAt: Date;
};

// How far back a chip looks for itself in a full history read. The text
// fallback below is not id-exact, so an identical message from earlier in the
// conversation must not retire a chip that is still genuinely in flight.
const pendingHistoryWindow = 6;

// entryPlacesPending reports whether a persisted entry IS this pending message.
// Id equality is the normal case — the client minted the id and the server
// persists it verbatim. The text fallback exists for exactly one situation:
// tryMerge folds consecutive same-source wakes into ONE entry, which keeps only
// the first message's id, so the second chip has no entry of its own and would
// otherwise never retire.
function entryPlacesPending(entry: ApiMessage, item: PendingMessage): boolean {
  if (entry.role !== "user") return false;
  if (entry.id && entry.id === item.id) return true;
  const wake = parseWakePayload(entry.content ?? "");
  // A system-sender wake is never a human's queued message, however its body
  // reads.
  if (wake.sender && wake.sender !== "user") return false;
  if (item.text !== "") return wake.body.includes(item.text);
  const media = extractMediaRefs(entry.media, wake.media);
  return (item.media ?? []).every((want) =>
    media.some((got) => got.name === want.name),
  );
}

// historyPlaced runs entryPlacesPending over the tail of a full history read.
function historyPlaced(api: ApiMessage[], item: PendingMessage): boolean {
  return api
    .filter((m) => m.role === "user")
    .slice(-pendingHistoryWindow)
    .some((m) => entryPlacesPending(m, item));
}

let nextLocalID = 0;
function localID(prefix: string): string {
  nextLocalID += 1;
  return `${prefix}-${nextLocalID}`;
}

// quoteText pulls the composer's pending quote off an outgoing message. The
// composer stores it under metadata.custom.quote as {text, messageId}; only the
// text is used — the quote is a piece of markdown, not a link to a message.
function quoteText(message: AppendMessage): string {
  const custom = message.metadata?.custom as { quote?: { text?: unknown } } | undefined;
  const text = custom?.quote?.text;
  return typeof text === "string" ? text.trim() : "";
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
//     the live-stream shape so layout and rendering are identical. Nothing
//     splits a turn any more: speaking to the human is plain content, not a
//     dispatch, so an assistant turn's tool chain always groups as one card.
//   - system-sender wakes (cron / heartbeat / compression / cross-session)
//     → subdued event cards, never mistaken for human speech
//   - human wakes → user bubbles, "me" resolved via sender_id
//
// It is the ONLY projection: a live turn and a reloaded one take this same
// path, because the live stream now patches session.jsonl entries by id rather
// than building a parallel message of its own.
//
// liveID, when set, names the assistant entry currently being streamed. Its
// tool calls have not been answered YET (the result rides a later entry), so
// they render as running; a missing result anywhere else means the turn died
// before answering and renders as settled-with-no-output.
export function sessionToChatMessages(
  api: ApiMessage[],
  isMe: IsMeFn,
  liveID?: string | null,
): ChatMessage[] {
  const out: ChatMessage[] = [];
  // Tool results, keyed by the tool call they answer, so each tool part can
  // show its own output.
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
    //
    // `view=chat` means a HISTORY read no longer carries these at all, but this
    // check is not dead: live `message` frames are the stored entry verbatim and
    // never pass through that filter, so a heartbeat turn running right now
    // still arrives flagged. The same holds for reasoning_trimmed below.
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
        const pendingResult = !result && m.id !== undefined && m.id === liveID;
        turnParts(m, createdAt).push({
          type: "tool",
          callId: tc.id || localID("tool"),
          toolName: name,
          argsText: prettyArgs(tc.function?.arguments),
          resultText: pendingResult ? undefined : (result?.content ?? ""),
        });
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

// samePart / sameChatMessage compare two projections of the same message by
// value. Every string they touch comes from the same ApiMessage object when
// nothing changed, so the comparisons are reference hits and cost nothing.
function samePart(a: TurnPart, b: TurnPart): boolean {
  if (a.type !== b.type) return false;
  if (a.type === "tool" && b.type === "tool") {
    return (
      a.callId === b.callId &&
      a.toolName === b.toolName &&
      a.argsText === b.argsText &&
      a.resultText === b.resultText
    );
  }
  if (a.type === "tool" || b.type === "tool") return false;
  return a.text === b.text;
}

function sameChatMessage(a: ChatMessage, b: ChatMessage): boolean {
  if (
    a.role !== b.role ||
    a.text !== b.text ||
    a.kind !== b.kind ||
    a.source !== b.source ||
    a.senderName !== b.senderName ||
    a.isMe !== b.isMe ||
    a.caller !== b.caller ||
    a.target !== b.target ||
    a.compressed !== b.compressed ||
    a.createdAt?.getTime() !== b.createdAt?.getTime()
  )
    return false;
  const am = a.media ?? [];
  const bm = b.media ?? [];
  if (am.length !== bm.length) return false;
  if (am.some((m, i) => m.name !== bm[i]!.name || m.kind !== bm[i]!.kind))
    return false;
  const ap = a.parts;
  const bp = b.parts;
  if ((ap === undefined) !== (bp === undefined)) return false;
  if (ap && bp) {
    if (ap.length !== bp.length) return false;
    if (ap.some((p, i) => !samePart(p, bp[i]!))) return false;
  }
  return true;
}

// reuseIdentity carries the previous render's objects forward wherever the new
// projection is value-equal. assistant-ui keys its conversion and render caches
// on object identity, and a live turn re-derives the WHOLE list ~12 times a
// second — without this, every message in the rendered window would re-convert
// and re-render on every token, when only the streaming tail actually changed.
function reuseIdentity(
  cache: Map<string, ChatMessage>,
  next: ChatMessage[],
): ChatMessage[] {
  const out = next.map((m) => {
    const prev = cache.get(m.id);
    return prev && sameChatMessage(prev, m) ? prev : m;
  });
  cache.clear();
  for (const m of out) cache.set(m.id, m);
  return out;
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
  // session.jsonl, as the server has revealed it — the single authority for
  // which messages exist and in what order. A history read installs it and the
  // live `message` frames extend it; nothing else may insert an entry. Bubbles
  // are DERIVED from this (see `messages` below), so the live turn and a
  // reloaded one are the same data through the same projection.
  const [rawMessages, setRawMessages] = useState<ApiMessage[]>([]);
  // Id of the assistant entry currently streaming, from its message_start to
  // turn_end. Only used to decide whether an unanswered tool call is still
  // running.
  const [liveID, setLiveID] = useState<string | null>(null);
  // UI-only lines that are not conversation entries and never will be:
  // transport errors, and the user's own words when the socket was down so the
  // daemon never received them. They render after the derived list and are
  // deliberately never cleared — an error the user tabbed away from must still
  // be there when they come back.
  const [notices, setNotices] = useState<ChatMessage[]>([]);
  // Sent but not yet placed — see PendingMessage.
  const [pending, setPending] = useState<PendingMessage[]>([]);
  // Id of the oldest rendered entry — the window's top edge. null until the
  // first history settles. The pane is keyed by sessionKey, so a session switch
  // resets this to one page.
  const [windowStartID, setWindowStartID] = useState<string | null>(null);
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
  // isMe: with a known sender_id, match against the viewer's identity keys.
  // Without one (data predating sender_id, or auth disabled), fall back to the
  // shape of the data: single-user messages carry no sender_name and read as
  // the viewer's own; named group-chat speakers stay "others".
  const isMe = useCallback<IsMeFn>(
    (senderID, senderName) => {
      if (senderID) return meKeys.has(senderID);
      return !senderName;
    },
    [meKeys],
  );

  const socketRef = useRef<NagobotSocket | null>(null);
  const runningTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Assigned by the socket effect; the failsafe timeout below needs to reach
  // the effect's resync without being torn down with it.
  const resyncRef = useRef<() => void>(() => {});
  // Last render's projection, by message id — see reuseIdentity.
  const identityCache = useRef(new Map<string, ChatMessage>());

  // Held in refs so onNew keeps a stable identity: the pane is remounted per
  // session (key={sessionKey}), so "first send" is naturally per-session.
  const onFirstSendRef = useRef(onFirstSend);
  onFirstSendRef.current = onFirstSend;
  const firstSendNotified = useRef(false);

  // The pending queue is read synchronously (a WS frame handler decides on the
  // spot whether to promote chips) and rendered from state, so the ref is
  // authoritative and the state mirrors it. Every mutation goes through here
  // so the two can never disagree.
  const pendingRef = useRef<PendingMessage[]>([]);
  const updatePending = useCallback(
    (fn: (prev: PendingMessage[]) => PendingMessage[]) => {
      pendingRef.current = fn(pendingRef.current);
      setPending(pendingRef.current);
    },
    [],
  );

  // armFailsafe restarts the "we have something outstanding and have not heard
  // anything in a long time" watchdog. Outstanding means a queued chip OR a
  // running turn — the two are armed separately because a message can sit in
  // the daemon's queue for minutes while nothing is running.
  const armFailsafe = useCallback(() => {
    if (runningTimer.current) clearTimeout(runningTimer.current);
    runningTimer.current = setTimeout(() => {
      runningTimer.current = null;
      setIsRunning(false);
      // Nothing ever came back — every turn frame re-arms this timer, so
      // reaching it means the turn is dead. Ask the file who is right instead
      // of forcing the queued chips onto the screen: if the daemon did persist
      // them they become bubbles at their real position, and if it did not, a
      // chip that stays a chip is the honest report that the message never
      // landed.
      resyncRef.current();
    }, runningTimeoutMs);
  }, []);

  const stopRunning = useCallback(() => {
    setIsRunning(false);
    // A turn ending does not mean nothing is outstanding: messages sent into a
    // busy turn are still queued and their turn has not started yet.
    if (pendingRef.current.length > 0) {
      armFailsafe();
      return;
    }
    if (runningTimer.current) {
      clearTimeout(runningTimer.current);
      runningTimer.current = null;
    }
  }, [armFailsafe]);

  // isRunning means "a turn is actually running", NOT "the user just sent
  // something", and the distinction is load-bearing rather than pedantic:
  // assistant-ui's top-anchor engages on `isRunning && messages.at(-2) is user
  // && messages.at(-1) is assistant` (MessageRoot.useIsTopAnchorUser). During
  // the queue phase our own message is not in the list yet, so the last two
  // entries are the PREVIOUS turn's user+assistant — every condition matches
  // and the viewport scrolls to the previous turn. Measured: a 215px backward
  // scroll 91ms after Enter, before the message even existed. So only a turn
  // frame may set this.
  const startRunning = useCallback(() => {
    setIsRunning(true);
    armFailsafe();
  }, [armFailsafe]);

  // One socket per mounted chat pane. The pane is keyed by sessionKey, so a
  // session switch remounts and reconnects cleanly.
  useEffect(() => {
    const sock = new NagobotSocket();
    socketRef.current = sock;

    // Live turn state (per socket, so a session switch resets it). It is now
    // only "is a turn in flight" — everything the turn PRODUCES lands in
    // rawMessages, addressed by the ids the server hands out.
    const live = { active: false };

    let cancelled = false;

    // syncGen serializes the history fetches below: an older response must
    // never overwrite the list a newer one already installed.
    let syncGen = 0;

    // upsertRaw is the only way an entry enters the list. Replacing by id in
    // place rather than appending is what lets a streamed assistant message
    // become its persisted self without moving: its id was announced before
    // its first token, so every later frame — and the authoritative entry —
    // addresses the slot it already occupies.
    const upsertRaw = (entry: ApiMessage) => {
      setRawMessages((prev) => {
        const i = entry.id ? prev.findIndex((m) => m.id === entry.id) : -1;
        if (i < 0) return [...prev, entry];
        const next = prev.slice();
        next[i] = entry;
        return next;
      });
    };
    // patchRaw edits an entry the server has already announced. An unknown id
    // is dropped, never created: a delta for a message we were not told about
    // means we missed its message_start, and inventing a slot for it would put
    // it at the end instead of wherever the server actually placed it. The next
    // authoritative frame (or resync) repairs that.
    const patchRaw = (id: string, patch: Partial<ApiMessage>) => {
      setRawMessages((prev) => {
        const i = prev.findIndex((m) => m.id === id);
        if (i < 0) return prev;
        const next = prev.slice();
        next[i] = { ...next[i]!, ...patch };
        return next;
      });
    };
    const retirePlaced = (entry: ApiMessage) => {
      updatePending((prev) => prev.filter((p) => !entryPlacesPending(entry, p)));
    };

    // applyHistory installs a history read as the whole list and retires every
    // chip the server has now placed. Chips it does NOT find stay queued — the
    // server has not committed them yet.
    const applyHistory = (api: ApiMessage[]) => {
      setRawMessages(api);
      updatePending((prev) => prev.filter((p) => !historyPlaced(api, p)));
    };

    // resync re-reads session.jsonl and REPLACES the list. It is a fallback,
    // not a step of normal operation: messages delivered while this page was
    // disconnected (mobile OS froze the PWA, server fell back to Web Push)
    // exist only on disk, and the reconnected socket never replays them, so
    // without this the page resumes showing its pre-freeze state forever.
    const resync = () => {
      const gen = ++syncGen;
      fetchSession(sessionKey, "chat")
        .then((detail) => {
          if (cancelled || gen !== syncGen) return;
          // A turn_end missed while disconnected would leave the spinner stuck
          // until the failsafe timeout. Clear the running state here; a turn
          // genuinely still in flight re-arms it on its next frame.
          live.active = false;
          setLiveID(null);
          stopRunning();
          applyHistory(detail.messages);
        })
        .catch(() => {
          // Keep whatever is on screen; the next reconnect/visible retries.
        });
    };
    resyncRef.current = resync;

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

    sock.onStream = (ev: StreamFrame) => {
      // A turn is visibly in flight; each event re-arms the failsafe timeout.
      // `message` is excluded deliberately — it is the one frame that also
      // arrives outside a turn: post-turn hook injections are persisted, and
      // announced, AFTER turn_end, and re-arming there would leave the spinner
      // running on a turn that is over.
      if (ev.kind !== "message") {
        live.active = true;
        startRunning();
      }
      switch (ev.kind) {
        case "message": {
          // An entry has just been written to session.jsonl. This is the only
          // frame that adds a message, and it carries the entry itself — the
          // same shape a history read returns.
          if (!ev.message) break;
          upsertRaw(ev.message);
          retirePlaced(ev.message);
          break;
        }
        case "message_start": {
          // The id of the assistant message about to stream. Claiming its slot
          // now is what gives the deltas below something to patch; the
          // authoritative entry replaces this placeholder in place when the
          // round closes.
          if (!ev.message_id) break;
          setLiveID(ev.message_id);
          upsertRaw({
            role: "assistant",
            id: ev.message_id,
            content: "",
            timestamp: new Date().toISOString(),
          });
          break;
        }
        case "thinking": {
          if (!ev.message_id || !ev.snapshot) break;
          patchRaw(ev.message_id, { reasoning_content: ev.snapshot });
          break;
        }
        case "text": {
          if (!ev.message_id || !ev.snapshot) break;
          patchRaw(ev.message_id, { content: ev.snapshot });
          break;
        }
        case "turn_end": {
          // Spinners stop by themselves: once isRunning drops, the message
          // status goes complete and every part renders settled. Clearing
          // liveID settles the tool cards too — anything still unanswered was
          // abandoned, not running.
          live.active = false;
          setLiveID(null);
          stopRunning();
          break;
        }
        // tool_call / tool_result / round_end are deliberately not handled.
        // The authoritative `message` frames carry both the assistant's
        // tool_calls and each tool's result, and they arrive FIRST — acting on
        // the decorations too would make two writers for one piece of state,
        // which is the whole class of bug this design removes.
      }
    };

    sock.onResponse = (text) => {
      // The reply text itself already arrived as an authoritative `message`
      // frame; this frame is only a turn-completion signal now.
      //
      // Streamed turns end on turn_end; a lone response (non-streaming
      // provider) still closes the spinner.
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
    };

    // peer_message is deliberately not handled. Another viewer's message
    // reaches us as a `message` frame the moment the daemon writes it, at the
    // position the daemon chose; rendering the peer echo too would put a second
    // copy of it at a position we guessed. The cost is latency, not loss: a
    // peer speaking mid-turn shows up when the next turn write-ahead-persists
    // the queue.

    sock.onError = (message) => {
      stopRunning();
      setNotices((prev) => [
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
    // setHistoryLoading(false) lives in the SAME callback as setRawMessages —
    // not a .finally — so both land in one React commit. Split across two
    // promise callbacks they can commit separately, and the in-between frame
    // (loading done, messages still empty) flashes the welcome screen.
    fetchSession(sessionKey, "chat")
      .then((detail) => {
        if (cancelled) return;
        // Prepend history in front of whatever arrived live while the fetch was
        // in flight, skipping entries the live feed already delivered — the
        // read may well include them. Overwriting instead would drop the live
        // turn's placeholder, leaving its remaining deltas with nothing to
        // patch: the turn would look frozen after a close-and-reopen
        // mid-response.
        setRawMessages((prev) => {
          const seen = new Set(prev.map((m) => m.id).filter(Boolean));
          return [...detail.messages.filter((m) => !seen.has(m.id)), ...prev];
        });
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
  }, [sessionKey, startRunning, stopRunning, updatePending]);

  // enqueue is the send path. It puts the message on the wire immediately —
  // nothing is held back, the daemon accepts messages mid-turn — but renders
  // it as a composer chip rather than a bubble, because only the daemon knows
  // where in the conversation it will land (see ComposerQueueBar). The chip is
  // promoted to a real bubble the moment the server reveals that position.
  const enqueue = useCallback(
    (message: AppendMessage) => {
      const typed = message.content
        .filter((p) => p.type === "text")
        .map((p) => p.text)
        .join("\n")
        .trim();

      // A composer quote rides along as metadata rather than being merged into
      // the text — the composer never prepends it. Doing it here is the whole
      // integration: from this point on the quote is just the first line of an
      // ordinary markdown message, so it persists, reloads and renders with no
      // quote-aware code anywhere downstream.
      const quote = quoteText(message);
      const text = quote && typed !== "" ? `${quote}\n\n${typed}` : typed;

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

      // Name the message before sending it. The server persists this id
      // verbatim, so the chip below, the bubble it becomes when the server
      // reveals its placement, and the entry a later history read returns are
      // all the same id — the message is never re-keyed.
      const id = clientMessageID();
      const sent =
        socketRef.current?.send(
          id,
          text,
          media.map((m) => ({ name: m.name })),
        ) ?? false;
      if (!sent) {
        // Never queued: the socket is down, so the daemon has nothing, this
        // message will never appear in session.jsonl, and there is no
        // placement to wait for. Show the user's words and the failure
        // together as notices — they are not conversation entries.
        setNotices((prev) => [
          ...prev,
          {
            id: localID("user"),
            role: "user",
            text,
            createdAt: new Date(),
            isMe: true,
            media: media.length > 0 ? media : undefined,
          },
          {
            id: localID("err"),
            role: "assistant",
            text: i18n.t("chat.notConnected"),
            createdAt: new Date(),
          },
        ]);
        return;
      }

      updatePending((prev) => [
        ...prev,
        {
          id,
          prompt: typed || media.map((m) => m.name).join(", "),
          text,
          media: media.length > 0 ? media : undefined,
          createdAt: new Date(),
        },
      ]);
      // Deliberately NOT startRunning(): the message is queued, not running.
      // See the comment on startRunning — claiming a turn here anchors the
      // viewport to the PREVIOUS turn and scrolls backward. The watchdog is
      // armed on its own, because a queued message can wait minutes for a busy
      // thread and still deserves a dead-daemon check.
      armFailsafe();
      if (!firstSendNotified.current) {
        firstSendNotified.current = true;
        onFirstSendRef.current?.(sessionKey);
      }
    },
    [armFailsafe, sessionKey, updatePending],
  );

  // With a queue adapter present the runtime routes composer sends to
  // queue.enqueue and never calls onNew (external-store-thread-runtime-core's
  // append() returns right after enqueueing). It stays wired to the same path
  // so the two can never diverge if that ever changes.
  const onNew = useCallback(
    async (message: AppendMessage) => {
      enqueue(message);
    },
    [enqueue],
  );

  // takeOver reclaims the session after another page displaced this one
  // (status "replaced"): reconnect + rebind, which in turn bumps that page.
  const takeOver = useCallback(() => {
    socketRef.current?.resume();
  }, []);

  // The rendered conversation, derived from session.jsonl and nothing else.
  // Notices (transport errors, unsendable messages) trail it — they have no
  // place in the file and never will.
  const messages = useMemo(() => {
    const base = reuseIdentity(
      identityCache.current,
      sessionToChatMessages(rawMessages, isMe, liveID),
    );
    return notices.length > 0 ? [...base, ...notices] : base;
  }, [rawMessages, isMe, liveID, notices]);

  // Only the pinned window is handed to the (non-virtualized) thread view;
  // "load earlier" widens it a page at a time. Live updates target recent
  // messages, so they are always inside the window.
  const visibleMessages = useMemo(() => {
    if (windowStartID !== null) {
      const start = messages.findIndex((m) => m.id === windowStartID);
      if (start >= 0) return messages.slice(start);
    }
    // Before the pin is placed, and if the pinned entry ever disappears (Tier-2
    // compression rewrites ids), fall back to the trailing page — the effect
    // below re-pins on the next commit.
    return messages.length > historyPageSize
      ? messages.slice(-historyPageSize)
      : messages;
  }, [messages, windowStartID]);

  // Place the pin once the history has settled, and re-place it if the pinned
  // entry is gone. Deliberately NOT placed on the first message to arrive: a
  // live message can land before the history fetch resolves, and pinning there
  // would hide the whole history the fetch then prepends.
  useEffect(() => {
    if (historyLoading || messages.length === 0) return;
    if (windowStartID !== null && messages.some((m) => m.id === windowStartID))
      return;
    const start = Math.max(0, messages.length - historyPageSize);
    setWindowStartID(messages[start]!.id);
  }, [historyLoading, messages, windowStartID]);

  const loadEarlier = useCallback(() => {
    const start = Math.max(
      0,
      messages.length - visibleMessages.length - historyPageSize,
    );
    setWindowStartID(messages[start]?.id ?? null);
  }, [messages, visibleMessages.length]);

  // Supplying `queue` is what keeps the composer usable during a run: it flips
  // thread.capabilities.queue, which is the sole term letting useComposerSend
  // and ComposerInput's Enter handler through while isRunning. Nothing here
  // buffers — enqueue sends straight away; the queue is the pending-placement
  // display, and the runtime's own message-queue driver is deliberately not
  // used because it drops an item the moment it dispatches.
  const queue = useMemo<ExternalThreadQueueAdapter>(
    () => ({
      items: pending.map((p) => ({ id: p.id, prompt: p.prompt })),
      enqueue,
      // Unreachable, and no-ops by design: the chips render no steer/remove
      // control, and this runtime exposes no edit/reload/cancel for clear() to
      // answer. A sent message cannot be recalled — the daemon already has it.
      steer: () => {},
      remove: () => {},
      clear: () => {},
    }),
    [pending, enqueue],
  );

  const runtime = useExternalStoreRuntime<ChatMessage>({
    messages: visibleMessages,
    isRunning,
    convertMessage,
    onNew,
    queue,
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
    // catches up. Queued messages count: the first send on a fresh session is
    // a chip until the turn opens, and the welcome screen must not outlive it.
    messageCount: visibleMessages.length + pending.length,
    // Entries above the rendered window (0 = everything is shown).
    earlierCount: messages.length - visibleMessages.length,
    loadEarlier,
  };
}
