"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  createPlayoutClient,
  fetchPlayoutAudioLevels,
  fetchPlayoutClients,
  fetchPlayoutDevices,
  isPlayoutOn,
  startPlayout,
  stopPlayout,
  type AudioLevels,
  type PlayoutClient,
} from "@/lib/api";
import Thumbnail from "@/components/Thumbnail";
import AudioMonitor from "@/components/AudioMonitor";
import DecodeSettingsModal from "@/components/DecodeSettingsModal";

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

export default function DecodeGrid() {
  const [clients, setClients] = useState<PlayoutClient[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [creating, setCreating] = useState(false);
  const [audio, setAudio] = useState<Record<number, AudioLevels>>({});
  const [listening, setListening] = useState<Record<number, boolean>>({});
  const [settingsId, setSettingsId] = useState<number | null>(null);
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
    const pollAudio = async () => {
      const active = clientsRef.current.filter((c) => isPlayoutOn(c.status));
      if (active.length === 0) return;
      const updates: Record<number, AudioLevels> = {};
      await Promise.all(
        active.map(async (c) => {
          try {
            updates[c.id] = await fetchPlayoutAudioLevels(c.id);
          } catch {
            // ignore
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

  const addClient = async () => {
    setCreating(true);
    setError(null);
    try {
      const devices = await fetchPlayoutDevices().catch(() => []);
      const first = devices[0];
      const created = await createPlayoutClient({
        name: "",
        device: first?.name || "",
        format_code: first?.formats?.[0]?.code || "",
        mode: "listener",
        latency_ms: 120,
      });
      await load();
      setSettingsId(created.id);
    } catch (e) {
      setError(String(e));
    } finally {
      setCreating(false);
    }
  };

  const toggle = async (c: PlayoutClient) => {
    setBusy((b) => ({ ...b, [c.id]: true }));
    setError(null);
    try {
      if (isPlayoutOn(c.status)) await stopPlayout(c.id);
      else await startPlayout(c.id);
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy((b) => ({ ...b, [c.id]: false }));
    }
  };

  const settingsClient = settingsId != null ? clients.find((c) => c.id === settingsId) ?? null : null;

  return (
    <section className="io-section">
      <div className="io-section-head">
        <h2 className="io-section-title">Decode</h2>
        <button type="button" className="badge" onClick={addClient} disabled={creating}>
          {creating ? "…" : "+ Add client"}
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
          <span>Loading decode clients…</span>
        </div>
      ) : clients.length === 0 ? (
        <p className="io-section-empty">No decode clients yet — add one to receive SRT onto a DeckLink output.</p>
      ) : (
        <div className="cards-grid">
          {clients.map((c) => {
            const on = isPlayoutOn(c.status);
            const hasMedia = c.status === "running";
            const isListening = !!listening[c.id];
            return (
              <div key={c.id} className={`card-panel ${c.status}`}>
                <AudioMonitor
                  id={c.id}
                  active={on}
                  listening={isListening}
                  playlistPath={`/hls/playout/${c.id}/audio.m3u8`}
                />
                <div className="card-stage">
                  <div className="card-thumb">
                    <Thumbnail id={c.id} active={on} path={`/hls/playout/${c.id}/thumb.jpg`} />
                    {on && (
                      <div className="thumb-badges">
                        <div className={`stream-badge${hasMedia || c.sending ? "" : " waiting"}`}>
                          {hasMedia || c.sending
                            ? `DECODE · ${formatBitrate(c.bitrate_kbps) === "--" ? "live" : formatBitrate(c.bitrate_kbps)}`
                            : c.mode === "caller"
                              ? "DECODE · connecting…"
                              : "DECODE · waiting for publisher"}
                        </div>
                        {(c.reconnects ?? 0) > 0 && !(hasMedia || c.sending) && (
                          <div className="stream-badge waiting">reconnects {c.reconnects}</div>
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
                        <span className={`input-badge ${c.status}`} title={c.device_label || c.device || "No device"}>
                          {c.id}
                        </span>
                        <span className="card-name" title={c.name}>
                          {c.name}
                        </span>
                      </div>
                      <div
                        className="card-meta"
                        title={[
                          c.decklink_out ? c.device_label || c.device || null : "SRT preview",
                          c.format_code || null,
                          c.mode,
                          c.listen_url || c.target || null,
                        ]
                          .filter(Boolean)
                          .join(" · ")}
                      >
                        <span className="card-meta-item">
                          {c.decklink_out
                            ? c.device_label || c.device || "No device"
                            : "SRT preview"}
                        </span>
                        <span className="card-meta-sep">·</span>
                        <span className="card-meta-item">{c.format_code || "—"}</span>
                        <span className="card-meta-sep">·</span>
                        <span className="card-meta-item">
                          {c.mode === "listener" ? `:${c.port}` : c.target || "caller"}
                        </span>
                      </div>
                    </div>
                    <div className="card-actions">
                      <button
                        type="button"
                        className={`stream-btn ${on ? "streaming" : "idle"}`}
                        onClick={() => toggle(c)}
                        disabled={busy[c.id]}
                        title={on ? "Stop decode" : "Start decode"}
                      >
                        {busy[c.id] ? "…" : on ? "STOP" : "START"}
                      </button>
                      {hasMedia && (
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
    </section>
  );
}
