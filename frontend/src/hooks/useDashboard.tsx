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
import {
  fetchDashboardSnapshot,
  type AudioLevels,
  type DashboardSnapshot,
  type PlayoutClient,
  type RecordingInfo,
  type SrtInfo,
  type Stream,
  type TcLoopInfo,
} from "@/lib/api";
import { mediaBase } from "@/lib/mediaBase";

const API_KEY = process.env.NEXT_PUBLIC_API_KEY ?? "";

export type { DashboardSnapshot };

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
  const u = new URL("/ws", mediaBase());
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
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

function snapshotToState(msg: DashboardSnapshot, connected: boolean): DashboardState {
  const rec: Record<number, RecordingInfo> = {};
  for (const r of msg.recordings ?? []) rec[r.id] = r;
  const srt: Record<number, SrtInfo> = {};
  for (const s of msg.srt ?? []) srt[s.id] = s;
  const tc: Record<number, TcLoopInfo> = {};
  for (const t of msg.tc ?? []) tc[t.id] = t;
  return {
    connected,
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
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const intentionalCloseRef = useRef(false);
  const mountedRef = useRef(false);

  const clearRetryTimer = useCallback(() => {
    if (retryTimerRef.current != null) {
      clearTimeout(retryTimerRef.current);
      retryTimerRef.current = null;
    }
  }, []);

  const applySnapshot = useCallback((msg: DashboardSnapshot, connected: boolean) => {
    if (!mountedRef.current) return;
    setState(snapshotToState(msg, connected));
  }, []);

  const connect = useCallback(() => {
    if (!mountedRef.current || typeof window === "undefined") return;

    clearRetryTimer();
    intentionalCloseRef.current = false;

    const existing = wsRef.current;
    if (existing && (existing.readyState === WebSocket.OPEN || existing.readyState === WebSocket.CONNECTING)) {
      return;
    }

    if (existing) {
      intentionalCloseRef.current = true;
      try {
        existing.close();
      } catch {
        /* ignore */
      }
      wsRef.current = null;
      intentionalCloseRef.current = false;
    }

    const sock = new WebSocket(wsURL());
    wsRef.current = sock;

    sock.onopen = () => {
      if (!mountedRef.current || wsRef.current !== sock) return;
      retryRef.current = 0;
      setState((prev) => ({ ...prev, connected: true }));
    };

    sock.onmessage = (ev) => {
      if (!mountedRef.current || wsRef.current !== sock) return;
      try {
        const msg = JSON.parse(String(ev.data)) as DashboardSnapshot;
        if (msg?.type !== "snapshot") return;
        applySnapshot(msg, true);
      } catch {
        /* ignore bad frames */
      }
    };

    sock.onclose = () => {
      if (!mountedRef.current || wsRef.current !== sock) return;
      wsRef.current = null;
      if (intentionalCloseRef.current) return;

      setState((prev) => ({ ...prev, connected: false }));
      const attempt = retryRef.current++;
      const delay = Math.min(10_000, 500 * 2 ** Math.min(attempt, 4));
      retryTimerRef.current = setTimeout(() => {
        retryTimerRef.current = null;
        connect();
      }, delay);
    };

    sock.onerror = () => {
      sock.close();
    };
  }, [applySnapshot, clearRetryTimer]);

  useEffect(() => {
    mountedRef.current = true;

    void fetchDashboardSnapshot()
      .then((msg) => applySnapshot(msg, false))
      .catch(() => {
        /* WS reconnect will retry */
      });

    connect();

    return () => {
      mountedRef.current = false;
      clearRetryTimer();
      intentionalCloseRef.current = true;
      try {
        wsRef.current?.close();
      } catch {
        /* ignore */
      }
      wsRef.current = null;
    };
  }, [connect, applySnapshot, clearRetryTimer]);

  const value = useMemo(() => state, [state]);
  return <DashboardContext.Provider value={value}>{children}</DashboardContext.Provider>;
}

export function useDashboard(): DashboardState {
  return useContext(DashboardContext);
}
