"use client";

import { useEffect, useState } from "react";
import {
  fetchEncodePresets,
  fetchLibraryCategories,
  startRecording,
  stopRecording,
  startSrt,
  stopSrt,
  type EncodePreset,
  type LibraryCategory,
  isCaptureOn,
} from "@/lib/api";
import { showEncodeCard } from "@/lib/workflow";
import { useWorkflows } from "@/hooks/useWorkflows";
import { useDashboard } from "@/hooks/useDashboard";
import HlsPreview from "@/components/HlsPreview";
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
  const {
    loading,
    streams,
    recordings,
    srtById,
    metersEncode: audio,
  } = useDashboard();
  const [presets, setPresets] = useState<EncodePreset[]>([]);
  const [categories, setCategories] = useState<LibraryCategory[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [recBusy, setRecBusy] = useState<Record<number, boolean>>({});
  const [srtBusy, setSrtBusy] = useState<Record<number, boolean>>({});
  const [listening, setListening] = useState<Record<number, boolean>>({});
  const [settingsId, setSettingsId] = useState<number | null>(null);
  const { workflows } = useWorkflows();

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
    return () => {
      window.removeEventListener("roc-presets-changed", refreshPresets);
      window.removeEventListener("roc-library-changed", refreshCategories);
    };
  }, []);

  const toggleRecording = async (id: number) => {
    setRecBusy((b) => ({ ...b, [id]: true }));
    try {
      if (recordings[id]?.status === "recording") await stopRecording(id);
      else await startRecording(id);
    } catch (e) {
      setError(String(e));
    } finally {
      setRecBusy((b) => ({ ...b, [id]: false }));
    }
  };

  const toggleSrt = async (id: number) => {
    setSrtBusy((b) => ({ ...b, [id]: true }));
    try {
      if (srtById[id]?.status === "streaming") await stopSrt(id);
      else await startSrt(id);
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
  const visibleStreams = streams.filter((s) => showEncodeCard(workflows, s.id));

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

      <section className="io-section">
        <div className="io-section-head">
          <h2 className="io-section-title">Encode</h2>
        </div>

      {loading && visibleStreams.length === 0 ? (
        <div className="loading">
          <span>Connecting to backend…</span>
        </div>
      ) : visibleStreams.length === 0 ? null : (
      <div className="cards-grid">
        {visibleStreams.map((s) => {
          const rec = recordings[s.id];
          const isRecording = rec?.status === "recording";
          const isEncoding = isRecording && !!rec?.encoding;
          const isListening = !!listening[s.id];
          const srtOn = srtById[s.id]?.status === "streaming";
          const activePreset = presets.find((p) => p.id === s.encode_preset);
          const cat = rec?.category || "_unsorted";
          const captureOn = isCaptureOn(s.status);
          const hasSignal = s.status === "running";
          const tslText = s.tsl_text?.trim();
          return (
            <div key={s.id} className={`card-panel ${s.status}`}>
              <div className="card-stage">
                <div className="card-thumb">
                  <HlsPreview
                    active={captureOn}
                    listening={isListening}
                    playlistPath={`/hls/${s.id}/preview.m3u8`}
                  />
                  {tslText && hasSignal && (
                    <div className="thumb-tsl-overlay">
                      <div className="tsl-badge" title={`TSL ${s.tsl_index ?? s.id}`}>
                        {tslText}
                      </div>
                    </div>
                  )}
                  {(isRecording || srtOn) && (
                    <div className="thumb-badges">
                      {isEncoding && (
                        <div className="rec-badge">
                          REC · {formatElapsed(rec?.elapsed_sec)} · {formatBitrate(rec?.bitrate_kbps)}
                        </div>
                      )}
                      {isRecording && !isEncoding && (
                        <div className="rec-badge starting">REC · STARTING…</div>
                      )}
                      {srtOn && (
                        <div
                          className={`stream-badge${srtById[s.id]?.sending ? "" : " waiting"}`}
                          title={srtById[s.id]?.publish_url || "SRT streaming"}
                        >
                          {srtById[s.id]?.sending
                            ? `STREAM · ${formatBitrate(srtById[s.id]?.bitrate_kbps)}`
                            : "STREAM · waiting for client"}
                        </div>
                      )}
                    </div>
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
                    <>
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
                    </>
                    {captureOn && (
                      <button
                        className={`badge listen-btn ${isListening ? "active" : ""}`}
                        onClick={() => toggleListen(s.id)}
                        title={isListening ? "Mute preview" : "Unmute preview"}
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
      )}
      </section>

      <ChannelSettingsModal
        open={settingsId != null}
        stream={settingsStream}
        recording={settingsId != null ? recordings[settingsId] ?? null : null}
        presets={presets}
        categories={categories}
        onClose={() => setSettingsId(null)}
        onSaved={() => {}}
      />
    </>
  );
}
