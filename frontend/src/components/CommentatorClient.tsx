"use client";

import { useEffect, useRef, useState } from "react";
import type { CommentatorIntercomSlot } from "@/lib/api";
import {
  CommentatorSession,
  type CommentatorConnectionState,
} from "@/lib/commentatorWebRTC";

type Props = {
  token: string;
};

export default function CommentatorClient({ token }: Props) {
  const sessionRef = useRef<CommentatorSession | null>(null);
  const [state, setState] = useState<CommentatorConnectionState>("idle");
  const [error, setError] = useState<string | null>(null);
  const [intercom, setIntercom] = useState<CommentatorIntercomSlot[]>([]);
  const [pgmVol, setPgmVol] = useState(1);
  const [intercomVol, setIntercomVol] = useState<Record<number, number>>({});
  const [pttActive, setPttActive] = useState<number | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);

  useEffect(() => {
    const session = new CommentatorSession(token);
    sessionRef.current = session;
    session.onState = setState;
    session.onError = (msg) => setError(msg);
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
      if (videoRef.current) {
        videoRef.current.srcObject = stream;
      }
    };

    void session.start().catch((e) => {
      setError(String(e));
      setState("failed");
    });

    return () => {
      session.stop();
      sessionRef.current = null;
    };
  }, [token]);

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

  return (
    <div className="commentator-shell">
      <header className="commentator-header">
        <h1>Remote Commentator</h1>
        <p className="commentator-subtitle">Status: {state}</p>
      </header>

      {error && <div className="error-message">{error}</div>}

      <div className="commentator-main">
        <div className="commentator-video-wrap">
          <video ref={videoRef} className="commentator-program-video" autoPlay playsInline />
          <p className="commentator-video-label">Program (WebRTC)</p>
        </div>

        <aside className="commentator-controls">
          <div className="commentator-fader-row">
            <label>PGM</label>
            <input
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
              <label>{slot.name}</label>
              <input
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
          ))}
        </aside>
      </div>
    </div>
  );
}
