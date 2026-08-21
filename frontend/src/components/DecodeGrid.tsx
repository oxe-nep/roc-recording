"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  fetchPlayoutAudioLevels,
  fetchPlayoutClients,
  isPlayoutOn,
  isPlayoutPaused,
  pausePlayout,
  resumePlayout,
  startPlayout,
  stopPlayout,
  updatePlayoutClient,
  type AudioLevels,
  type PlayoutClient,
} from "@/lib/api";
import Thumbnail from "@/components/Thumbnail";
import AudioMonitor from "@/components/AudioMonitor";
import DecodeSettingsModal from "@/components/DecodeSettingsModal";
import MediaLibraryModal from "@/components/MediaLibraryModal";

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
    <div className="audio-col" title={`${label}`}>
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

/** Primary card title: file name or SRT stream target. */
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
  if (c.source === "file") return "DECODE · file…";
  if (c.mode === "caller") return "DECODE · connecting…";
  return "DECODE · waiting for publisher";
}

export default function DecodeGrid() {
  const [clients, setClients] = useState<PlayoutClient[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [audio, setAudio] = useState<Record<number, AudioLevels>>({});
  const [listening, setListening] = useState<Record<number, boolean>>({});
  const [settingsId, setSettingsId] = useState<number | null>(null);
  const [mediaOpen, setMediaOpen] = useState(false);
  const clientsRef = useRef<PlayoutClient[]>([]);
  clientsRef.current = clients;

  const load = useCallback(async () => {
    try {
      const data = await fetchPlayoutClients();
      setClients(data);
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
    let alive = true;
    const silence: AudioLevels = { l: -90, r: -90 };
    const pollAudio = async () => {
      const all = clientsRef.current;
      const updates: Record<number, AudioLevels> = {};
      for (const c of all) {
        if (!isPlayoutOn(c.status) || isPlayoutPaused(c.status)) {
          updates[c.id] = silence;
        }
      }
      const active = all.filter((c) => isPlayoutOn(c.status) && !isPlayoutPaused(c.status));
      await Promise.all(
        active.map(async (c) => {
          try {
            updates[c.id] = await fetchPlayoutAudioLevels(c.id);
          } catch {
            updates[c.id] = silence;
          }
        }),
      );
      if (!alive) return;
      if (Object.keys(updates).length === 0) return;
      setAudio((prev) => ({ ...prev, ...updates }));
    };
    const interval = setInterval(pollAudio, 500);
    pollAudio();
    return () => {
      alive = false;
      clearInterval(interval);
    };
  }, []);

  const withBusy = async (id: number, fn: () => Promise<void>) => {
    setBusy((b) => ({ ...b, [id]: true }));
    setError(null);
    try {
      await fn();
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy((b) => ({ ...b, [id]: false }));
    }
  };

  const toggleLoop = async (c: PlayoutClient) => {
    await withBusy(c.id, async () => {
      const next = !c.loop;
      const wasOn = isPlayoutOn(c.status);
      if (wasOn) await stopPlayout(c.id);
      await updatePlayoutClient(c.id, { loop: next });
      if (wasOn) await startPlayout(c.id);
    });
  };

  const settingsClient = settingsId != null ? clients.find((c) => c.id === settingsId) ?? null : null;

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
          <span>Loading decode channels…</span>
        </div>
      ) : clients.length === 0 ? (
        <p className="io-section-empty">No decode channels yet — waiting for sinks on the host.</p>
      ) : (
        <div className="cards-grid">
          {clients.map((c) => {
            const on = isPlayoutOn(c.status);
            const paused = isPlayoutPaused(c.status);
            const playing = on && !paused;
            const hasMedia = c.status === "running";
            const isListening = !!listening[c.id];
            const isFile = c.source === "file";
            const title = cardTitle(c);
            return (
              <div key={c.id} className={`card-panel ${c.status}`}>
                <AudioMonitor
                  id={c.id}
                  active={playing}
                  listening={isListening}
                  playlistPath={`/hls/playout/${c.id}/audio.m3u8`}
                />
                <div className="card-stage">
                  <div className="card-thumb">
                    <Thumbnail id={c.id} active={on} path={`/hls/playout/${c.id}/thumb.jpg`} />
                    {on && (
                      <div className="thumb-badges">
                        <div className={`stream-badge${hasMedia || c.sending ? "" : " waiting"}`}>
                          {paused
                            ? "DECODE · paused"
                            : hasMedia || c.sending
                              ? `DECODE · ${formatBitrate(c.bitrate_kbps) === "--" ? "live" : formatBitrate(c.bitrate_kbps)}`
                              : waitingLabel(c)}
                        </div>
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
                            className={`badge${c.loop ? " active" : ""}`}
                            disabled={busy[c.id]}
                            onClick={() => toggleLoop(c)}
                            title="Toggle loop"
                          >
                            LOOP
                          </button>
                        </>
                      ) : (
                        <button
                          type="button"
                          className={`stream-btn ${on ? "streaming" : "idle"}`}
                          onClick={() =>
                            withBusy(c.id, async () => {
                              if (on) await stopPlayout(c.id);
                              else await startPlayout(c.id);
                            })
                          }
                          disabled={busy[c.id]}
                          title={on ? "Stop" : "Start"}
                        >
                          {busy[c.id] ? "…" : on ? "STOP" : "START"}
                        </button>
                      )}
                      {playing && (
                        <button
                          type="button"
                          className={`badge listen-btn ${isListening ? "active" : ""}`}
                          onClick={() => setListening((prev) => ({ ...prev, [c.id]: !prev[c.id] }))}
                          title={isListening ? "Stop audio monitor" : "Monitor decode audio"}
                        >
                          {isListening ? "🔊" : "🔈"}
                        </button>
                      )}
                      <button
                        type="button"
                        className="badge settings-btn"
                        onClick={() => setSettingsId(c.id)}
                        title="Decode settings"
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
        onSaved={load}
      />
      <MediaLibraryModal open={mediaOpen} onClose={() => setMediaOpen(false)} />
    </section>
  );
}
