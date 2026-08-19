"use client";

import { useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { fetchStreams, startStream, stopStream, type Stream } from "@/lib/api";

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
        Ansluter till backend…
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
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
      {streams.map((s) => (
        <div
          key={s.id}
          className="bg-slate-800 rounded-xl overflow-hidden border border-slate-700 flex flex-col"
        >
          {/* Preview-thumbnail / länk */}
          <Link
            href={`/stream/${s.id}`}
            className="block aspect-video bg-slate-900 relative group"
          >
            <div className="absolute inset-0 flex items-center justify-center text-slate-600 group-hover:text-slate-400 transition">
              {s.status === "running" ? (
                <span className="text-xs font-mono">▶ preview</span>
              ) : (
                <span className="text-xs font-mono">–</span>
              )}
            </div>
          </Link>

          {/* Info + kontroller */}
          <div className="p-3 flex items-center justify-between gap-2">
            <div className="min-w-0">
              <p className="text-sm font-medium text-white truncate">{s.name}</p>
              <StatusBadge status={s.status} />
            </div>
            <button
              onClick={() => toggle(s)}
              disabled={busy[s.id]}
              className={`shrink-0 text-xs px-3 py-1 rounded-lg font-medium transition disabled:opacity-40 ${
                s.status === "running"
                  ? "bg-red-700 hover:bg-red-600 text-white"
                  : "bg-emerald-700 hover:bg-emerald-600 text-white"
              }`}
            >
              {busy[s.id] ? "…" : s.status === "running" ? "Stoppa" : "Starta"}
            </button>
          </div>
          {s.error && (
            <p className="px-3 pb-2 text-xs text-red-400 truncate">{s.error}</p>
          )}
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
