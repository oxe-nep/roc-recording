"use client";

import { useEffect, useState, useCallback } from "react";
import {
  fetchStreams,
  startStream,
  stopStream,
  fetchRecordings,
  startRecording,
  stopRecording,
  startAllRecordings,
  stopAllRecordings,
  type Stream,
  type RecordingInfo,
} from "@/lib/api";
import Thumbnail from "@/components/Thumbnail";

export default function StreamGrid() {
  const [streams, setStreams] = useState<Stream[]>([]);
  const [recordings, setRecordings] = useState<Record<number, RecordingInfo>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [recBusy, setRecBusy] = useState<Record<number, boolean>>({});
  const [globalRecBusy, setGlobalRecBusy] = useState(false);

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
      setError(null);
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const interval = setInterval(load, 3000);
    return () => clearInterval(interval);
  }, [load]);

  const toggle = async (s: Stream) => {
    setBusy((b) => ({ ...b, [s.id]: true }));
    try {
      if (s.status === "running") await stopStream(s.id);
      else await startStream(s.id);
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
          return (
            <div key={s.id} className={`card-panel ${s.status}`}>
              <div className="card-thumb">
                <Thumbnail id={s.id} active={s.status === "running"} />
                {isRecording && <div className="rec-badge">● REC</div>}
              </div>

              <div className="card-footer">
                <div className="card-title">
                  <span className={`status-dot ${s.status}`} />
                  <span className="card-name">{s.name}</span>
                  {s.format && (
                    <span className="signal-format">{s.format}</span>
                  )}
                </div>
                <div className="card-actions">
                  <button
                    className={`badge ${s.status}`}
                    onClick={() => toggle(s)}
                    disabled={busy[s.id]}
                  >
                    {busy[s.id] ? "…" : s.status === "running" ? "Stop" : "Start"}
                  </button>
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

              {s.error && <div className="error-bar">{s.error}</div>}
            </div>
          );
        })}
      </div>
    </>
  );
}
