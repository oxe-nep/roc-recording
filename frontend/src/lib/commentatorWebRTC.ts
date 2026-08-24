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

/** Commentator page always talks to backend via same origin (nginx proxy). */
function commentatorOrigin(): string {
  if (typeof window !== "undefined") return window.location.origin;
  return "";
}

function wsURL(path: string): string {
  const u = new URL(path, commentatorOrigin() || "http://localhost");
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  return u.toString();
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function fetchCommentatorJoin(token: string): Promise<CommentatorJoinInfo> {
  const base = commentatorOrigin();
  const res = await fetch(`${base}/api/commentator/join/${encodeURIComponent(token)}`);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `Join failed (${res.status})`);
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
  private pendingRemoteICE: RTCIceCandidateInit[] = [];
  private remoteDescSet = false;
  private stopped = false;
  private reconnectAttempts = 0;
  private readonly maxReconnects = 3;

  onState?: (state: CommentatorConnectionState) => void;
  onError?: (message: string) => void;
  onIntercom?: (slots: CommentatorIntercomSlot[]) => void;
  onRemoteVideo?: (stream: MediaStream) => void;

  constructor(private readonly token: string) {}

  async start(): Promise<void> {
    this.stopped = false;
    this.remoteDescSet = false;
    this.pendingRemoteICE = [];
    this.onState?.("joining");

    const join = await fetchCommentatorJoin(this.token);
    this.onIntercom?.(join.intercom);

    this.onState?.("connecting");
    await this.connectSignaling(join.ws_path);

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
      if (state === "connected") {
        this.reconnectAttempts = 0;
        this.onState?.("connected");
      } else if (state === "failed") {
        void this.scheduleReconnect();
      }
    };

    const offer = await this.pc.createOffer();
    await this.pc.setLocalDescription(offer);
    this.send({ type: "offer", sdp: offer.sdp });
  }

  private async scheduleReconnect() {
    if (this.stopped || this.reconnectAttempts >= this.maxReconnects) {
      this.onState?.("failed");
      this.onError?.(
        "WebRTC connection failed. Check WEBRTC_PUBLIC_HOST (and TURN if remote) on the capture host, then click Reconnect."
      );
      return;
    }
    this.reconnectAttempts++;
    this.onState?.("reconnecting");
    this.ws?.close();
    this.pc?.close();
    this.ws = null;
    this.pc = null;
    await delay(Math.min(1500 * this.reconnectAttempts, 5000));
    if (this.stopped) return;
    try {
      await this.start();
    } catch {
      void this.scheduleReconnect();
    }
  }

  private async connectSignaling(path: string): Promise<void> {
    const url = wsURL(path);
    await new Promise<void>((resolve, reject) => {
      let settled = false;
      const ws = new WebSocket(url);
      this.ws = ws;
      ws.onopen = () => {
        settled = true;
        resolve();
      };
      ws.onclose = (ev) => {
        if (!settled) {
          reject(new Error(`Signaling WebSocket closed (${ev.code}${ev.reason ? `: ${ev.reason}` : ""})`));
        }
      };
      ws.onerror = () => {
        if (!settled) {
          reject(new Error(`Signaling WebSocket failed (${url})`));
        }
      };
      ws.onmessage = (ev) => void this.handleSignal(JSON.parse(String(ev.data)) as SignalMsg);
    });
  }

  private async handleSignal(msg: SignalMsg) {
    switch (msg.type) {
      case "config":
        if (msg.intercom) this.onIntercom?.(msg.intercom);
        return;
      case "error":
        this.onError?.(msg.message || "WebRTC error");
        this.onState?.("failed");
        return;
      case "answer":
        if (!this.pc) return;
        if (msg.sdp) {
          await this.pc.setRemoteDescription({ type: "answer", sdp: msg.sdp });
          this.remoteDescSet = true;
          for (const c of this.pendingRemoteICE) {
            await this.pc.addIceCandidate(c);
          }
          this.pendingRemoteICE = [];
        }
        break;
      case "ice":
        if (!this.pc || !msg.candidate) return;
        if (!this.remoteDescSet) {
          this.pendingRemoteICE.push(msg.candidate);
          return;
        }
        await this.pc.addIceCandidate(msg.candidate);
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

/** Turn a stored invite path into an absolute browser URL. */
export function absoluteInviteURL(url: string): string {
  const u = url.trim();
  if (!u) return "";
  if (u.startsWith("http://") || u.startsWith("https://")) return u;
  if (typeof window !== "undefined") {
    return `${window.location.origin}${u.startsWith("/") ? u : `/${u}`}`;
  }
  return u;
}
