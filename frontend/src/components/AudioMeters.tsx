"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { useMeterLevels, type MeterBus } from "@/hooks/useDashboard";

const METER_SEGMENTS = 24;
const METER_MIN_DB = -50;
const METER_MAX_DB = 0;
const COLS = 4;
const COL_GAP = 3;
const SEG_GAP = 1;
const OFF = "rgba(255, 255, 255, 0.07)";
const GREEN = "#3fa34a";
const YELLOW = "#d4a017";
const RED = "#e74c3c";

function hasAudioLevel(db?: number): db is number {
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
  const clamped = Math.max(METER_MIN_DB, Math.min(METER_MAX_DB, db));
  const pct = (clamped - METER_MIN_DB) / (METER_MAX_DB - METER_MIN_DB);
  return Math.round(pct * METER_SEGMENTS);
}

function meterChannels(a?: { l: number; r: number; channels?: number[] }): number[] {
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

function zoneColor(zone: "green" | "yellow" | "red"): string {
  if (zone === "green") return GREEN;
  if (zone === "yellow") return YELLOW;
  return RED;
}

function drawBank(canvas: HTMLCanvasElement, dbs: number[]) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  if (w < 1 || h < 1) return;
  const pixelW = Math.round(w * dpr);
  const pixelH = Math.round(h * dpr);
  if (canvas.width !== pixelW) canvas.width = pixelW;
  if (canvas.height !== pixelH) canvas.height = pixelH;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  const colW = (w - COL_GAP * (COLS - 1)) / COLS;
  const segH = (h - SEG_GAP * (METER_SEGMENTS - 1)) / METER_SEGMENTS;
  for (let c = 0; c < COLS; c++) {
    const lit = litSegmentCount(dbs[c]);
    const x = c * (colW + COL_GAP);
    for (let i = 0; i < METER_SEGMENTS; i++) {
      const y = h - (i + 1) * segH - i * SEG_GAP;
      ctx.fillStyle = i < lit ? zoneColor(segmentZone(i)) : OFF;
      const hh = Math.max(1, segH);
      if (typeof ctx.roundRect === "function") {
        ctx.beginPath();
        ctx.roundRect(x, y, colW, hh, 1);
        ctx.fill();
      } else {
        ctx.fillRect(x, y, colW, hh);
      }
    }
  }
}

function MeterBank({ dbs, labels, title }: { dbs: number[]; labels: string[]; title: string }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const dbsRef = useRef(dbs);
  dbsRef.current = dbs;

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const paint = () => drawBank(canvas, dbsRef.current);
    paint();
    const ro = new ResizeObserver(paint);
    ro.observe(canvas);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (canvas) drawBank(canvas, dbs);
  }, [dbs]);

  const tip = labels
    .map((label, i) => {
      const db = dbs[i];
      const text = hasAudioLevel(db) ? `${db.toFixed(1)} dBFS` : "— dBFS";
      return `${label}: ${text}`;
    })
    .join(" · ");

  return (
    <div className="audio-bank" title={`${title}. Green ≤ -18 · Yellow ≤ -9 · Red above -9. ${tip}`}>
      <canvas ref={canvasRef} className="audio-meter-canvas" aria-hidden />
      <div className="audio-meter-labels">
        {labels.map((label) => (
          <span key={label} className="audio-label">
            {label}
          </span>
        ))}
      </div>
    </div>
  );
}

function LiveBank({
  channelId,
  bus,
  start,
}: {
  channelId: number;
  bus: MeterBus;
  start: 0 | 4;
}) {
  const levels = useMeterLevels(channelId, bus);
  const ch = meterChannels(levels);
  const labels = start === 0 ? ["1", "2", "3", "4"] : ["5", "6", "7", "8"];
  const title = start === 0 ? "Audio 1–4" : "Audio 5–8";
  return <MeterBank dbs={ch.slice(start, start + 4)} labels={labels} title={title} />;
}

export default function AudioMeters({
  channelId,
  bus,
  children,
}: {
  channelId: number;
  bus: MeterBus;
  children: ReactNode;
}) {
  return (
    <>
      <div className="audio-meter audio-meter-left">
        <LiveBank channelId={channelId} bus={bus} start={0} />
      </div>
      {children}
      <div className="audio-meter audio-meter-right">
        <LiveBank channelId={channelId} bus={bus} start={4} />
      </div>
    </>
  );
}
