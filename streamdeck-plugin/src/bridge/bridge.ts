import type { DialAction, KeyAction } from "@elgato/streamdeck";
import WebSocket, { WebSocketServer } from "ws";

export const BRIDGE_PORT = 17200;

export type LayoutButton = {
  slot: number;
  channel: number;
  label: string;
};

type PairMessage = {
  type: "pair";
  origin: string;
  token: string;
  pin: string;
  controls_path: string;
};

type LayoutMessage = {
  type: "layout";
  buttons: LayoutButton[];
  hosta?: boolean;
};

type WebMessage = PairMessage | LayoutMessage | { type: "unpair" };

type KeyRef = {
  action: KeyAction | DialAction;
  row: number;
  column: number;
};

class Bridge {
  private wss: WebSocketServer | null = null;
  private webClient: WebSocket | null = null;
  private controls: WebSocket | null = null;
  private controlsTimer: ReturnType<typeof setInterval> | null = null;
  private keys = new Map<string, KeyRef>();
  private layout: LayoutButton[] = [];
  private slotByKey = new Map<string, number>();
  private channelByKey = new Map<string, number>();
  private activeKey: string | null = null;
  private pairInfo: { origin: string; token: string; pin: string; controlsPath: string } | null = null;

  startLocalServer() {
    if (this.wss) return;
    this.wss = new WebSocketServer({ host: "127.0.0.1", port: BRIDGE_PORT });
    this.wss.on("connection", (ws) => {
      this.webClient = ws;
      ws.send(JSON.stringify({ type: "ready", version: "0.1.0" }));
      ws.on("message", (raw) => this.onWebMessage(ws, raw));
      ws.on("close", () => {
        if (this.webClient === ws) this.webClient = null;
        this.disconnectControls();
      });
    });
  }

  registerKey(action: KeyAction | DialAction, row: number, column: number) {
    this.keys.set(action.id, { action, row, column });
    void this.applyLayoutToAction(action);
  }

  async applyLayoutToAction(action: KeyAction | DialAction) {
    const ref = this.keys.get(action.id);
    if (!ref) return;
    const slot = ref.row * 5 + ref.column;
    this.slotByKey.set(action.id, slot);
    const btn = this.layout.find((b) => b.slot === slot);
    if (!btn) {
      await action.setTitle("PTT");
      this.channelByKey.delete(action.id);
      return;
    }
    this.channelByKey.set(action.id, btn.channel);
    await action.setTitle(btn.label);
    await action.setState(0);
  }

  pttDown(actionId: string) {
    const channel = this.channelByKey.get(actionId);
    if (!channel) return;
    this.activeKey = actionId;
    this.sendControls({ type: "ptt", channel });
  }

  pttUp() {
    if (!this.activeKey) return;
    this.activeKey = null;
    this.sendControls({ type: "ptt", channel: 0 });
  }

  private onWebMessage(ws: WebSocket, raw: WebSocket.RawData) {
    let msg: WebMessage;
    try {
      msg = JSON.parse(String(raw)) as WebMessage;
    } catch {
      return;
    }
    switch (msg.type) {
      case "pair":
        this.pairInfo = {
          origin: msg.origin.replace(/\/$/, ""),
          token: msg.token,
          pin: msg.pin,
          controlsPath: msg.controls_path,
        };
        void this.connectControls().then((ok) => {
          ws.send(JSON.stringify({ type: "paired", ok, controls_connected: ok }));
        });
        break;
      case "layout":
        this.layout = msg.buttons;
        void this.refreshAllKeys();
        break;
      case "unpair":
        this.pairInfo = null;
        this.layout = [];
        this.disconnectControls();
        void this.refreshAllKeys();
        break;
    }
  }

  private async refreshAllKeys() {
    for (const ref of this.keys.values()) {
      await this.applyLayoutToAction(ref.action);
    }
  }

  private controlsURL(): string | null {
    if (!this.pairInfo) return null;
    const u = new URL(this.pairInfo.controlsPath, this.pairInfo.origin);
    u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
    u.searchParams.set("pin", this.pairInfo.pin);
    return u.toString();
  }

  private async connectControls(): Promise<boolean> {
    const url = this.controlsURL();
    if (!url) return false;
    this.disconnectControls();
    return new Promise((resolve) => {
      const ws = new WebSocket(url);
      this.controls = ws;
      const timeout = setTimeout(() => resolve(false), 8000);
      ws.on("open", () => {
        clearTimeout(timeout);
        this.controlsTimer = setInterval(() => {
          this.sendControls({ type: "ping" });
        }, 25000);
        this.notifyStatus(true);
        resolve(true);
      });
      ws.on("message", (raw) => {
        try {
          const msg = JSON.parse(String(raw)) as { type?: string };
          if (msg.type === "ready") void this.refreshAllKeys();
        } catch {
          /* ignore */
        }
      });
      ws.on("close", () => {
        this.notifyStatus(false);
        if (this.controls === ws) this.controls = null;
      });
      ws.on("error", () => {
        clearTimeout(timeout);
        resolve(false);
      });
    });
  }

  private disconnectControls() {
    if (this.controlsTimer) {
      clearInterval(this.controlsTimer);
      this.controlsTimer = null;
    }
    this.controls?.close();
    this.controls = null;
    this.notifyStatus(false);
  }

  private sendControls(msg: object) {
    if (this.controls?.readyState !== WebSocket.OPEN) return;
    this.controls.send(JSON.stringify(msg));
  }

  private notifyStatus(controlsConnected: boolean) {
    if (this.webClient?.readyState !== WebSocket.OPEN) return;
    this.webClient.send(JSON.stringify({ type: "status", controls_connected: controlsConnected }));
  }
}

export const bridge = new Bridge();
