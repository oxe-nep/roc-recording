import type { DialAction, KeyAction } from "@elgato/streamdeck";
import WebSocket from "ws";
import { postJson } from "./http.js";
import { scheduleProfileActivation } from "../profile.js";
import type { VolumeSettings } from "../actions/volume.js";

export const VOLUME_STEP = 0.05;

export type LayoutButton = {
  slot: number;
  channel: number;
  label: string;
};

type PairInfo = {
  origin: string;
  token: string;
  pin: string;
  controlsPath: string;
};

type PTTKeyRef = {
  kind: "ptt";
  action: KeyAction | DialAction;
  slot: number;
};

type VolumeKeyRef = {
  kind: "volume";
  action: KeyAction | DialAction;
  target: "pgm" | "intercom";
  slot?: number;
  direction: "up" | "down";
};

type KeyRef = PTTKeyRef | VolumeKeyRef;

type ControlsReady = {
  type: "ready";
  buttons?: LayoutButton[];
};

type ControlsLayout = {
  type: "layout";
  buttons: LayoutButton[];
};

type ControlsVolumes = {
  type: "volumes";
  pgm: number;
  intercom: Record<string, number>;
};

class Bridge {
  private controls: WebSocket | null = null;
  private controlsTimer: ReturnType<typeof setInterval> | null = null;
  private keys = new Map<string, KeyRef>();
  private layout: LayoutButton[] = [];
  private channelByKey = new Map<string, number>();
  private activeKey: string | null = null;
  private pairInfo: PairInfo | null = null;
  private volumes = { pgm: 1, intercom: {} as Record<number, number> };
  private connectSettings: { server?: string; code?: string } = {};

  saveConnectSettings(settings: { server?: string; code?: string }) {
    this.connectSettings = {
      server: settings.server?.trim() || this.connectSettings.server,
      code: settings.code?.trim() || this.connectSettings.code,
    };
  }

  async claimAndConnect(): Promise<boolean> {
    const server = (this.connectSettings.server || "https://commentator.nepsweden.tech").replace(/\/$/, "");
    const code = this.connectSettings.code?.trim().toUpperCase();
    if (!code) {
      console.error("[nep-commentator] missing pairing code in Connect action settings");
      return false;
    }
    try {
      const res = await postJson<{
        origin: string;
        token: string;
        pin: string;
        controls_path: string;
      }>(`${server}/api/commentator/deck/claim`, { code });
      if (!res.ok || !res.data) {
        console.error("[nep-commentator] deck claim failed:", res.status, res.text);
        return false;
      }
      const data = res.data;
      this.pairInfo = {
        origin: server,
        token: data.token,
        pin: data.pin,
        controlsPath: data.controls_path,
      };
      scheduleProfileActivation();
      return this.connectControls();
    } catch (err) {
      console.error("[nep-commentator] deck claim error:", err);
      return false;
    }
  }

  registerKey(action: KeyAction | DialAction, slot: number) {
    this.keys.set(action.id, { kind: "ptt", action, slot });
    void this.applyPTTKey(action.id);
  }

  registerVolumeKey(action: KeyAction | DialAction, settings: VolumeSettings) {
    const target = settings.target === "pgm" ? "pgm" : "intercom";
    const direction = settings.direction === "down" ? "down" : "up";
    this.keys.set(action.id, {
      kind: "volume",
      action,
      target,
      slot: settings.slot,
      direction,
    });
    void this.applyVolumeKey(action.id);
  }

