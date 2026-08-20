"use client";

import { useEffect, useState, useCallback } from "react";
import {
  fetchStreams,
  startStream,
  fetchRecordings,
  fetchAudioLevels,
  startRecording,
  stopRecording,
  startAllRecordings,
  stopAllRecordings,
  setRecordingName,
  type Stream,
  type AudioLevels,
  type RecordingInfo,
} from "@/lib/api";
import Thumbnail from "@/components/Thumbnail";
import AudioMonitor from "@/components/AudioMonitor";

function formatElapsed(sec?: number): string {
  if (sec === undefined || Number.isNaN(sec) || sec < 0) return "00:00:00";
  const s = Math.floor(sec);
  const hh = Math.floor(s / 3600);
  const mm = Math.floor((s % 3600) / 60);
  const ss = s % 60;
  return [hh, mm, ss].map((n) => String(n).padStart(2, "0")).join(":");
}

function formatBitrate(kbps?: number): string {
  if (!kbps || kbps <= 0) return "--";
  if (kbps >= 1000) return `${(kbps / 1000).toFixed(1)} Mbit/s`;
  return `${kbps.toFixed(0)} kbit/s`;
}

export default function StreamGrid() {
  const [streams, setStreams] = useState<Stream[]>([]);
  const [recordings, setRecordings] = useState<Record<number, RecordingInfo>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [recBusy, setRecBusy] = useState<Record<number, boolean>>({});
  const [globalRecBusy, setGlobalRecBusy] = useState(false);
  const [audio, setAudio] = useState<Record<number, AudioLevels>>({});
  const [listening, setListening] = useState<Record<number, boolean>>({});
  const [nameDraft, setNameDraft] = useState<Record<number, string>>({});

  const load = useCallback(async () => {
    try {
      const [streamData, recData] = await Promise.all([
        fetchStreams(),
        fetchRecordings(),
      ]);
      setStreams(streamData);
      const recMap: Record<number, RecordingInfo> = {};
      for (const r of recData) recMap[r.id] = r;
      setRecordings(recMap);
      setNameDraft((prev) => {
        const next = { ...prev };
        for (const r of recData) {
          if (next[r.id] === undefined) next[r.id] = r.name || `ch${r.id}`;
        }
        return next;
      });
      setError(null);
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const interval = setInterval(load, 1000);
    return () => clearInterval(interval);
  }, [load]);

  useEffect(() => {
    const pollAudio = async () => {
      const running = streams.filter((s) => s.status === "running");
      if (running.length === 0) return;
      await Promise.all(
        running.map(async (s) => {
          try {
            const levels = await fetchAudioLevels(s.id);
            setAudio((prev) => ({ ...prev, [s.id]: levels }));
          } catch {
            // ignore transient audio fetch errors
          }
        }),
      );
    };
    const interval = setInterval(pollAudio, 250);
    pollAudio();
    return () => clearInterval(interval);
  }, [streams]);

  const hasLevel = (db?: number): boolean =>
    db !== undefined && !Number.isNaN(db) && db > -89;

  const levelPct = (db?: number): number => {
    if (!hasLevel(db)) return 0;
    const clamped = Math.max(-50, Math.min(0, db!));
    return ((clamped + 50) / 50) * 100;
  };

  const formatDb = (db?: number): string => {
    if (!hasLevel(db)) return "--.- dBFS";
    return `${db!.toFixed(1)} dBFS`;
  };

  const startPreview = async (s: Stream) => {
    setBusy((b) => ({ ...b, [s.id]: true }));
    try {
      await startStream(s.id);
      await load();
    } finally {
      setBusy((b) => ({ ...b, [s.id]: false }));
    }
  };

  const toggleRecording = async (id: number) => {
    setRecBusy((b) => ({ ...b, [id]: true }));
    try {
      if (recordings[id]?.status === "recording") await stopRecording(id);
      else await startRecording(id);
      await load();
    } finally {
      setRecBusy((b) => ({ ...b, [id]: false }));
    }
  };

  const commitName = async (id: number) => {
    const draft = (nameDraft[id] ?? "").trim();
    if (!draft) return;
    try {
      const info = await setRecordingName(id, draft);
      setRecordings((prev) => ({ ...prev, [id]: { ...prev[id], ...info } }));
      setNameDraft((prev) => ({ ...prev, [id]: info.name }));
    } catch (e) {
      setError(String(e));
    }
  };

  const anyRecording = Object.values(recordings).some((r) => r.status === "recording");

  const handleGlobalRec = async () => {
    setGlobalRecBusy(true);
    try {
      if (anyRecording) await stopAllRecordings();
      else await startAllRecordings();
      await load();
    } finally {
      setGlobalRecBusy(false);
    }
  };

  const openFilesWindow = (id: number) => {
    if (typeof window === "undefined") return;
    window.open(`/recordings/${id}`, "_blank", "noopener,noreferrer");
  };

  const toggleListen = (id: number) => {
    setListening((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  if (loading) {
    return <div className="loading"><span>Connecting to backend…</span></div>;
  }

  if (error) {
    return <div className="error-message">{error}</div>;
  }

  return (
    <>
      <div className="global-rec-bar">
        <button
          className={`global-rec-btn ${anyRecording ? "recording" : ""}`}
          onClick={handleGlobalRec}
          disabled={globalRecBusy}
        >
          {globalRecBusy ? "…" : anyRecording ? "⏹ Stop all recordings" : "⏺ Record all"}
        </button>
      </div>

      <div className="cards-grid">
        {streams.map((s) => {
          const rec = recordings[s.id];
          const isRecording = rec?.status === "recording";
          const isEncoding = isRecording && !!rec?.encoding;
          const isListening = !!listening[s.id];
          return (
            <div key={s.id} className={`card-panel ${s.status}`}>
              <AudioMonitor id={s.id} active={s.status === "running"} listening={isListening} />
              <div className="card-thumb">
                <Thumbnail id={s.id} active={s.status === "running"} />
                {isEncoding && (
                  <div className="rec-badge">
                    ● REC {formatElapsed(rec?.elapsed_sec)} · {formatBitrate(rec?.bitrate_kbps)}
                  </div>
                )}
                {isRecording && !isEncoding && (
                  <div className="rec-badge starting">● STARTING…</div>
                )}
              </div>

              <div className="card-footer">
                <div className="card-title">
                  <span className={`status-dot ${s.status}`} />
                  <span className="card-name">{s.name}</span>
                  {s.format && (
                    <span className="signal-format">{s.format}</span>
                  )}
                </div>
                <div className="audio-meter" title="Audio level (dBFS): 0 = max, -inf = silence">
                  <div className="audio-row">
                    <span className="audio-label">L</span>
                    <div className="audio-bar">
                      <div className="audio-mask" style={{ width: `${100 - levelPct(audio[s.id]?.l)}%` }} />
                    </div>
                    <span className="audio-db">{formatDb(audio[s.id]?.l)}</span>
                  </div>
                  <div className="audio-row">
                    <span className="audio-label">R</span>
                    <div className="audio-bar">
                      <div className="audio-mask" style={{ width: `${100 - levelPct(audio[s.id]?.r)}%` }} />
                    </div>
                    <span className="audio-db">{formatDb(audio[s.id]?.r)}</span>
                  </div>
                </div>
                <div className="card-actions">
                  {s.status !== "running" && (
                    <button
                      className={`badge ${s.status}`}
                      onClick={() => startPreview(s)}
                      disabled={busy[s.id]}
                    >
                      {busy[s.id] ? "…" : "Start"}
                    </button>
                  )}
                  {s.status === "running" && (
                    <button
                      className={`badge listen-btn ${isListening ? "active" : ""}`}
                      onClick={() => toggleListen(s.id)}
                      title={isListening ? "Stop audio monitor" : "Monitor input audio"}
                    >
                      {isListening ? "🔊" : "🔈"}
                    </button>
                  )}
                  <button
                    className={`badge rec-btn ${isRecording ? "recording" : "idle"}`}
                    onClick={() => toggleRecording(s.id)}
                    disabled={recBusy[s.id]}
                    title={isRecording ? "Stop recording" : "Start recording"}
                  >
                    {recBusy[s.id] ? "…" : isRecording ? "⏹" : "⏺"}
                  </button>
                  <button
                    className="badge files-btn"
                    onClick={() => openFilesWindow(s.id)}
                    title="Open recordings window"
                  >
                    Files
                  </button>
                </div>
              </div>

              <div className="rec-name-row">
                <label className="rec-name-label" htmlFor={`rec-name-${s.id}`}>
                  Rec name
                </label>
                <input
                  id={`rec-name-${s.id}`}
                  className="rec-name-input"
                  value={nameDraft[s.id] ?? ""}
                  disabled={isRecording}
                  placeholder={`ch${s.id}`}
                  onChange={(e) =>
                    setNameDraft((prev) => ({ ...prev, [s.id]: e.target.value }))
                  }
                  onBlur={() => commitName(s.id)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.currentTarget.blur();
                    }
                  }}
                  title="Filename prefix: {name}_{date}_{time}.mp4"
                />
                <span className="rec-name-hint">
                  {(nameDraft[s.id] || `ch${s.id}`).replace(/\s+/g, "_")}_YYYY-MM-DD_HH-MM-SS.mp4
                </span>
              </div>

              {s.error && <div className="error-bar">{s.error}</div>}
            </div>
          );
        })}
      </div>
    </>
  );
}
