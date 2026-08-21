"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import {
  fetchStreams,
  startStream,
  fetchRecordings,
  fetchAudioLevels,
  fetchEncodePresets,
  fetchLibraryCategories,
  fetchSrtAll,
  startRecording,
  stopRecording,
  startSrt,
  stopSrt,
  type Stream,
  type AudioLevels,
  type RecordingInfo,
  type EncodePreset,
  type LibraryCategory,
  type SrtInfo,
  isCaptureOn,
} from "@/lib/api";
import Thumbnail from "@/components/Thumbnail";
import AudioMonitor from "@/components/AudioMonitor";
import ChannelSettingsModal from "@/components/ChannelSettingsModal";

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
  const dbText = hasAudioLevel(db) ? `${db!.toFixed(1)} dBFS` : "— dBFS";
  return (
    <div
      className="audio-col"
      title={`${label}: ${dbText}. Green ≤ -18 · Yellow ≤ -9 · Red above -9`}
    >
      <div className="audio-segments" aria-hidden>
        {Array.from({ length: METER_SEGMENTS }, (_, i) => (
          <span
            key={i}
            className={`audio-seg ${segmentZone(i)}${i < lit ? " on" : ""}`}
          />
        ))}
      </div>
      <span className="audio-label">{label}</span>
    </div>
  );
}