  adjustVolume(actionId: string) {
    const ref = this.keys.get(actionId);
    if (!ref || ref.kind !== "volume") return;
    const delta = ref.direction === "up" ? VOLUME_STEP : -VOLUME_STEP;
    this.sendControls({
      type: "volume",
      target: ref.target,
      slot: ref.slot,
      delta,
    });
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

  private controlsURL(): string | null {
    if (!this.pairInfo) return null;
    const base = (this.connectSettings.server || this.pairInfo.origin).replace(/\/$/, "");
    const u = new URL(this.pairInfo.controlsPath, base);
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
      const timeout = setTimeout(() => resolve(false), 10000);
      ws.on("open", () => {
        clearTimeout(timeout);
        this.controlsTimer = setInterval(() => {
          this.sendControls({ type: "ping" });
        }, 25000);
        resolve(true);
      });
      ws.on("message", (raw) => this.onControlsMessage(String(raw)));
      ws.on("close", () => {
        if (this.controls === ws) this.controls = null;
      });
      ws.on("error", (err) => {
        clearTimeout(timeout);
        console.error("[nep-commentator] controls websocket error:", url, err);
        resolve(false);
      });
    });
  }

  private onControlsMessage(raw: string) {
    let msg: ControlsReady | ControlsLayout | ControlsVolumes;
    try {
      msg = JSON.parse(raw) as ControlsReady | ControlsLayout | ControlsVolumes;
    } catch {
      return;
    }
    switch (msg.type) {
      case "ready":
        if (msg.buttons?.length) {
          this.layout = msg.buttons;
          void this.refreshAllKeys();
        }
        break;
      case "layout":
        this.layout = msg.buttons;
        void this.refreshAllKeys();
        break;
      case "volumes":
        this.volumes.pgm = msg.pgm;
        this.volumes.intercom = {};
        for (const [id, value] of Object.entries(msg.intercom)) {
          const channel = Number(id);
          if (Number.isFinite(channel)) this.volumes.intercom[channel] = value;
        }
        void this.refreshVolumeKeys();
        break;
    }
  }

  private async refreshAllKeys() {
    for (const id of this.keys.keys()) {
      await this.applyKey(id);
    }
  }

  private async refreshVolumeKeys() {
    for (const [id, ref] of this.keys) {
      if (ref.kind === "volume") await this.applyVolumeKey(id);
    }
  }

  private async applyKey(actionId: string) {
    const ref = this.keys.get(actionId);
    if (!ref) return;
    if (ref.kind === "ptt") await this.applyPTTKey(actionId);
    else await this.applyVolumeKey(actionId);
  }

  private async applyPTTKey(actionId: string) {
    const ref = this.keys.get(actionId);
    if (!ref || ref.kind !== "ptt") return;
    const btn = this.layout.find((b) => b.slot === ref.slot);
    if (!btn) {
      await ref.action.setTitle("PTT");
      this.channelByKey.delete(actionId);
      return;
    }
    this.channelByKey.set(actionId, btn.channel);
    await ref.action.setTitle(btn.label);
    await ref.action.setState(0);
  }

  private async applyVolumeKey(actionId: string) {
    const ref = this.keys.get(actionId);
    if (!ref || ref.kind !== "volume") return;
    const arrow = ref.direction === "up" ? "+" : "−";
    if (ref.target === "pgm") {
      const pct = Math.round(this.volumes.pgm * 100);
      await ref.action.setTitle(`PGM ${arrow}\n${pct}%`);
      return;
    }
    const layoutBtn = ref.slot != null ? this.layout.find((b) => b.slot === ref.slot) : undefined;
    if (!layoutBtn) {
      await ref.action.setTitle(`${arrow}`);
      return;
    }
    const pct = Math.round((this.volumes.intercom[layoutBtn.channel] ?? 0.8) * 100);
    const short = layoutBtn.label.split(/\s+/)[0]?.slice(0, 8) || "IC";
    await ref.action.setTitle(`${short} ${arrow}\n${pct}%`);
  }

  private disconnectControls() {
    if (this.controlsTimer) {
      clearInterval(this.controlsTimer);
      this.controlsTimer = null;
    }
    this.controls?.close();
    this.controls = null;
  }

  private sendControls(msg: object) {
    if (!this.controls || this.controls.readyState !== WebSocket.OPEN) return;
    this.controls.send(JSON.stringify(msg));
  }
}

export const bridge = new Bridge();
