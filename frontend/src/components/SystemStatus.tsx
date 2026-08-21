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

function fmtVram(mb?: number): string {
  if (mb == null || Number.isNaN(mb)) return "--";
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)}G`;
  return `${mb.toFixed(0)}M`;
}

function pct(n?: number): string {
  if (n == null || Number.isNaN(n)) return "--";
  return `${n.toFixed(0)}%`;
}

function Item({
  label,
  value,
  sub,
  title,
}: {
  label: string;
  value: string;
  sub?: string;
  title?: string;
}) {
  return (
    <span className="sys-item" title={title}>
      <span className="sys-label">{label}</span>
      <span className="sys-value">
        {value}
        {sub ? <span className="sys-sub">{sub}</span> : null}
      </span>
    </span>
  );
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
      <div className="sys-group" aria-label="Host">
        <span className="sys-group-label">Host</span>
        <Item label="CPU" value={pct(m.cpu_percent)} />
        <Item
          label="MEM"
          value={pct(m.mem_percent)}
          sub={`${fmtBytes(m.mem_used_bytes)}/${fmtBytes(m.mem_total_bytes)}`}
        />
        <Item
          label="DISK"
          value={pct(m.disk_percent)}
          sub={`${fmtBytes(m.disk_used_bytes)}/${fmtBytes(m.disk_total_bytes)}`}
          title={m.disk_path ? `Disk: ${m.disk_path}` : "Disk"}
        />
      </div>

      <div
        className={`sys-group${m.gpu_available ? "" : " muted"}`}
        aria-label="GPU"
        title={
          m.gpu_available
            ? `GPU compute ${pct(m.gpu_percent)} · VRAM ${fmtVram(m.gpu_mem_used_mb)}/${fmtVram(m.gpu_mem_total_mb)}`
            : "NVIDIA GPU not available"
        }
      >
        <span className="sys-group-label">GPU</span>
        {m.gpu_available ? (
          <>
            <Item label="NVENC" value={pct(m.nvenc_percent)} title="Encoder utilization" />
            <Item label="NVDEC" value={pct(m.nvdec_percent)} title="Decoder utilization" />
            <Item
              label="VRAM"
              value={`${fmtVram(m.gpu_mem_used_mb)}/${fmtVram(m.gpu_mem_total_mb)}`}
              title={`GPU compute ${pct(m.gpu_percent)}`}
            />
          </>
        ) : (
          <Item label="—" value="n/a" />
        )}
      </div>
    </div>
  );
}
