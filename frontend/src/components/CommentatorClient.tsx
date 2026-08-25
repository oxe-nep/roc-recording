"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CommentatorIntercomSlot } from "@/lib/api";
import {
  loadCommentatorDebug,
  loadCommentatorDevices,
  loadCommentatorVolumes,
  saveCommentatorDebug,
  saveCommentatorDevices,
  saveCommentatorVolumes,
  type CommentatorDevicePrefs,
} from "@/lib/commentatorPrefs";
import {
  CommentatorSession,
  listMediaDevices,
  type CommentatorConnectionState,
  type CommentatorRTCStats,
} from "@/lib/commentatorWebRTC";
import { useBodyScrollLock } from "@/hooks/useBodyScrollLock";

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

function parseIntercomVolumes(saved: Record<string, number>): Record<number, number> {
  const out: Record<number, number> = {};
  for (const [id, v] of Object.entries(saved)) {
    const n = Number(id);
    if (Number.isFinite(n)) out[n] = v;
  }
  return out;
}

export default function CommentatorClient({ token }: Props) {
  const savedVolumes = useMemo(() => loadCommentatorVolumes(token), [token]);
  const sessionRef = useRef<CommentatorSession | null>(null);
  const [state, setState] = useState<CommentatorConnectionState>("idle");
  const [error, setError] = useState<string | null>(null);
  const [intercom, setIntercom] = useState<CommentatorIntercomSlot[]>([]);
  const [pgmVol, setPgmVol] = useState(savedVolumes.pgm);
  const [intercomVol, setIntercomVol] = useState<Record<number, number>>(() =>
    parseIntercomVolumes(savedVolumes.intercom),
  );
  const [pttActive, setPttActive] = useState<number | null>(null);
  const [hostaActive, setHostaActive] = useState(false);
  const [audioLocked, setAudioLocked] = useState(false);
  const [rtcStats, setRtcStats] = useState<CommentatorRTCStats | null>(null);
  const [showDebug, setShowDebug] = useState(() => loadCommentatorDebug(token));
  const [displayName, setDisplayName] = useState("");
  const [reconnectRequired, setReconnectRequired] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [devices, setDevices] = useState<CommentatorDevicePrefs>(() => loadCommentatorDevices());
  const [deviceLists, setDeviceLists] = useState<{ mics: MediaDeviceInfo[]; cams: MediaDeviceInfo[] }>({
    mics: [],
    cams: [],
  });
  const [deviceError, setDeviceError] = useState<string | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const pgmVolRef = useRef(pgmVol);
  const intercomVolRef = useRef(intercomVol);
  pgmVolRef.current = pgmVol;
  intercomVolRef.current = intercomVol;

  useBodyScrollLock(settingsOpen);

  useEffect(() => {
    const saved = loadCommentatorVolumes(token);
    setPgmVol(saved.pgm);
    setIntercomVol(parseIntercomVolumes(saved.intercom));
    setShowDebug(loadCommentatorDebug(token));
  }, [token]);

  useEffect(() => {
    saveCommentatorVolumes(token, pgmVol, intercomVol);
  }, [token, pgmVol, intercomVol]);

  useEffect(() => {
    saveCommentatorDebug(token, showDebug);
  }, [token, showDebug]);

  useEffect(() => {
    const title = displayName.trim();
    document.title = title || "Commentator";
  }, [displayName]);

  const sessionOptions = useMemo(() => {
    const saved = loadCommentatorVolumes(token);
    return {
      devices,
      initialPgmVolume: saved.pgm,
      initialIntercomVolumes: parseIntercomVolumes(saved.intercom),
    };
  }, [token, devices]);

  const bindSession = useCallback((session: CommentatorSession) => {
    session.onState = setState;
    session.onError = (msg) => setError(msg);
    session.onAudioLocked = setAudioLocked;
    session.onStats = setRtcStats;
    session.onReconnectRequired = setReconnectRequired;
    session.onDisplayName = setDisplayName;
    session.onIntercom = (slots) => {
      setIntercom(slots);
      setIntercomVol((prev) => {
        const next: Record<number, number> = {};
        for (const s of slots) {
          const vol = prev[s.id] ?? intercomVolRef.current[s.id] ?? 0.8;
          next[s.id] = vol;
          session.setIntercomVolume(s.id, vol);
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
    const session = new CommentatorSession(token, sessionOptions);
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
  }, [token, sessionOptions, bindSession]);

  const reconnect = () => {
    sessionRef.current?.stop();
    setError(null);
    setReconnectRequired(false);
    const session = new CommentatorSession(token, sessionOptions);
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

  useEffect(() => {
    sessionRef.current?.setHosta(hostaActive);
  }, [hostaActive]);

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

  const bindHosta = () => ({
    onPointerDown: (e: React.PointerEvent) => {
      e.preventDefault();
      (e.target as HTMLElement).setPointerCapture(e.pointerId);
      setHostaActive(true);
    },
    onPointerUp: (e: React.PointerEvent) => {
      e.preventDefault();
      setHostaActive(false);
    },
    onPointerLeave: () => setHostaActive(false),
  });

  const openSettings = async () => {
    setDeviceError(null);
    setSettingsOpen(true);
    try {
      await navigator.mediaDevices.getUserMedia({ audio: true, video: true }).then((s) => {
        s.getTracks().forEach((t) => t.stop());
      });
      setDeviceLists(await listMediaDevices());
    } catch (e) {
      setDeviceError(String(e));
    }
  };

  const applyDevices = () => {
    saveCommentatorDevices(devices);
    setSettingsOpen(false);
    reconnect();
  };

  const statusClass =
    state === "connected"
      ? "commentator-status--ok"
      : state === "failed"
        ? "commentator-status--err"
        : "commentator-status--pending";

  return (
    <div className="commentator-shell">
      <header className="commentator-header">
        <div className="commentator-header-brand">
          <img src="/nep-logo.svg" alt="NEP" className="nep-logo commentator-logo" />
          <div className="commentator-header-text">
            <h1>{displayName.trim() || "Commentator"}</h1>
          </div>
        </div>
        <div className="commentator-header-actions">
          <button type="button" className="commentator-btn" onClick={() => void openSettings()}>
            Settings
          </button>
          <span className={`commentator-status-pill ${statusClass}`}>{STATE_LABELS[state]}</span>
          {(state === "failed" || state === "reconnecting" || reconnectRequired) && (
            <button type="button" className="commentator-btn commentator-btn-primary" onClick={reconnect}>
              Reconnect
            </button>
          )}
        </div>
      </header>

      {error && <div className="commentator-alert">{error}</div>}
      {reconnectRequired && state === "connected" && (
        <div className="commentator-alert commentator-alert-warn">
          Intercom channels changed or quality settings updated. Reconnect to apply.
        </div>
      )}
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
            {state === "connected" && showDebug && rtcStats && (
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
            <p className="commentator-video-caption" aria-live="polite">
              {rtcStats && rtcStats.videoLossPct >= 2 && (
                <span className="commentator-stats-warn">High packet loss (VPN/UDP?)</span>
              )}
              {rtcStats && rtcStats.videoLossPct < 1 && rtcStats.videoInKbps > 0 && rtcStats.videoInKbps < 800 && (
                <span className="commentator-stats-warn">Low bitrate / encoder starved</span>
              )}
            </p>
          </div>
        </section>

        <section className="commentator-controls commentator-controls-below">
          <h2 className="commentator-controls-title">Audio mix</h2>

          <div className="commentator-mix-grid">
            <div className="commentator-channel-card commentator-channel-card-pgm">
              <div className="commentator-channel-head">
                <span className="commentator-channel-name">PGM</span>
                <span className="commentator-fader-val">{Math.round(pgmVol * 100)}%</span>
              </div>
              <input
                id="pgm-vol"
                className="commentator-range commentator-range-accent"
                type="range"
                min={0}
                max={1}
                step={0.01}
                value={pgmVol}
                onChange={(e) => setPgmVol(Number(e.target.value))}
                aria-label="PGM volume"
              />
              <div className="commentator-pgm-face" aria-hidden>
                <span>Program</span>
              </div>
            </div>

            {intercom.map((slot) => {
              const vol = intercomVol[slot.id] ?? 0.8;
              return (
                <div key={slot.id} className="commentator-channel-card">
                  <div className="commentator-channel-head">
                    <span className="commentator-channel-name">{slot.name}</span>
                    <span className="commentator-fader-val">{Math.round(vol * 100)}%</span>
                  </div>
                  <input
                    id={`ic-${slot.id}`}
                    className="commentator-range"
                    type="range"
                    min={0}
                    max={1}
                    step={0.01}
                    value={vol}
                    onChange={(e) =>
                      setIntercomVol((prev) => ({ ...prev, [slot.id]: Number(e.target.value) }))
                    }
                    aria-label={`${slot.name} volume`}
                  />
                  <button
                    type="button"
                    className={`commentator-action-btn commentator-action-btn-ptt${pttActive === slot.id ? " active" : ""}`}
                    {...bindPTT(slot.id)}
                  >
                    <span className="commentator-action-btn-label">PTT</span>
                    <span className="commentator-action-btn-sub">{slot.name}</span>
                  </button>
                </div>
              );
            })}

            <div className="commentator-channel-card commentator-channel-card-hosta">
              <div className="commentator-channel-head">
                <span className="commentator-channel-name">Mic</span>
              </div>
              <button
                type="button"
                className={`commentator-action-btn commentator-action-btn-hosta${hostaActive ? " active" : ""}`}
                {...bindHosta()}
                title="Hold to mute outgoing mic (cough mute)"
              >
                <span className="commentator-action-btn-label">HOSTA</span>
                <span className="commentator-action-btn-sub">Hold to mute</span>
              </button>
            </div>
          </div>

          {intercom.length === 0 && (
            <p className="commentator-empty-hint">No intercom channels enabled in producer settings.</p>
          )}
        </section>
      </div>

      {settingsOpen && (
        <div className="modal-backdrop" onClick={() => setSettingsOpen(false)} role="presentation">
          <div
            className="modal-panel commentator-settings-panel"
            onClick={(e) => e.stopPropagation()}
            role="dialog"
            aria-label="Commentator device settings"
          >
            <div className="modal-header">
              <h2>Settings</h2>
              <button type="button" className="modal-close" onClick={() => setSettingsOpen(false)} aria-label="Close">
                ×
              </button>
            </div>
            {deviceError && <div className="commentator-alert">{deviceError}</div>}
            <label className="commentator-toggle commentator-settings-debug" title="Show WebRTC debug overlay on video">
              <input
                type="checkbox"
                checked={showDebug}
                onChange={(e) => setShowDebug(e.target.checked)}
              />
              <span>Show WebRTC debug overlay</span>
            </label>
            <p className="channel-settings-hint">
              Device choices are saved in this browser. Reconnect applies a new mic/camera.
            </p>
            <label className="presets-field">
              <span>Microphone</span>
              <select
                value={devices.micId}
                onChange={(e) => setDevices((d) => ({ ...d, micId: e.target.value }))}
              >
                <option value="">System default</option>
                {deviceLists.mics.map((d) => (
                  <option key={d.deviceId} value={d.deviceId}>
                    {d.label || `Mic ${d.deviceId.slice(0, 8)}`}
                  </option>
                ))}
              </select>
            </label>
            <label className="presets-field">
              <span>Camera</span>
              <select
                value={devices.camId}
                onChange={(e) => setDevices((d) => ({ ...d, camId: e.target.value }))}
              >
                <option value="">System default</option>
                {deviceLists.cams.map((d) => (
                  <option key={d.deviceId} value={d.deviceId}>
                    {d.label || `Camera ${d.deviceId.slice(0, 8)}`}
                  </option>
                ))}
              </select>
            </label>
            <div className="channel-settings-actions">
              <button type="button" className="commentator-btn" onClick={() => setSettingsOpen(false)}>
                Cancel
              </button>
              <button type="button" className="commentator-btn commentator-btn-primary" onClick={applyDevices}>
                Save &amp; reconnect
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
