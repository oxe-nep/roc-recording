"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { CommentatorIntercomSlot } from "@/lib/api";
import {
  CommentatorSession,
  type CommentatorConnectionState,
  type CommentatorRTCStats,
} from "@/lib/commentatorWebRTC";

type Props = {
  token: string;
};

const STATE_LABELS: Record<CommentatorConnectionState, string> = {
  idle: "Idle",
  joining: "Joining…",
  connecting: "Connecting media…",
  negotiating: "Setting up…",
  connected: "Connected",
  reconnecting: "Reconnecting…",
  failed: "Failed",
};

export default function CommentatorClient({ token }: Props) {
  const sessionRef = useRef<CommentatorSession | null>(null);
  const [state, setState] = useState<CommentatorConnectionState>("idle");
  const [error, setError] = useState<string | null>(null);
  const [intercom, setIntercom] = useState<CommentatorIntercomSlot[]>([]);
  const [pgmVol, setPgmVol] = useState(1);
  const [intercomVol, setIntercomVol] = useState<Record<number, number>>({});
  const [pttActive, setPttActive] = useState<number | null>(null);
  const [audioLocked, setAudioLocked] = useState(false);
  const [rtcStats, setRtcStats] = useState<CommentatorRTCStats | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);

  const bindSession = useCallback((session: CommentatorSession) => {
    session.onState = setState;
    session.onError = (msg) => setError(msg);
    session.onAudioLocked = setAudioLocked;
    session.onStats = setRtcStats;
    session.onIntercom = (slots) => {
      setIntercom(slots);
      setIntercomVol((prev) => {
        const next = { ...prev };
        for (const s of slots) {
          if (next[s.id] == null) next[s.id] = 0.8;
        }
        return next;
      });
    };
    session.onRemoteVideo = (stream) => {
      const el = videoRef.current;
      if (!el) return;
      el.srcObject = stream;
      void el.play().catch(() => {
        /* autoplay policy — muted video should still start */
      });
    };
  }, []);

  useEffect(() => {
    const session = new CommentatorSession(token);
    sessionRef.current = session;
    bindSession(session);

    void session.start().catch((e) => {
      setError(String(e));
      setState("failed");
    });

    return () => {
      session.stop();
      sessionRef.current = null;
    };
  }, [token, bindSession]);

  const reconnect = () => {
    sessionRef.current?.stop();
    setError(null);
    const session = new CommentatorSession(token);
    sessionRef.current = session;
    bindSession(session);
    void session.start().catch((e) => {
      setError(String(e));
      setState("failed");
    });
  };

  useEffect(() => {
    sessionRef.current?.setPGMVolume(pgmVol);
  }, [pgmVol]);

  useEffect(() => {
    const session = sessionRef.current;
    if (!session) return;
    for (const [id, vol] of Object.entries(intercomVol)) {
      session.setIntercomVolume(Number(id), vol);
    }
  }, [intercomVol]);

  const bindPTT = (channelId: number) => ({
    onPointerDown: (e: React.PointerEvent) => {
      e.preventDefault();
      (e.target as HTMLElement).setPointerCapture(e.pointerId);
      setPttActive(channelId);
      sessionRef.current?.setPTT(channelId);
    },
    onPointerUp: (e: React.PointerEvent) => {
      e.preventDefault();
      setPttActive(null);
      sessionRef.current?.setPTT(0);
    },
    onPointerLeave: () => {
      if (pttActive === channelId) {
        setPttActive(null);
        sessionRef.current?.setPTT(0);
      }
    },
  });

  const statusClass =
    state === "connected"
      ? "commentator-status--ok"
      : state === "failed"
        ? "commentator-status--err"
        : "commentator-status--pending";

  return (
    <div className="commentator-shell">
      <header className="commentator-header">
        <div className="commentator-header-text">
          <p className="commentator-eyebrow">ROC Recording</p>
          <h1>Remote Commentator</h1>
        </div>
        <div className="commentator-header-actions">
          <span className={`commentator-status-pill ${statusClass}`}>{STATE_LABELS[state]}</span>
          {(state === "failed" || state === "reconnecting") && (
            <button type="button" className="commentator-btn commentator-btn-primary" onClick={reconnect}>
              Reconnect
            </button>
          )}
        </div>
      </header>

      {error && <div className="commentator-alert">{error}</div>}
      {audioLocked && (
        <button
          type="button"
          className="commentator-alert"
          style={{ cursor: "pointer", border: "none", width: "100%", textAlign: "left" }}
          onClick={() => void sessionRef.current?.unlockAudio()}
        >
          Click here to enable program audio (browser blocked autoplay)
        </button>
      )}

      <div className="commentator-main">
        <section className="commentator-video-panel">
          <div className="commentator-video-wrap">
            <video ref={videoRef} className="commentator-program-video" autoPlay playsInline muted />
            {state !== "connected" && (
              <div className="commentator-video-overlay">
                <span>{STATE_LABELS[state]}</span>
              </div>
            )}
            {state === "connected" && rtcStats && (
              <div className="commentator-stats-overlay" title="WebRTC receive stats for program video">
                <div>
                  {rtcStats.videoCodec} · {rtcStats.videoInKbps} kb/s · {rtcStats.videoInFps} fps
                </div>
                <div>
                  loss {rtcStats.videoLossPct}% ({rtcStats.videoPacketsLost}/{rtcStats.videoPacketsReceived}) · jitter{" "}
                  {rtcStats.videoJitterMs} ms
                </div>
                <div>
                  decoded {rtcStats.videoFramesDecoded} · dropped {rtcStats.videoFramesDropped}
                  {rtcStats.videoFreezeCount > 0 ? ` · freezes ${rtcStats.videoFreezeCount}` : ""}
                </div>
                <div>
                  audio {rtcStats.audioInKbps} kb/s · lost {rtcStats.audioPacketsLost} · {rtcStats.iceState}
                </div>
                <div className="commentator-stats-pair">{rtcStats.candidatePair}</div>
              </div>
            )}
          </div>
          <p className="commentator-video-caption">
            Program feed
            {rtcStats && rtcStats.videoLossPct >= 2 && (
              <span className="commentator-stats-warn"> — high packet loss (VPN/UDP?)</span>
            )}
            {rtcStats && rtcStats.videoLossPct < 1 && rtcStats.videoInKbps > 0 && rtcStats.videoInKbps < 800 && (
              <span className="commentator-stats-warn"> — low bitrate / encoder starved</span>
            )}
          </p>
        </section>

        <aside className="commentator-controls">
          <h2 className="commentator-controls-title">Audio mix</h2>

          <div className="commentator-fader-row">
            <div className="commentator-fader-head">
              <label htmlFor="pgm-vol">PGM / mix-minus</label>
              <span className="commentator-fader-val">{Math.round(pgmVol * 100)}%</span>
            </div>
            <input
              id="pgm-vol"
              className="commentator-range"
              type="range"
              min={0}
              max={1}
              step={0.01}
              value={pgmVol}
              onChange={(e) => setPgmVol(Number(e.target.value))}
            />
          </div>

          {intercom.map((slot) => (
            <div key={slot.id} className="commentator-fader-row">
              <div className="commentator-fader-head">
                <label htmlFor={`ic-${slot.id}`}>{slot.name}</label>
                <span className="commentator-fader-val">
                  {Math.round((intercomVol[slot.id] ?? 0.8) * 100)}%
                </span>
              </div>
              <div className="commentator-fader-actions">
                <input
                  id={`ic-${slot.id}`}
                  className="commentator-range"
                  type="range"
                  min={0}
                  max={1}
                  step={0.01}
                  value={intercomVol[slot.id] ?? 0.8}
                  onChange={(e) =>
                    setIntercomVol((prev) => ({ ...prev, [slot.id]: Number(e.target.value) }))
                  }
                />
                <button
                  type="button"
                  className={`commentator-ptt${pttActive === slot.id ? " active" : ""}`}
                  {...bindPTT(slot.id)}
                >
                  PTT
                </button>
              </div>
            </div>
          ))}

          {intercom.length === 0 && (
            <p className="commentator-empty-hint">No intercom channels enabled in producer settings.</p>
          )}
        </aside>
      </div>
    </div>
  );
}
