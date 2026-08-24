import { mediaBase } from "@/lib/mediaBase";
import type { CommentatorIntercomSlot } from "@/lib/api";

export type CommentatorJoinInfo = {
  channel_id: number;
  ice_servers: RTCIceServer[];
  intercom: CommentatorIntercomSlot[];
  ws_path: string;
};

export type CommentatorConnectionState =
  | "idle"
  | "joining"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "failed";

function wsURL(path: string): string {
  const u = new URL(path, mediaBase());
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  return u.toString();
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function fetchCommentatorJoin(token: string): Promise<CommentatorJoinInfo> {
  const res = await fetch(`${mediaBase()}/api/commentator/join/${encodeURIComponent(token)}`);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `join failed: ${res.status}`);
  }
  return res.json();
}

type SignalMsg = {
  type: string;
  sdp?: string;
  channel?: number;
  candidate?: RTCIceCandidateInit;
  intercom?: CommentatorIntercomSlot[];
  channel_id?: number;
  message?: string;
};

export class CommentatorSession {
  private pc: RTCPeerConnection | null = null;
  private ws: WebSocket | null = null;
  private localStream: MediaStream | null = null;
  private audioCtx: AudioContext | null = null;
  private gainNodes = new Map<string, GainNode>();
  private pttChannel = 0;
  private stopped = false;
  private reconnectAttempts = 0;
  private readonly maxReconnects = 5;

  onState?: (state: CommentatorConnectionState) => void;
  onError?: (message: string) => void;
  onIntercom?: (slots: CommentatorIntercomSlot[]) => void;
  onRemoteVideo?: (stream: MediaStream) => void;

  constructor(private readonly token: string) {}

  async start(): Promise<void> {
    this.stopped = false;
    this.onState?.("joining");
    const join = await fetchCommentatorJoin(this.token);
    this.onIntercom?.(join.intercom);

    if (!this.localStream) {
      this.localStream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true },
        video: { width: { ideal: 1280 }, height: { ideal: 720 } },
      });
    }

    this.pc = new RTCPeerConnection({ iceServers: join.ice_servers });
    for (const track of this.localStream.getTracks()) {
      this.pc.addTrack(track, this.localStream);
    }

    if (!this.audioCtx) {
      this.audioCtx = new AudioContext();
    }
    this.gainNodes.clear();

    this.pc.onicecandidate = (ev) => {
      if (ev.candidate) {
        this.send({ type: "ice", candidate: ev.candidate.toJSON() });
      }
    };
    this.pc.ontrack = (ev) => {
      if (ev.track.kind === "video") {
        this.onRemoteVideo?.(ev.streams[0] ?? new MediaStream([ev.track]));
        return;
      }
      const stream = ev.streams[0] ?? new MediaStream([ev.track]);
      const id = ev.track.id || ev.track.label || "track";
      const gain = this.audioCtx!.createGain();
      gain.gain.value = id.includes("pgm") || id.includes("audio") ? 1 : 0.8;
      const src = this.audioCtx!.createMediaStreamSource(stream);
      src.connect(gain).connect(this.audioCtx!.destination);
      this.gainNodes.set(id, gain);
    };
    this.pc.onconnectionstatechange = () => {
      const state = this.pc?.connectionState;
      if (state === "failed" || state === "disconnected") {
        void this.scheduleReconnect();
      }
    };

    this.onState?.("connecting");
    await this.connectSignaling(join.ws_path);
    const offer = await this.pc.createOffer();
    await this.pc.setLocalDescription(offer);
    this.send({ type: "offer", sdp: offer.sdp });
  }

  private async scheduleReconnect() {
    if (this.stopped || this.reconnectAttempts >= this.maxReconnects) {
      this.onState?.("failed");
      this.onError?.("WebRTC-anslutningen bröts. Prova Återanslut.");
      return;
    }
    this.reconnectAttempts++;
    this.onState?.("reconnecting");
    this.ws?.close();
    this.pc?.close();
    this.ws = null;
    this.pc = null;
    await delay(Math.min(1000 * this.reconnectAttempts, 5000));
    if (this.stopped) return;
    try {
      await this.start();
      this.reconnectAttempts = 0;
    } catch (e) {
      void this.scheduleReconnect();
    }
  }

  private async connectSignaling(path: string): Promise<void> {
    await new Promise<void>((resolve, reject) => {
      const ws = new WebSocket(wsURL(path));
      this.ws = ws;
      ws.onopen = () => resolve();
      ws.onerror = () => reject(new Error("signaling websocket failed"));
      ws.onmessage = (ev) => this.handleSignal(JSON.parse(String(ev.data)) as SignalMsg);
      ws.onclose = () => {
        if (!this.stopped && this.pc?.connectionState !== "connected") {
          void this.scheduleReconnect();
        }
      };
    });
  }

  private handleSignal(msg: SignalMsg) {
    if (!this.pc) return;
    switch (msg.type) {
      case "config":
        if (msg.intercom) this.onIntercom?.(msg.intercom);
        break;
      case "answer":
        if (msg.sdp) {
          void this.pc.setRemoteDescription({ type: "answer", sdp: msg.sdp }).then(() => {
            this.reconnectAttempts = 0;
            this.onState?.("connected");
          });
        }
        break;
      case "ice":
        if (msg.candidate) void this.pc.addICECandidate(msg.candidate);
        break;
      case "error":
        this.onError?.(msg.message || "WebRTC error");
        this.onState?.("failed");
        break;
    }
  }

  setPGMVolume(value: number) {
    for (const [id, gain] of this.gainNodes) {
      if (id.includes("pgm") || id === "audio") gain.gain.value = value;
    }
  }

  setIntercomVolume(slotId: number, value: number) {
    for (const [id, gain] of this.gainNodes) {
      if (id.includes(`intercom${slotId}`)) gain.gain.value = value;
    }
  }

  setPTT(channel: number) {
    this.pttChannel = channel;
    this.send({ type: "ptt", channel });
  }

  private send(msg: SignalMsg) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  stop() {
    this.stopped = true;
    this.send({ type: "ptt", channel: 0 });
    this.ws?.close();
    this.pc?.close();
    this.localStream?.getTracks().forEach((t) => t.stop());
    void this.audioCtx?.close();
    this.ws = null;
    this.pc = null;
    this.localStream = null;
    this.audioCtx = null;
    this.gainNodes.clear();
  }
}
