"use client";

import { useState } from "react";
import {
  isPlayoutOn,
  isPlayoutPaused,
  pausePlayout,
  resumePlayout,
  startPlayout,
  stopPlayout,
  updatePlayoutClient,
  type PlayoutClient,
} from "@/lib/api";
import { showDecodeCard } from "@/lib/workflow";
import { sortByChannelId } from "@/lib/sortChannels";
import { useWorkflows } from "@/hooks/useWorkflows";
import { useDashboard } from "@/hooks/useDashboard";
import HlsPreview from "@/components/HlsPreview";
import DecodeSettingsModal from "@/components/DecodeSettingsModal";
import MediaLibraryModal from "@/components/MediaLibraryModal";

function formatBitrate(kbps?: number): string {
  if (!kbps || kbps <= 0) return "--";
  if (kbps >= 1000) return `${(kbps / 1000).toFixed(1)} Mbit/s`;
  return `${kbps.toFixed(0)} kbit/s`;
}

function formatClock(sec?: number): string {
  if (sec == null || !Number.isFinite(sec) || sec < 0) return "--:--";
  const s = Math.floor(sec);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = s % 60;
  if (h > 0) {
    return `${h}:${String(m).padStart(2, "0")}:${String(r).padStart(2, "0")}`;
  }
  return `${m}:${String(r).padStart(2, "0")}`;
}

const METER_SEGMENTS = 24;
const METER_MIN_DB = -50;
const METER_MAX_DB = 0;

function hasAudioLevel(db?: number): boolean {
  return db !== undefined && !Number.isNaN(db) && db > -89;
}

function segmentZone(index: number): "green" | "yellow" | "red" {
  const db = METER_MIN_DB + ((index + 1) / METER_SEGMENTS) * (METER_MAX_DB - METER_MIN_DB);
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
    <div className="audio-col" title={label}>
      <div className="audio-segments" aria-hidden>
        {Array.from({ length: METER_SEGMENTS }, (_, i) => (
          <span key={i} className={`audio-seg ${segmentZone(i)}${i < lit ? " on" : ""}`} />
        ))}
      </div>
      <span className="audio-label">{label}</span>
    </div>
  );
}

function formatDisplay(code?: string): string {
  if (!code) return "—";
  const map: Record<string, string> = {
    Hi50: "1080i50",
    Hp50: "1080p50",
    Hi25: "1080i25",
    Hp25: "1080p25",
    Hp60: "1080p60",
    Hp30: "1080p30",
    Hp24: "1080p24",
  };
  return map[code] || code;
}

function basename(path: string): string {
  const parts = path.split(/[/\\]/);
  return parts[parts.length - 1] || path;
}

function cardTitle(c: PlayoutClient): string {
  if (c.source === "file") {
    if (c.file_name) return basename(c.file_name);
    return c.name?.trim() || `Decode ${c.id}`;
  }
  if (c.mode === "caller" && c.target?.trim()) return c.target.trim();
  if (c.mode === "listener") return `SRT :${c.port}`;
  return c.name?.trim() || `Decode ${c.id}`;
}

function cardMeta(c: PlayoutClient): string {
  const bits = [formatDisplay(c.format_code), (c.source || "srt").toUpperCase()];
  if (c.source === "file" && c.loop) bits.push("LOOP");
  return bits.join(" · ");
}

function waitingLabel(c: PlayoutClient): string {
  if (c.source === "file") return "File…";
  if (c.mode === "caller") return "Connecting…";
  return "Waiting";
}

