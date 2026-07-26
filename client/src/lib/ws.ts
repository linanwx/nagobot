// WebSocket client for the nagobot web channel (/ws).
//
// Protocol (see channel/web.go):
//   client → server: {type: "bind", session_id} | {type: "message", id, text}
//   server → client: {type: "response", text} | {type: "bound", text}
//                  | {type: "error", error} | {type: "stream", kind, ...}
//                  | {type: "peer_message", text, sender}
//
// "stream" frames carry live turn activity. Two of them declare what EXISTS —
// `message` (an entry just written to session.jsonl) and `message_start` (the
// id an assistant message will carry, announced before its first token) — and
// the rest decorate a message the client has already been told about, addressed
// by `message_id`: thinking/text deltas (with the round-accumulated snapshot
// for self-healing), tool lifecycle, and round/turn boundaries.

import type { ApiMessage } from "@/lib/api";

// "replaced": another page bound this session and the server closed us with
// code 4001. Auto-reconnecting would kick that page right back, so the socket
// stays down until resume() is called explicitly.
export type SocketStatus = "connecting" | "open" | "closed" | "replaced";

const closeCodeReplaced = 4001;

export type StreamFrame = {
  kind:
    | "message"
    | "message_start"
    | "thinking"
    | "text"
    | "tool_call"
    | "tool_result"
    | "round_end"
    | "turn_end";
  delta?: string;
  snapshot?: string;
  tool?: string;
  tool_call_id?: string;
  args?: string;
  is_error?: boolean;
  seq?: number;
  // The message this frame belongs to: the entry's own id on message /
  // message_start, and the id of the assistant message the round is building on
  // every in-round frame.
  message_id?: string;
  // The persisted entry, on kind:"message" only — the same shape a history read
  // returns, so both paths feed one list.
  message?: ApiMessage;
};

// Media the client already uploaded via POST /api/media, referenced by the
// basename that endpoint returned.
export type OutboundMedia = { name: string; mime?: string };

type OutboundFrame =
  | { type: "bind"; session_id: string }
  | {
      type: "message";
      text: string;
      // The id this message will carry on disk. The server validates its shape
      // and persists it verbatim, so the caller holds one id from the moment
      // the message is typed until long after it is written to session.jsonl —
      // no re-keying when the queued chip becomes a real message, and none when
      // a later history read replaces it. Rejected ids fall back to a
      // store-assigned one (the message still goes through).
      id: string;
      // Stamped on every message so routing never depends on the server having
      // already processed our bind frame.
      session_id: string;
      media?: OutboundMedia[];
      // The browser's IANA timezone, so the server renders wake-frontmatter
      // times in this device's zone rather than its own. Server validates it.
      tz?: string;
    };

type InboundFrame = {
  type: string;
  text?: string;
  error?: string;
  // type:"peer_message" — another viewer of this session spoke.
  sender?: string;
} & Partial<StreamFrame>;

const maxBackoffMs = 15_000;

// clientMessageID mints the id a message will carry on disk. The "web-" prefix
// and the [A-Za-z0-9_-] charset are the server's validation rule
// (sanitizeClientMessageID in channel/web.go) — an id outside it is rejected
// there and the message silently reverts to a store-assigned id, so the two
// must stay in sync. crypto.randomUUID needs a secure context, which a plain
// http:// LAN deployment is not; the fallback keeps enough entropy that two
// pages of the same session cannot collide.
export function clientMessageID(): string {
  const uuid =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
  return `web-${uuid}`;
}

// browserTimezone returns the device's IANA zone (e.g. "Asia/Shanghai"), or ""
// if the runtime can't report it. Sent with each message so the server renders
// wake-frontmatter times in the user's zone instead of its own.
function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
}

export class NagobotSocket {
  private ws: WebSocket | null = null;
  // No default session: bind() is always called before connect(). A silent
  // fallback here would route messages into someone else's session.
  private session = "";
  private backoffMs = 1_000;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private closed = false;

  onResponse: ((text: string) => void) | null = null;
  onStream: ((ev: StreamFrame) => void) | null = null;
  onStatus: ((status: SocketStatus) => void) | null = null;
  onError: ((message: string) => void) | null = null;
  onPeerMessage: ((text: string, sender: string) => void) | null = null;

  connect(): void {
    if (this.closed) return;
    this.onStatus?.("connecting");

    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${window.location.host}/ws`);
    this.ws = ws;

    ws.onopen = () => {
      this.backoffMs = 1_000;
      this.sendFrame({ type: "bind", session_id: this.session });
      this.onStatus?.("open");
    };

    ws.onmessage = (ev) => {
      let frame: InboundFrame;
      try {
        frame = JSON.parse(ev.data as string);
      } catch {
        return;
      }
      switch (frame.type) {
        case "response":
          if (frame.text) this.onResponse?.(frame.text);
          break;
        case "stream":
          if (frame.kind) this.onStream?.(frame as StreamFrame);
          break;
        case "error":
          if (frame.error) this.onError?.(frame.error);
          break;
        case "peer_message":
          if (frame.text) this.onPeerMessage?.(frame.text, frame.sender ?? "");
          break;
        // "bound" acks need no handling.
      }
    };

    ws.onclose = (ev) => {
      if (this.ws !== ws) return; // superseded by a newer connection
      this.ws = null;
      if (ev.code === closeCodeReplaced) {
        this.onStatus?.("replaced");
        return; // no reconnect — the user reclaims via resume()
      }
      this.onStatus?.("closed");
      this.scheduleReconnect();
    };
  }

  private scheduleReconnect(): void {
    if (this.closed || this.reconnectTimer) return;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, this.backoffMs);
    this.backoffMs = Math.min(this.backoffMs * 2, maxBackoffMs);
  }

  // resume reconnects after a "replaced" takeover — this page becomes the
  // active one and the server bumps whichever page took over (code 4001).
  resume(): void {
    if (this.closed || this.ws) return;
    this.backoffMs = 1_000;
    this.connect();
  }

  bind(session: string): void {
    this.session = session;
    this.sendFrame({ type: "bind", session_id: session });
  }

  // send returns false when the socket is not open; the message is not queued.
  // `id` is minted by the caller (see clientMessageID) because the caller is
  // what needs to remember it.
  send(id: string, text: string, media?: OutboundMedia[]): boolean {
    return this.sendFrame({
      type: "message",
      id,
      text,
      session_id: this.session,
      ...(media && media.length > 0 ? { media } : {}),
      ...(browserTimezone() ? { tz: browserTimezone() } : {}),
    });
  }

  private sendFrame(frame: OutboundFrame): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) return false;
    this.ws.send(JSON.stringify(frame));
    return true;
  }

  close(): void {
    this.closed = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
  }
}
