"use client";

import { useEffect, useMemo, useState } from "react";
import { useParams } from "next/navigation";
import { deleteRecordingFile, fetchRecordingBlob, fetchRecordingFiles, type RecordingFile } from "@/lib/api";

export default function RecordingsPage() {
  const params = useParams<{ id: string }>();
  const id = Number(params?.id ?? "0");
  const [files, setFiles] = useState<RecordingFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyName, setBusyName] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [playerURL, setPlayerURL] = useState<string | null>(null);
  const [playingName, setPlayingName] = useState<string | null>(null);

  const validID = useMemo(() => Number.isInteger(id) && id > 0, [id]);

  const load = async () => {
    if (!validID) return;
    setLoading(true);
    try {
      const list = await fetchRecordingFiles(id);
      setFiles(list);
      setError(null);
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    const timer = setInterval(load, 5000);
    return () => {
      clearInterval(timer);
      if (playerURL) URL.revokeObjectURL(playerURL);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, validID]);

  const playFile = async (name: string) => {
    setBusyName(name);
    try {
      const blob = await fetchRecordingBlob(id, name);
      if (playerURL) URL.revokeObjectURL(playerURL);
      setPlayerURL(URL.createObjectURL(blob));
      setPlayingName(name);
    } finally {
      setBusyName(null);
    }
  };

  const downloadFile = async (name: string) => {
    setBusyName(name);
    try {
      const blob = await fetchRecordingBlob(id, name);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } finally {
      setBusyName(null);
    }
  };

  const deleteFile = async (name: string) => {
    const ok = window.confirm(`Delete recording "${name}"?`);
    if (!ok) return;

    setBusyName(name);
    try {
      await deleteRecordingFile(id, name);
      setFiles((prev) => prev.filter((f) => f.name !== name));
      if (playingName === name) {
        if (playerURL) URL.revokeObjectURL(playerURL);
        setPlayerURL(null);
        setPlayingName(null);
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setBusyName(null);
    }
  };

  if (!validID) {
    return <div className="recordings-page">Invalid channel id.</div>;
  }

  return (
    <div className="recordings-page">
      <div className="recordings-header">
        <h1>Channel {id} recordings</h1>
        <button className="global-rec-btn" onClick={load} disabled={loading}>
          {loading ? "Refreshing..." : "Refresh"}
        </button>
      </div>

      {error && <div className="error-message">{error}</div>}

      <div className="recordings-layout">
        <div className="recordings-list">
          {files.length === 0 ? (
            <div className="files-empty">No recordings found.</div>
          ) : (
            files.map((f) => (
              <div key={f.name} className="recording-row">
                <div className="recording-meta">
                  <div className="recording-name">{f.name}</div>
                  <div className="recording-sub">
                    {Math.round(f.size / 1024 / 1024)} MB
                  </div>
                </div>
                <div className="recording-actions">
                  <button className="badge files-btn" onClick={() => playFile(f.name)} disabled={busyName === f.name}>
                    {busyName === f.name ? "..." : "Play"}
                  </button>
                  <button className="badge" onClick={() => downloadFile(f.name)} disabled={busyName === f.name}>
                    Download
                  </button>
                  <button className="badge delete-btn" onClick={() => deleteFile(f.name)} disabled={busyName === f.name}>
                    Delete
                  </button>
                </div>
              </div>
            ))
          )}
        </div>

        <div className="recordings-player-wrap">
          {playerURL ? (
            <video className="recordings-player" controls src={playerURL} />
          ) : (
            <div className="files-empty">Select a file to play.</div>
          )}
        </div>
      </div>
    </div>
  );
}

