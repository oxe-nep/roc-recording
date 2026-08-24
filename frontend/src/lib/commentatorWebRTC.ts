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
  | "negotiating"
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
  private answering = false;
  private stopped = false;
  private reconnectAttempts = 0;
  private readonly maxReconnects = 3;
  private iceServers: RTCIceServer[] = [];
  private iceTimer: ReturnType<typeof setTimeout> | null = null;

  onState?: (state: CommentatorConnectionState) => void;
  onError?: (message: string) => void;
  onIntercom?: (slots: CommentatorIntercomSlot[]) => void;
  onRemoteVideo?: (stream: MediaStream) => void;

  constructor(private readonly token: string) {}

  async start(): Promise<void> {
    this.stopped = false;
    this.remoteDescSet = false;
    this.answering = false;
    this.pendingRemoteICE = [];
    this.onState?.("joining");

    const join = await fetchCommentatorJoin(this.token);
    this.iceServers = join.ice_servers;
    this.onIntercom?.(join.intercom);

    this.onState?.("connecting");
    await this.connectSignaling(join.ws_path);
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
          const hint =
            path.includes("/api/commentator/ws/")
              ? " Redeploy frontend and capture host, then create a new invite."
              : "";
          reject(
            new Error(
              `Signaling WebSocket closed (${ev.code}${ev.reason ? `: ${ev.reason}` : ""}). URL: ${url}.${hint}`
            )
          );
        }
      };
      ws.onerror = () => {
        if (!settled) {
          reject(
            new Error(
              `Signaling WebSocket failed (${url}). If the dashboard works but this does not, redeploy frontend + capture host and create a new invite.`
            )
          );
        }
      };
      ws.onmessage = (ev) => void this.handleSignal(JSON.parse(String(ev.data)) as SignalMsg);
    });
  }

  private markConnected() {
    if (this.iceTimer) {
      clearTimeout(this.iceTimer);
      this.iceTimer = null;
    }
    this.reconnectAttempts = 0;
    this.onState?.("connected");
  }

  private startIceTimeout() {
    if (this.iceTimer) clearTimeout(this.iceTimer);
    this.iceTimer = setTimeout(() => {
      if (this.stopped || this.pc?.connectionState === "connected") return;
      const ice = this.pc?.iceConnectionState;
      if (ice === "connected" || ice === "completed") return;
      this.onError?.(
        "Media connection timed out (ICE). On the capture host set WEBRTC_PUBLIC_HOST to a reachable IP/hostname and configure TURN for remote commentators."
      );
      this.onState?.("failed");
    }, 25000);
  }

  private bindPeerConnectionHandlers() {
    if (!this.pc) return;
    this.pc.oniceconnectionstatechange = () => {
      const ice = this.pc?.iceConnectionState;
      if (ice === "connected" || ice === "completed") {
        this.markConnected();
      } else if (ice === "failed") {
        void this.scheduleReconnect();
      }
    };
    this.pc.onconnectionstatechange = () => {
      const state = this.pc?.connectionState;
      if (state === "connected") {
        this.markConnected();
      } else if (state === "failed") {
        void this.scheduleReconnect();
      }
    };
  }

  private async handleOffer(sdp: string) {
    if (this.answering || this.stopped) return;
    this.answering = true;
    this.onState?.("negotiating");
    try {
      this.pc = new RTCPeerConnection({ iceServers: this.iceServers });
      this.bindPeerConnectionHandlers();

      if (!this.localStream) {
        this.localStream = await navigator.mediaDevices.getUserMedia({
          audio: { echoCancellation: true, noiseSuppression: true },
          video: { width: { ideal: 1280 }, height: { ideal: 720 } },
        });
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

      await this.pc.setRemoteDescription({ type: "offer", sdp });
      this.remoteDescSet = true;

      const videoTrack = this.localStream.getVideoTracks()[0];
      const audioTrack = this.localStream.getAudioTracks()[0];
      if (videoTrack) {
        this.pc.addTransceiver(videoTrack, {
          direction: "sendonly",
          streams: [this.localStream],
        });
      }
      if (audioTrack) {
        this.pc.addTransceiver(audioTrack, {
          direction: "sendonly",
          streams: [this.localStream],
        });
      }

      const answer = await this.pc.createAnswer();
      await this.pc.setLocalDescription(answer);
      this.send({ type: "answer", sdp: answer.sdp });
      this.onState?.("connecting");
      this.startIceTimeout();

      for (const c of this.pendingRemoteICE) {
        await this.pc.addIceCandidate(c);
      }
      this.pendingRemoteICE = [];
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e);
      this.onError?.(`WebRTC negotiation failed: ${detail}`);
      this.onState?.("failed");
    }
  }

  private async handleSignal(msg: SignalMsg) {
    switch (msg.type) {
      case "config":
        if (msg.intercom) this.onIntercom?.(msg.intercom);
        return;
      case "offer":
        if (msg.sdp) await this.handleOffer(msg.sdp);
        return;
      case "error":
        this.onError?.(msg.message || "WebRTC error");
        this.onState?.("failed");
        return;
      case "ice":
        if (!msg.candidate) return;
        if (!this.pc || !this.remoteDescSet) {
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
    if (this.iceTimer) {
      clearTimeout(this.iceTimer);
      this.iceTimer = null;
    }
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