export default function DecodeGrid() {
  const { loading, playout: clients, metersPlayout: audio } = useDashboard();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [listening, setListening] = useState<Record<number, boolean>>({});
  const [settingsId, setSettingsId] = useState<number | null>(null);
  const [mediaOpen, setMediaOpen] = useState(false);
  const { workflows } = useWorkflows();

  const withBusy = async (id: number, fn: () => Promise<unknown>) => {
    setBusy((b) => ({ ...b, [id]: true }));
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy((b) => ({ ...b, [id]: false }));
    }
  };

  const toggleLoop = async (c: PlayoutClient) => {
    const next = !c.loop;
    try {
      await updatePlayoutClient(c.id, { loop: next });
    } catch (e) {
      setError(String(e));
    }
  };

  const settingsClient = settingsId != null ? clients.find((c) => c.id === settingsId) ?? null : null;
  const visibleClients = sortByChannelId(clients.filter((c) => showDecodeCard(workflows, c.id)));

  if (!loading && visibleClients.length === 0) {
    return null;
  }

  return (
    <section className="io-section">
      <div className="io-section-head">
        <h2 className="io-section-title">Decode</h2>
        <button type="button" className="badge" onClick={() => setMediaOpen(true)}>
          Media
        </button>
      </div>

      {error && (
        <div className="error-message">
          {error}
          <button type="button" className="error-dismiss" onClick={() => setError(null)}>
            Dismiss
          </button>
        </div>
      )}

      {loading && clients.length === 0 ? (
        <div className="loading">
          <span>Loading…</span>
        </div>
      ) : visibleClients.length === 0 ? (
        <p className="io-section-empty">No channels.</p>
      ) : (
        <div className="cards-grid">
          {visibleClients.map((c) => {
            const on = isPlayoutOn(c.status);
            const paused = isPlayoutPaused(c.status);
            const playing = on && !paused;
            const hasMedia = c.status === "running";
            const isListening = !!listening[c.id];
            const isFile = c.source === "file";
            const title = cardTitle(c);
            return (
              <div key={c.id} className={`card-panel ${c.status}`}>
                <div className="card-stage">
                  <div className="card-thumb">
                    <HlsPreview
                      active={on}
                      listening={isListening}
                      playlistPath={`/hls/playout/${c.id}/preview.m3u8`}
                      sessionKey={`${c.id}-${c.status}-${c.source ?? "srt"}-${c.sending ? "live" : "idle"}`}
                    />
                    {on && (
                      <div className="thumb-badges">
                        <div className={`stream-badge${hasMedia || c.sending ? "" : " waiting"}`}>
                          {paused
                            ? "Paused"
                            : hasMedia || c.sending
                              ? isFile
                                ? "Playing"
                                : formatBitrate(c.bitrate_kbps) === "--"
                                  ? "Live"
                                  : formatBitrate(c.bitrate_kbps)
                              : waitingLabel(c)}
                        </div>
                        {isFile && (c.duration_sec ?? 0) > 0 && (
                          <div className="stream-badge waiting" title="Elapsed / remaining">
                            {formatClock(c.elapsed_sec)} / −{formatClock(c.remain_sec)}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                  <div className="audio-meter">
                    <SegmentedMeter label="L" db={audio[c.id]?.l} />
                    <SegmentedMeter label="R" db={audio[c.id]?.r} />
                  </div>
                </div>

                <div className="card-footer">
                  <div className="card-top">
                    <div className="card-identity">
                      <div className="card-title">
                        <span className={`input-badge ${c.status}`} title={`Output ${c.id}`}>
                          {c.id}
                        </span>
                        <span className="card-name" title={title}>
                          {title}
                        </span>
                      </div>
                      <div className="card-meta" title={cardMeta(c)}>
                        <span className="card-meta-item">{formatDisplay(c.format_code)}</span>
                        <span className="card-meta-sep">·</span>
                        <span className="card-meta-item">{(c.source || "srt").toUpperCase()}</span>
                      </div>
                    </div>
                    <div className="card-actions">
                      {isFile ? (
                        <>
                          {!on || paused ? (
                            <button
                              type="button"
                              className="stream-btn idle"
                              disabled={busy[c.id] || (!c.file_id && !paused)}
                              onClick={() =>
                                withBusy(c.id, async () => {
                                  if (paused) await resumePlayout(c.id);
                                  else await startPlayout(c.id);
                                })
                              }
                              title={paused ? "Resume" : "Play"}
                            >
                              {busy[c.id] ? "…" : "PLAY"}
                            </button>
                          ) : (
                            <button
                              type="button"
                              className="stream-btn streaming"
                              disabled={busy[c.id]}
                              onClick={() => withBusy(c.id, async () => pausePlayout(c.id))}
                              title="Pause"
                            >
                              {busy[c.id] ? "…" : "PAUSE"}
                            </button>
                          )}
                          <button
                            type="button"
                            className="badge"
                            disabled={busy[c.id] || !on}
                            onClick={() => withBusy(c.id, async () => stopPlayout(c.id))}
                            title="Stop"
                          >
                            STOP
                          </button>
                          <button
                            type="button"
                            className={`badge loop-btn${c.loop ? " active" : ""}`}
                            disabled={busy[c.id]}
                            onClick={() => toggleLoop(c)}
                            title={c.loop ? "Loop on" : "Loop off"}
                          >
                            LOOP
                          </button>
                        </>
                      ) : on ? (
                        <button
                          type="button"
                          className="stream-btn streaming"
                          onClick={() => withBusy(c.id, async () => stopPlayout(c.id))}
                          disabled={busy[c.id]}
                          title="Stop"
                        >
                          {busy[c.id] ? "…" : "STOP"}
                        </button>
                      ) : null}
                      {on && (
                        <button
                          type="button"
                          className={`badge listen-btn ${isListening ? "active" : ""}`}
                          onClick={() => setListening((prev) => ({ ...prev, [c.id]: !prev[c.id] }))}
                          title={isListening ? "Mute preview" : "Unmute preview"}
                        >
                          {isListening ? "🔊" : "🔈"}
                        </button>
                      )}
                      <button
                        type="button"
                        className="badge settings-btn"
                        onClick={() => setSettingsId(c.id)}
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

      <DecodeSettingsModal
        open={settingsId != null}
        client={settingsClient}
        onClose={() => setSettingsId(null)}
        onSaved={() => {}}
      />
      <MediaLibraryModal open={mediaOpen} onClose={() => setMediaOpen(false)} />
    </section>
  );
}
