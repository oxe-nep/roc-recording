"use client";

import { useEffect, useState, useCallback } from "react";
import {
  fetchStreams,
  startStream,
  stopStream,
  fetchRecordings,
  fetchRecordingFiles,
  fetchRecordingBlob,
  startRecording,
  stopRecording,
  startAllRecordings,
  stopAllRecordings,
  type Stream,
  type RecordingInfo,
  type RecordingFile,
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
  const [filesOpen, setFilesOpen] = useState<Record<number, boolean>>({});
  const [filesBusy, setFilesBusy] = useState<Record<number, boolean>>({});
  const [recFiles, setRecFiles] = useState<Record<number, RecordingFile[]>>({});
  const [playerURL, setPlayerURL] = useState<Record<number, string>>({});

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

  const toggleFiles = async (id: number) => {
    const willOpen = !filesOpen[id];
    setFilesOpen((s) => ({ ...s, [id]: willOpen }));
    if (!willOpen) return;
    setFilesBusy((s) => ({ ...s, [id]: true }));
    try {
      const files = await fetchRecordingFiles(id);
      setRecFiles((s) => ({ ...s, [id]: files }));
    } finally {
      setFilesBusy((s) => ({ ...s, [id]: false }));
    }
  };

  const playFile = async (id: number, name: string) => {
    setFilesBusy((s) => ({ ...s, [id]: true }));
    try {
      const blob = await fetchRecordingBlob(id, name);
      const old = playerURL[id];
      if (old) URL.revokeObjectURL(old);
      const url = URL.createObjectURL(blob);
      setPlayerURL((s) => ({ ...s, [id]: url }));
    } finally {
      setFilesBusy((s) => ({ ...s, [id]: false }));
    }
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
                    onClick={() => toggleFiles(s.id)}
                    disabled={filesBusy[s.id]}
                    title="Show recording files"
                  >
                    {filesBusy[s.id] ? "…" : "Files"}
                  </button>
                </div>
              </div>

              {filesOpen[s.id] && (
                <div className="files-panel">
                  {(recFiles[s.id] ?? []).length === 0 ? (
                    <div className="files-empty">No recordings yet</div>
                  ) : (
                    <>
                      <div className="files-list">
                        {(recFiles[s.id] ?? []).slice(0, 5).map((f) => (
                          <button
                            key={f.name}
                            className="file-item"
                            onClick={() => playFile(s.id, f.name)}
                            disabled={filesBusy[s.id]}
                            title={f.name}
                          >
                            ▶ {f.name}
                          </button>
                        ))}
                      </div>
                      {playerURL[s.id] && (
                        <video className="recording-player" controls src={playerURL[s.id]} />
                      )}
                    </>
                  )}
                </div>
              )}

              {s.error && <div className="error-bar">{s.error}</div>}
            </div>
          );
        })}
      </div>
    </>
  );
}
