"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import {
  fetchStreams,
  startStream,
  fetchRecordings,
  fetchAudioLevels,
  fetchEncodePresets,
  setEncodePreset,
  fetchLibraryCategories,
  setRecordingCategory,
  startRecording,
  stopRecording,
  setRecordingName,
  type Stream,
  type AudioLevels,
  type RecordingInfo,
  type EncodePreset,
  type LibraryCategory,
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

const METER_SEGMENTS = 24;
const METER_MIN_DB = -50;
const METER_MAX_DB = 0;

function hasAudioLevel(db?: number): boolean {
  return db !== undefined && !Number.isNaN(db) && db > -89;
}

function segmentZone(index: number): "green" | "yellow" | "red" {
  // dBFS at the top edge of this segment
  const db =
    METER_MIN_DB + ((index + 1) / METER_SEGMENTS) * (METER_MAX_DB - METER_MIN_DB);
  if (db <= -18) return "green";
  if (db <= -9) return "yellow";
  return "red";
}

function litSegmentCount(db?: number): number {
  if (!hasAudioLevel(db)) return 0;
  const clamped = Math.max(METER_MIN_DB, Math.min(METER_MAX_DB, db!));
  const pct = (clamped - METER_MIN_DB) / (METER_MAX_DB - METER_MIN_DB);
  return Math.round(pct * METER_SEGMENTS);
}

function SegmentedMeter({ db, label }: { db?: number; label: string }) {
  const lit = litSegmentCount(db);
  return (
    <div className="audio-row">
      <span className="audio-label">{label}</span>
      <div
        className="audio-segments"
        aria-hidden
        title="Green ≤ -18 dBFS · Yellow ≤ -9 · Red above -9"
      >
        {Array.from({ length: METER_SEGMENTS }, (_, i) => (
          <span
            key={i}
            className={`audio-seg ${segmentZone(i)}${i < lit ? " on" : ""}`}
          />
        ))}
      </div>
      <span className="audio-db">
        {hasAudioLevel(db) ? `${db!.toFixed(1)} dBFS` : "--.- dBFS"}
      </span>
    </div>
  );
}

