"use client";

import type { ReactNode } from "react";
import type { AudioLevels } from "@/lib/api";

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

function meterChannels(a?: AudioLevels): number[] {
  const out = Array.from({ length: 8 }, () => -90);
  if (a?.channels?.length) {
    for (let i = 0; i < 8 && i < a.channels.length; i++) out[i] = a.channels[i];
    return out;
  }
  if (a) {
    out[0] = a.l;
    out[1] = a.r;
  }
  return out;
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
          <span key={i} className={`audio-seg ${segmentZone(i)}${i < lit ? " on" : ""}`} />
        ))}
      </div>
      <span className="audio-label">{label}</span>
    </div>
  );
}

export default function AudioMeters({
  levels,
  children,
}: {
  levels?: AudioLevels;
  children: ReactNode;
}) {
  const ch = meterChannels(levels);
  return (
    <>
      <div className="audio-meter audio-meter-left" title="Audio 1–4">
        {ch.slice(0, 4).map((db, i) => (
          <SegmentedMeter key={i} label={String(i + 1)} db={db} />
        ))}
      </div>
      {children}
      <div className="audio-meter audio-meter-right" title="Audio 5–8">
        {ch.slice(4, 8).map((db, i) => (
          <SegmentedMeter key={i + 4} label={String(i + 5)} db={db} />
        ))}
      </div>
    </>
  );
}
