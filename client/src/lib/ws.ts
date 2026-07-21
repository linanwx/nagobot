// WebSocket client for the nagobot web channel (/ws).
//
// Protocol (see channel/web.go):
//   client → server: {type: "bind", session_id} | {type: "message", text}
//   server → client: {type: "response", text} | {type: "bound", text}
//                  | {type: "error", error} | {type: "stream", kind, ...}
//
// "stream" frames carry live turn activity: thinking/text deltas (with the
// round-accumulated snapshot for self-healing), tool lifecycle, and round/
// turn boundaries.

// "replaced": another page bound this session and the server closed us with
// code 4001. Auto-reconnecting would kick that page right back, so the socket
// stays down until resume() is called explicitly.
export type SocketStatus = "connecting" | "open" | "closed" | "replaced";

const closeCodeReplaced = 4001;

export type StreamFrame = {
  kind:
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
};

type OutboundFrame =
  | { type: "bind"; session_id: string }
  | { type: "message"; text: string };

type InboundFrame = {
  type: string;
  text?: string;
  error?: string;
} & Partial<StreamFrame>;

const maxBackoffMs = 15_000;

export class NagobotSocket {
  private ws: WebSocket | null = null;
  private session = "cli";
  private backoffMs = 1_000;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private closed = false;

  onResponse: ((text: string) => void) | null = null;
  onStream: ((ev: StreamFrame) => void) | null = null;
  onStatus: ((status: SocketStatus) => void) | null = null;
  onError: ((message: string) => void) | null = null;

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
  send(text: string): boolean {
    return this.sendFrame({ type: "message", text });
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
