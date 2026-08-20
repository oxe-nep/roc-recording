"use client";

import { useEffect, useState } from "react";
import { fetchSystemMetrics, type SystemMetrics } from "@/lib/api";

function fmtBytes(n: number): string {
  if (!n || Number.isNaN(n)) return "--";
  const gb = n / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(1)}G`;
  const mb = n / (1024 * 1024);
  return `${mb.toFixed(0)}M`;
}

export default function SystemStatus() {
  const [m, setM] = useState<SystemMetrics | null>(null);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const data = await fetchSystemMetrics();
        if (alive) setM(data);
      } catch {
        // ignore transient errors
      }
    };
    load();
    const t = setInterval(load, 2000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  if (!m) {
    return <div className="sys-status muted">Host…</div>;
  }

  return (
    <div className="sys-status" title="Capture host resource usage">
      <span className="sys-item">
        <span className="sys-label">CPU</span>
        <span className="sys-value">{m.cpu_percent.toFixed(0)}%</span>
      </span>
      <span className="sys-item">
        <span className="sys-label">MEM</span>
        <span className="sys-value">
          {m.mem_percent.toFixed(0)}%
          <span className="sys-sub">
            {fmtBytes(m.mem_used_bytes)}/{fmtBytes(m.mem_total_bytes)}
          </span>
        </span>
      </span>
      <span className="sys-item" title={m.disk_path ? `Disk: ${m.disk_path}` : "Disk"}>
        <span className="sys-label">DISK</span>
        <span className="sys-value">
          {(m.disk_percent ?? 0).toFixed(0)}%
          <span className="sys-sub">
            {fmtBytes(m.disk_used_bytes)}/{fmtBytes(m.disk_total_bytes)}
          </span>
        </span>
      </span>
      {m.gpu_available ? (
        <span
          className="sys-item"
          title={`NVENC encoder util. GPU compute ${(m.gpu_percent ?? 0).toFixed(0)}% · VRAM ${(m.gpu_mem_used_mb ?? 0).toFixed(0)}/${(m.gpu_mem_total_mb ?? 0).toFixed(0)} MB`}
        >
          <span className="sys-label">NVENC</span>
          <span className="sys-value">
            {(m.nvenc_percent ?? 0).toFixed(0)}%
            <span className="sys-sub">
              VRAM {(m.gpu_mem_used_mb ?? 0).toFixed(0)}/
              {(m.gpu_mem_total_mb ?? 0).toFixed(0)}
            </span>
          </span>
        </span>
      ) : (
        <span className="sys-item muted">
          <span className="sys-label">NVENC</span>
          <span className="sys-value">n/a</span>
        </span>
      )}
    </div>
  );
}
