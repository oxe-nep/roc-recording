"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type {
  AudioLevels,
  PlayoutClient,
  RecordingInfo,
  SrtInfo,
  Stream,
  TcLoopInfo,
} from "@/lib/api";

const BASE = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";
const API_KEY = process.env.NEXT_PUBLIC_API_KEY ?? "";

export type DashboardSnapshot = {
  type: "snapshot";
  streams: Stream[];
  playout: PlayoutClient[];
  tc: TcLoopInfo[];
  recordings: RecordingInfo[];
  srt: SrtInfo[];
  meters_encode: Record<string, AudioLevels>;
  meters_playout: Record<string, AudioLevels>;
};

type DashboardState = {
  connected: boolean;
  loading: boolean;
  streams: Stream[];
  playout: PlayoutClient[];
  tcById: Record<number, TcLoopInfo>;
  recordings: Record<number, RecordingInfo>;
  srtById: Record<number, SrtInfo>;
  metersEncode: Record<number, AudioLevels>;
  metersPlayout: Record<number, AudioLevels>;
};

const EMPTY: DashboardState = {
  connected: false,
  loading: true,
  streams: [],
  playout: [],
  tcById: {},
  recordings: {},
  srtById: {},
  metersEncode: {},
  metersPlayout: {},
};

const DashboardContext = createContext<DashboardState>(EMPTY);

function wsURL(): string {
  const base = BASE.trim();
  let http: string;
  if (!base) {
    http = typeof window !== "undefined" ? window.location.origin : "http://localhost:8080";
  } else {
    http = base;
  }
  const u = new URL(http);
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  u.pathname = "/ws";
  u.search = "";
  if (API_KEY) u.searchParams.set("api_key", API_KEY);
  return u.toString();
}

function mapMeters(raw?: Record<string, AudioLevels>): Record<number, AudioLevels> {
  const out: Record<number, AudioLevels> = {};
  if (!raw) return out;
  for (const [k, v] of Object.entries(raw)) {
    const id = Number(k);
    if (Number.isFinite(id) && v) out[id] = v;
  }
  return out;
}

function applySnapshot(msg: DashboardSnapshot): DashboardState {
  const rec: Record<number, RecordingInfo> = {};
  for (const r of msg.recordings ?? []) rec[r.id] = r;
  const srt: Record<number, SrtInfo> = {};
  for (const s of msg.srt ?? []) srt[s.id] = s;
  const tc: Record<number, TcLoopInfo> = {};
  for (const t of msg.tc ?? []) tc[t.id] = t;
  return {
    connected: true,
    loading: false,
    streams: msg.streams ?? [],
    playout: msg.playout ?? [],
    tcById: tc,
    recordings: rec,
    srtById: srt,
    metersEncode: mapMeters(msg.meters_encode),
    metersPlayout: mapMeters(msg.meters_playout),
  };
}

export function DashboardProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<DashboardState>(EMPTY);
  const retryRef = useRef(0);
  const wsRef = useRef<WebSocket | null>(null);

  const connect = useCallback(() => {
    if (typeof window === "undefined") return;
    try {
      wsRef.current?.close();
    } catch {
      /* ignore */
    }
    const sock = new WebSocket(wsURL());
    wsRef.current = sock;

    sock.onopen = () => {
      retryRef.current = 0;
      setState((prev) => ({ ...prev, connected: true }));
    };

    sock.onmessage = (ev) => {
      try {
        const msg = JSON.parse(String(ev.data)) as DashboardSnapshot;
        if (msg?.type !== "snapshot") return;
        setState(applySnapshot(msg));
      } catch {
        /* ignore bad frames */
      }
    };

    sock.onclose = () => {
      setState((prev) => ({ ...prev, connected: false }));
      const attempt = retryRef.current++;
      const delay = Math.min(10_000, 500 * 2 ** Math.min(attempt, 4));
      window.setTimeout(connect, delay);
    };

    sock.onerror = () => {
      sock.close();
    };
  }, []);

  useEffect(() => {
    connect();
    return () => {
      try {
        wsRef.current?.close();
      } catch {
        /* ignore */
      }
    };
  }, [connect]);

  const value = useMemo(() => state, [state]);
  return <DashboardContext.Provider value={value}>{children}</DashboardContext.Provider>;
}

export function useDashboard(): DashboardState {
  return useContext(DashboardContext);
}