export default function StreamGrid() {
  const [streams, setStreams] = useState<Stream[]>([]);
  const [presets, setPresets] = useState<EncodePreset[]>([]);
  const [categories, setCategories] = useState<LibraryCategory[]>([]);
  const [recordings, setRecordings] = useState<Record<number, RecordingInfo>>({});
  const [srtById, setSrtById] = useState<Record<number, SrtInfo>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [recBusy, setRecBusy] = useState<Record<number, boolean>>({});
  const [srtBusy, setSrtBusy] = useState<Record<number, boolean>>({});
  const [audio, setAudio] = useState<Record<number, AudioLevels>>({});
  const [listening, setListening] = useState<Record<number, boolean>>({});
  const [settingsId, setSettingsId] = useState<number | null>(null);
  const streamsRef = useRef<Stream[]>([]);
  streamsRef.current = streams;

  const load = useCallback(async () => {
    try {
      const [streamData, recData, cats, srtData] = await Promise.all([
        fetchStreams(),
        fetchRecordings(),
        fetchLibraryCategories().catch(() => null),
        fetchSrtAll().catch(() => null),
      ]);
      setStreams(streamData);
      const recMap: Record<number, RecordingInfo> = {};
      for (const r of recData) recMap[r.id] = r;
      setRecordings(recMap);
      if (cats) setCategories(cats);
      if (srtData) {
        const srtMap: Record<number, SrtInfo> = {};
        for (const s of srtData) srtMap[s.id] = s;
        setSrtById(srtMap);
      }
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
      const running = streamsRef.current.filter((s) => isCaptureOn(s.status));
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

  const toggleSrt = async (id: number) => {
    setSrtBusy((b) => ({ ...b, [id]: true }));
    try {
      if (srtById[id]?.status === "streaming") await stopSrt(id);
      else await startSrt(id);
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setSrtBusy((b) => ({ ...b, [id]: false }));
    }
  };

  const anyRecording = Object.values(recordings).some((r) => r.status === "recording");

  useEffect(() => {
    window.dispatchEvent(
      new CustomEvent("roc-recording-state", { detail: { anyRecording } }),
    );
  }, [anyRecording]);

  const toggleListen = (id: number) => {
    setListening((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const settingsStream = settingsId != null ? streams.find((s) => s.id === settingsId) ?? null : null;

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
          const srtOn = srtById[s.id]?.status === "streaming";
          const activePreset = presets.find((p) => p.id === s.encode_preset);
          const cat = rec?.category || "_unsorted";
          const captureOn = isCaptureOn(s.status);
          const hasSignal = s.status === "running";
          return (
            <div key={s.id} className={`card-panel ${s.status}`}>
              <AudioMonitor id={s.id} active={hasSignal} listening={isListening} />
              <div className="card-stage">
                <div className="card-thumb">
                  <Thumbnail id={s.id} active={captureOn} />
                  {isEncoding && (
                    <div className="rec-badge">
                      {formatElapsed(rec?.elapsed_sec)} · {formatBitrate(rec?.bitrate_kbps)}
                    </div>
                  )}
                  {isRecording && !isEncoding && (
                    <div className="rec-badge starting">STARTING…</div>
                  )}
                </div>
                <div
                  className="audio-meter"
                  title="Sample peak (dBFS). Green ≤ -18 · Yellow ≤ -9 · Red above -9. Alignment tone ≈ -18."
                >
                  <SegmentedMeter label="L" db={audio[s.id]?.l} />
                  <SegmentedMeter label="R" db={audio[s.id]?.r} />
                </div>
              </div>

              <div className="card-footer">
                <div className="card-top">
                  <div className="card-identity">
                    <div className="card-title">
                      <span
                        className={`input-badge ${s.status}`}
                        title={s.name || `Input ${s.id}`}
                      >
                        {s.id}
                      </span>
                      <span className="card-name" title={rec?.name || `ch${s.id}`}>
                        {rec?.name || `ch${s.id}`}
                      </span>
                    </div>
                    <div
                      className="card-meta"
                      title={[
                        s.format || (s.status === "waiting" ? "Waiting for signal" : null),
                        cat === "_unsorted" ? "Unsorted" : cat,
                        activePreset?.label || s.encode_preset || null,
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    >
                      {s.format ? (
                        <>
                          <span className="card-meta-item card-meta-format">{s.format}</span>
                          <span className="card-meta-sep">·</span>
                        </>
                      ) : s.status === "waiting" ? (
                        <>
                          <span className="card-meta-item card-meta-waiting">Waiting for signal</span>
                          <span className="card-meta-sep">·</span>
                        </>
                      ) : null}
                      <span className="card-meta-item">
                        {cat === "_unsorted" ? "Unsorted" : cat}
                      </span>
                      <span className="card-meta-sep">·</span>
                      <span className="card-meta-item">
                        {activePreset?.label || s.encode_preset || "—"}
                      </span>
                    </div>
                  </div>
                  <div className="card-actions">
                    <button
                      type="button"
                      className={`rec-btn ${isRecording ? "recording" : "idle"}`}
                      onClick={() => toggleRecording(s.id)}
                      disabled={recBusy[s.id] || (!hasSignal && !isRecording)}
                      title={
                        isRecording
                          ? "Stop recording"
                          : s.status === "waiting"
                            ? "Waiting for input signal"
                            : !hasSignal
                              ? "Start channel before recording"
                              : "Start recording"
                      }
                    >
                      {recBusy[s.id] ? "…" : "REC"}
                    </button>
                    <button
                      type="button"
                      className={`stream-btn ${srtOn ? "streaming" : "idle"}`}
                      onClick={() => toggleSrt(s.id)}
                      disabled={srtBusy[s.id] || (!hasSignal && !srtOn)}
                      title={
                        srtOn
                          ? srtById[s.id]?.publish_url || "Stop SRT stream"
                          : s.status === "waiting"
                            ? "Waiting for input signal"
                            : !hasSignal
                              ? "Start channel before streaming"
                              : "Start SRT stream (configure in settings)"
                      }
                    >
                      {srtBusy[s.id] ? "…" : "STREAM"}
                    </button>
                    {!captureOn && (
                      <button
                        className={`badge ${s.status}`}
                        onClick={() => startPreview(s)}
                        disabled={busy[s.id]}
                      >
                        {busy[s.id] ? "…" : "Start"}
                      </button>
                    )}
                    {hasSignal && (
                      <button
                        className={`badge listen-btn ${isListening ? "active" : ""}`}
                        onClick={() => toggleListen(s.id)}
                        title={isListening ? "Stop audio monitor" : "Monitor input audio"}
                      >
                        {isListening ? "🔊" : "🔈"}
                      </button>
                    )}
                    <button
                      type="button"
                      className="badge settings-btn"
                      onClick={() => setSettingsId(s.id)}
                      title="Channel settings"
                      aria-label="Settings"
                    >
                      ⚙
                    </button>
                  </div>
                </div>
              </div>
            </div>
          );
        })}
      </div>

      <ChannelSettingsModal
        open={settingsId != null}
        stream={settingsStream}
        recording={settingsId != null ? recordings[settingsId] ?? null : null}
        presets={presets}
        categories={categories}
        onClose={() => setSettingsId(null)}
        onSaved={load}
      />
    </>
  );
}