export default function StreamGrid() {
  const [streams, setStreams] = useState<Stream[]>([]);
  const [presets, setPresets] = useState<EncodePreset[]>([]);
  const [categories, setCategories] = useState<LibraryCategory[]>([]);
  const [recordings, setRecordings] = useState<Record<number, RecordingInfo>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [recBusy, setRecBusy] = useState<Record<number, boolean>>({});
  const [presetBusy, setPresetBusy] = useState<Record<number, boolean>>({});
  const [audio, setAudio] = useState<Record<number, AudioLevels>>({});
  const [listening, setListening] = useState<Record<number, boolean>>({});
  const [nameDraft, setNameDraft] = useState<Record<number, string>>({});
  const streamsRef = useRef<Stream[]>([]);
  streamsRef.current = streams;

  const load = useCallback(async () => {
    try {
      const [streamData, recData, cats] = await Promise.all([
        fetchStreams(),
        fetchRecordings(),
        fetchLibraryCategories().catch(() => null),
      ]);
      setStreams(streamData);
      const recMap: Record<number, RecordingInfo> = {};
      for (const r of recData) recMap[r.id] = r;
      setRecordings(recMap);
      if (cats) setCategories(cats);
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
    const refreshPresets = () => {
      fetchEncodePresets()
        .then(setPresets)
        .catch(() => {});
    };
    const refreshCategories = () => {
      fetchLibraryCategories()
        .then(setCategories)
        .catch(() => {});
    };
    fetchEncodePresets()
      .then(setPresets)
      .catch((e) => setError(String(e)));
    refreshCategories();
    window.addEventListener("roc-presets-changed", refreshPresets);
    window.addEventListener("roc-library-changed", refreshCategories);
    load();
    const interval = setInterval(load, 1000);
    return () => {
      clearInterval(interval);
      window.removeEventListener("roc-presets-changed", refreshPresets);
      window.removeEventListener("roc-library-changed", refreshCategories);
    };
  }, [load]);

  useEffect(() => {
    let alive = true;
    const pollAudio = async () => {
      const running = streamsRef.current.filter((s) => s.status === "running");
      if (running.length === 0) return;
      const updates: Record<number, AudioLevels> = {};
      await Promise.all(
        running.map(async (s) => {
          try {
            updates[s.id] = await fetchAudioLevels(s.id);
          } catch {
            // ignore transient audio fetch errors
          }
        }),
      );
      if (!alive || Object.keys(updates).length === 0) return;
      setAudio((prev) => ({ ...prev, ...updates }));
    };
    const interval = setInterval(pollAudio, 500);
    pollAudio();
    return () => {
      alive = false;
      clearInterval(interval);
    };
  }, []);

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

  const changePreset = async (id: number, preset: string) => {
    setPresetBusy((b) => ({ ...b, [id]: true }));
    try {
      await setEncodePreset(id, preset);
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setPresetBusy((b) => ({ ...b, [id]: false }));
    }
  };

  const changeCategory = async (id: number, category: string) => {
    try {
      const info = await setRecordingCategory(id, category);
      setRecordings((prev) => ({ ...prev, [id]: { ...prev[id], ...info } }));
      const cats = await fetchLibraryCategories();
      setCategories(cats);
    } catch (e) {
      setError(String(e));
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

  useEffect(() => {
    window.dispatchEvent(
      new CustomEvent("roc-recording-state", { detail: { anyRecording } }),
    );
  }, [anyRecording]);

  const openLibrary = () => {
    window.dispatchEvent(new Event("roc-open-library"));
  };

  const toggleListen = (id: number) => {
    setListening((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  if (loading && streams.length === 0) {
    return <div className="loading"><span>Connecting to backend…</span></div>;
  }

  return (
    <>
      {error && (
        <div className="error-message">
          {error}
          <button type="button" className="error-dismiss" onClick={() => setError(null)}>
            Dismiss
          </button>
        </div>
      )}

      <div className="cards-grid">
        {streams.map((s) => {
          const rec = recordings[s.id];
          const isRecording = rec?.status === "recording";
          const isEncoding = isRecording && !!rec?.encoding;
          const isListening = !!listening[s.id];
          const activePreset = presets.find((p) => p.id === s.encode_preset);
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
                <div className="audio-meter" title="Sample peak (dBFS). Green ≤ -18 · Yellow ≤ -9 · Red above -9. Alignment tone ≈ -18.">
                  <SegmentedMeter label="L" db={audio[s.id]?.l} />
                  <SegmentedMeter label="R" db={audio[s.id]?.r} />
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
                    onClick={openLibrary}
                    title="Open recordings library"
                  >
                    Files
                  </button>
                </div>
              </div>

              <div
                className={`rec-settings ${isRecording ? "locked" : ""}`}
                title={isRecording ? "Locked while recording" : undefined}
              >
                <div className="rec-name-row">
                  <label className="rec-name-label" htmlFor={`encode-preset-${s.id}`}>
                    Encode
                  </label>
                  <select
                    id={`encode-preset-${s.id}`}
                    className="encode-preset-select"
                    value={s.encode_preset || ""}
                    disabled={isRecording || !!presetBusy[s.id] || presets.length === 0}
                    onChange={(e) => changePreset(s.id, e.target.value)}
                    title={
                      isRecording
                        ? "Locked while recording"
                        : activePreset
                          ? `${activePreset.label} · ${activePreset.video_bitrate} video / ${activePreset.audio_bitrate} audio · applies on next start`
                          : "Encode preset (applied when capture starts)"
                    }
                  >
                    {presets.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.label}
                      </option>
                    ))}
                  </select>
                  <span className="encode-preset-hint">
                    {activePreset
                      ? `${activePreset.video_bitrate} · ${activePreset.audio_bitrate}`
                      : s.encode_preset}
                  </span>
                </div>

                <div className="rec-name-row">
                  <label className="rec-name-label" htmlFor={`rec-cat-${s.id}`}>
                    Category
                  </label>
                  <select
                    id={`rec-cat-${s.id}`}
                    className="encode-preset-select"
                    value={rec?.category || "_unsorted"}
                    disabled={isRecording || categories.length === 0}
                    onChange={(e) => changeCategory(s.id, e.target.value)}
                    title={
                      isRecording
                        ? "Locked while recording"
                        : "Recordings are stored in recordings/{category}/"
                    }
                  >
                    {categories.map((c) => (
                      <option key={c.name} value={c.name}>
                        {c.name === "_unsorted" ? "Unsorted" : c.name}
                      </option>
                    ))}
                  </select>
                  <span className="encode-preset-hint">
                    recordings/{rec?.category || "_unsorted"}/
                  </span>
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
                    title={
                      isRecording
                        ? "Locked while recording"
                        : "Filename prefix: {name}_{date}_{time}.mp4"
                    }
                  />
                  <span className="rec-name-hint">
                    {(nameDraft[s.id] || `ch${s.id}`).replace(/\s+/g, "_")}_YYYY-MM-DD_HH-MM-SS.mp4
                  </span>
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
