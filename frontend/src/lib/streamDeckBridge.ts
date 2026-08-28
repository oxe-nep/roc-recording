import type { CommentatorIntercomSlot } from "@/lib/api";

/** Default local port for the NEP Commentator Stream Deck plugin bridge. */
export const STREAM_DECK_BRIDGE_URL = "ws://127.0.0.1:17200";

export type StreamDeckLayoutButton = {
  slot: number;
  channel: number;
  label: string;
};

export type StreamDeckBridgeStatus = "offline" | "connecting" | "ready" | "paired" | "error";

type PairMessage = {
  type: "pair";
  origin: string;
  token: string;
  pin: string;
  controls_path: string;
};

type LayoutMessage = {
  type: "layout";
  buttons: StreamDeckLayoutButton[];
  hosta?: boolean;
};

type UnpairMessage = {
  type: "unpair";
};

type WebToPlugin = PairMessage | LayoutMessage | UnpairMessage;

type PluginMessage =
  | { type: "ready"; version?: string }
  | { type: "paired"; ok: boolean; error?: string; controls_connected?: boolean }
  | { type: "status"; controls_connected?: boolean };

export type StreamDeckBridgeCallbacks = {
  onStatus?: (status: StreamDeckBridgeStatus) => void;
  onControlsConnected?: (connected: boolean) => void;
};

export function intercomToLayout(intercom: CommentatorIntercomSlot[]): StreamDeckLayoutButton[] {
  return intercom
    .filter((slot) => slot.enabled)
    .map((slot, index) => ({
      slot: index,
      channel: slot.id,
      label: slot.name?.trim() || `Intercom ${slot.id}`,
    }));
}

export class StreamDeckBridge {
  private ws: WebSocket | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;
  private paired = false;
  private status: StreamDeckBridgeStatus = "offline";
  private lastLayout: StreamDeckLayoutButton[] = [];

  constructor(private readonly callbacks: StreamDeckBridgeCallbacks = {}) {}

  getStatus(): StreamDeckBridgeStatus {
    return this.status;
  }

  start() {
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    this.paired = false;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.send({ type: "unpair" });
    }
    this.ws?.close();
    this.ws = null;
    this.setStatus("offline");
  }

  pair(args: { origin: string; token: string; pin: string; controlsPath: string }) {
    this.send({
      type: "pair",
      origin: args.origin,
      token: args.token,
      pin: args.pin,
      controls_path: args.controlsPath,
    });
  }

  publishLayout(buttons: StreamDeckLayoutButton[], hosta = true) {
    this.lastLayout = buttons;
    this.send({ type: "layout", buttons, hosta });
  }

  private connect() {
    if (this.stopped || typeof window === "undefined") return;
    this.setStatus("connecting");
    const ws = new WebSocket(STREAM_DECK_BRIDGE_URL);
    this.ws = ws;

    ws.onopen = () => {
      if (this.stopped) return;
      this.setStatus("ready");
    };

    ws.onmessage = (ev) => {
      let msg: PluginMessage;
      try {
        msg = JSON.parse(String(ev.data)) as PluginMessage;
      } catch {
        return;
      }
      switch (msg.type) {
        case "ready":
          this.setStatus("ready");
          break;
        case "paired":
          if (msg.ok) {
            this.paired = true;
            this.setStatus("paired");
            if (this.lastLayout.length > 0) {
              this.send({ type: "layout", buttons: this.lastLayout, hosta: true });
            }
          } else {
            this.paired = false;
            this.setStatus("error");
          }
          this.callbacks.onControlsConnected?.(!!msg.controls_connected);
          break;
        case "status":
          this.callbacks.onControlsConnected?.(!!msg.controls_connected);
          break;
      }
    };

    ws.onclose = () => {
      this.ws = null;
      this.paired = false;
      if (this.stopped) {
        this.setStatus("offline");
        return;
      }
      this.setStatus("offline");
      this.reconnectTimer = setTimeout(() => this.connect(), 2000);
    };

    ws.onerror = () => {
      if (!this.stopped) this.setStatus("offline");
    };
  }

  private send(msg: WebToPlugin) {
    if (this.ws?.readyState !== WebSocket.OPEN) return;
    this.ws.send(JSON.stringify(msg));
  }

  private setStatus(status: StreamDeckBridgeStatus) {
    this.status = status;
    this.callbacks.onStatus?.(status);
  }
}
