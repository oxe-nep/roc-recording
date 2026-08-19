"use client";

import { useEffect, useState, useCallback } from "react";
import { fetchStreams, startStream, stopStream, type Stream } from "@/lib/api";
import Thumbnail from "@/components/Thumbnail";

export default function StreamGrid() {
  const [streams, setStreams] = useState<Stream[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<Record<number, boolean>>({});

  const load = useCallback(async () => {
    try {
      const data = await fetchStreams();
      setStreams(data);
      setError(null);
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const interval = setInterval(load, 3000);
    return () => clearInterval(interval);
  }, [load]);

  const toggle = async (s: Stream) => {
    setBusy((b) => ({ ...b, [s.id]: true }));
    try {
      if (s.status === "running") {
        await stopStream(s.id);
      } else {
        await startStream(s.id);
      }
      await load();
    } finally {
      setBusy((b) => ({ ...b, [s.id]: false }));
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64 text-slate-400">
        Connecting to backend…
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg bg-red-900/40 border border-red-700 p-4 text-red-300">
        {error}
      </div>
    );
  }

  return (
    <div className="grid grid-cols-3 xl:grid-cols-4 gap-3">
      {streams.map((s) => (
        <div
          key={s.id}
          className="bg-slate-800 rounded-lg overflow-hidden border border-slate-700 flex flex-col"
        >
          <div className="aspect-video bg-slate-900 relative flex items-center justify-center">
            {s.status === "running" ? (
              <Thumbnail id={s.id} className="w-full h-full object-contain" />
            ) : (
              <span className="text-xs font-mono text-slate-600">no signal</span>
            )}
          </div>

          <div className="p-2 flex items-center justify-between gap-2">
            <div className="min-w-0">
              <p className="text-xs font-medium text-white truncate">{s.name}</p>
              <StatusBadge status={s.status} />
            </div>
            <button
              onClick={() => toggle(s)}
              disabled={busy[s.id]}
              className={`shrink-0 text-[11px] px-2 py-1 rounded-md font-medium transition disabled:opacity-40 ${
                s.status === "running"
                  ? "bg-red-700 hover:bg-red-600 text-white"
                  : "bg-emerald-700 hover:bg-emerald-600 text-white"
              }`}
            >
              {busy[s.id] ? "…" : s.status === "running" ? "Stop" : "Start"}
            </button>
          </div>
          {s.error && <p className="px-2 pb-2 text-[11px] text-red-400 truncate">{s.error}</p>}
        </div>
      ))}
    </div>
  );
}

function StatusBadge({ status }: { status: Stream["status"] }) {
  const map = {
    running: "bg-emerald-500/20 text-emerald-400",
    stopped: "bg-slate-500/20 text-slate-400",
    error: "bg-red-500/20 text-red-400",
  };
  return (
    <span className={`text-xs px-2 py-0.5 rounded-full ${map[status]}`}>
      {status}
    </span>
  );
}
