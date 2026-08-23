"use client";

import { useState } from "react";
import { updateTcLoop } from "@/lib/api";
import { tcIsActive, tcPreviewHasSignal, tcSourceLabel, tcSourceShort } from "@/lib/tcUi";
import { showTcCard } from "@/lib/workflow";
import { sortByChannelId } from "@/lib/sortChannels";
import { useWorkflows } from "@/hooks/useWorkflows";
import { useDashboard } from "@/hooks/useDashboard";
import HlsPreview from "@/components/HlsPreview";
import TcSettingsModal from "@/components/TcSettingsModal";
import type { TcLoopInfo } from "@/lib/api";

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

function statusMeta(tc?: TcLoopInfo, live?: boolean): string {
  const code = tc?.timecode?.trim();
  if (code && code !== "--:--:--") {
    if (live) return code;
    if (tcIsActive(tc)) return code;
  }
  if (live) return `Live · ${tcSourceShort(tc?.source)}`;
  if (tc?.status === "restarting") return "Starting…";
  if (tc?.status === "error") return "Error";
  if (tcIsActive(tc)) return "Starting…";
  return "Off";
}

export default function TcGrid() {
  const { loading, streams, tcById, metersPlayout: audio } = useDashboard();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [listening, setListening] = useState<Record<number, boolean>>({});
  const [settingsId, setSettingsId] = useState<number | null>(null);
  const { workflows } = useWorkflows();

  const tcStreams = sortByChannelId(streams.filter((s) => showTcCard(workflows, s.id)));
  const channelIds = tcStreams.map((s) => s.id);

  const stopTc = async (id: number) => {
    setBusy((b) => ({ ...b, [id]: true }));
    setError(null);
    try {
      await updateTcLoop(id, { enabled: false });
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy((b) => ({ ...b, [id]: false }));
    }
  };

  if (!loading && channelIds.length === 0) {
    return null;
  }

  return (
    <section className="io-section io-section-tc">
      <div className="io-section-head">
        <h2 className="io-section-title">TC burn-in</h2>
      </div>

      {error && (
        <div className="error-message">
          {error}
          <button type="button" className="error-dismiss" onClick={() => setError(null)}>
            Dismiss
          </button>
        </div>
      )}

      {loading && channelIds.length === 0 ? (
        <div className="loading">
          <span>Loading…</span>
        </div>
      ) : (
        <div className="cards-grid">
          {tcStreams.map((s) => {
              const tc = tcById[s.id];
              const tcOn = tcIsActive(tc);
              const tcLive = tcPreviewHasSignal(tc);
              const isListening = !!listening[s.id];
              const tslText = s.tsl_text?.trim();
              return (
                <div
                  key={s.id}
                  className={`card-panel ${s.status}${tcOn ? " tc-active" : ""}${tcLive ? " tc-live" : ""}`}
                >
                  <div className="card-stage">
                    <div className="card-thumb">
                      <HlsPreview
                        active={tcLive}
                        listening={isListening}
                        playlistPath={`/hls/playout/${s.id}/preview.m3u8`}
                      />
                      {tslText && tcOn && (
                        <div className="thumb-tsl-overlay">
                          <div className="tsl-badge" title={`TSL ${s.tsl_index ?? s.id}`}>
                            {tslText}
                          </div>
                        </div>
                      )}
                      {tcOn && !tcLive && (
                        <div className="thumb-badges">
                          <div
                            className={`tc-badge${tc?.status === "error" ? " error" : " starting"}`}
                            title={tcSourceLabel(tc?.source, tc?.udp_port, s.id)}
                          >
                            {tc?.status === "error" ? "Error" : "Starting…"}
                          </div>
                        </div>
                      )}
                    </div>
                    <div className="audio-meter">
                      <SegmentedMeter label="L" db={audio[s.id]?.l} />
                      <SegmentedMeter label="R" db={audio[s.id]?.r} />
                    </div>
                  </div>

                  <div className="card-footer">
                    <div className="card-top">
                      <div className="card-identity">
                        <div className="card-title">
                          <span className={`input-badge ${s.status}`}>{s.id}</span>
                          <span className="card-name">Channel {s.id}</span>
                        </div>
                        <div
                          className="card-meta"
                          title={tcOn ? tcSourceLabel(tc?.source, tc?.udp_port, s.id) : undefined}
                        >
                          <span className="card-meta-item card-meta-tc">{statusMeta(tc, tcLive)}</span>
                        </div>
                      </div>
                      <div className="card-actions">
                        {tcOn ? (
                          <button
                            type="button"
                            className="tc-stop-btn"
                            disabled={busy[s.id]}
                            onClick={() => stopTc(s.id)}
                          >
                            {busy[s.id] ? "…" : "STOP"}
                          </button>
                        ) : null}
                        {tcLive && (
                          <button
                            type="button"
                            className={`badge listen-btn ${isListening ? "active" : ""}`}
                            onClick={() => setListening((prev) => ({ ...prev, [s.id]: !prev[s.id] }))}
                            title={isListening ? "Mute" : "Unmute"}
                          >
                            {isListening ? "🔊" : "🔈"}
                          </button>
                        )}
                        <button
                          type="button"
                          className="badge settings-btn"
                          onClick={() => setSettingsId(s.id)}
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

      <TcSettingsModal
        open={settingsId != null}
        channelId={settingsId}
        onClose={() => setSettingsId(null)}
        onSaved={() => {}}
      />
    </section>
  );
}
